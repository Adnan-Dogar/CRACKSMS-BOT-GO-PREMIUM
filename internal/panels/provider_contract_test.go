package panels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/gorilla/websocket"
)

func isolatedBudget() *memoryBudget {
	return &memoryBudget{requests: map[string][]time.Time{}, pauses: map[string]time.Time{}, limits: map[string]int{}}
}
func contractAdapter(server *httptest.Server, kind string, gate RequestGate) *restAdapter {
	return newRESTAdapter(domain.Panel{ID: 77, BotInstanceID: 1, Name: "Account", Kind: kind, Config: map[string]any{"url": server.URL, "token": "fixture-scoped-token", "service": "WhatsApp", "includeFacets": "true"}}, server.Client(), gate)
}

func TestASPContractAndResumablePagination(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sms" || r.Header.Get("Authorization") != "Bearer fixture-scoped-token" || r.URL.Query().Get("token") != "" {
			t.Error("incorrect endpoint or token placement")
		}
		q := r.URL.Query()
		pages = append(pages, q.Get("page"))
		if q.Get("limit") != "100" || q.Get("service") != "WhatsApp" || q.Get("includeFacets") != "true" {
			t.Error("missing bounded query or filter")
		}
		for _, key := range []string{"from", "to"} {
			if _, e := time.Parse("2006-01-02", q.Get(key)); e != nil {
				t.Error("invalid UTC date")
			}
		}
		fmt.Fprint(w, `{"data":[{"id":"stable-sms","number":"+12025550123","sender":"WA","service":"WhatsApp","message":"Your verification code is 482910","otp":"482910","range_name":"US Verification","profit":0.012345,"currency":"USD","received_at":"2026-09-28T10:30:05Z"}],"pagination":{"totalPages":2,"hasMore":true}}`)
	}))
	defer server.Close()
	a := contractAdapter(server, "axon_asp", isolatedBudget())
	events, cursor, e := a.Poll(context.Background(), "")
	if e != nil || len(events) != 1 {
		t.Fatal(events, e)
	}
	event := events[0]
	if event.ProviderProfit != "0.012345" || event.Sender != "WA" || event.ProviderRecordID != "stable-sms" || event.Code != "482910" {
		t.Fatal("metadata lost", event)
	}
	var checkpoint restCursor
	_ = json.Unmarshal([]byte(cursor), &checkpoint)
	if checkpoint.Page != 2 || checkpoint.Done {
		t.Fatal("catch-up checkpoint lost", checkpoint)
	}
	// A new adapter resumes the persisted page, and renaming/rotating a token
	// leaves account record deduplication stable.
	p := a.panel
	p.Name = "Renamed"
	p.Config = map[string]any{"url": server.URL, "token": "fixture-scoped-token", "service": "WhatsApp", "includeFacets": "true"}
	resumed := newRESTAdapter(p, server.Client(), isolatedBudget())
	again, completed, e := resumed.Poll(context.Background(), cursor)
	if e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal([]byte(completed), &checkpoint)
	if checkpoint.Page != 1 || !checkpoint.Done || again[0].DedupKey != event.DedupKey {
		t.Fatal("restart/rename deduplication or completion failed")
	}
	checkpoint.Page = 2
	checkpoint.Done = false
	checkpoint.FreshAt = time.Time{}
	raw, _ := json.Marshal(checkpoint)
	_, next, e := resumed.Poll(context.Background(), string(raw))
	if e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal([]byte(next), &checkpoint)
	if checkpoint.Page != 2 || strings.Join(pages, ",") != "1,2,1" {
		t.Fatal("fresh page priority interrupted catch-up", pages, checkpoint)
	}
}

func TestAugesSharedBudgetNumbersAndStatistics(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-scoped-token" || r.URL.Query().Get("token") != "" {
			t.Error("credential placement")
		}
		switch r.URL.Path {
		case "/api/v1/iprn/numbers":
			if r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("range_name") != "US Premium" {
				t.Error("number filters")
			}
			fmt.Fprint(w, `{"success":true,"data":[{"number":"+12025551234","range_name":"US Premium","a2p_rate":0.0150}],"pagination":{"last_page":1,"total":1}}`)
		case "/api/v1/iprn/statistics":
			if r.URL.Query().Get("group_by") != "week" {
				t.Error("statistics grouping")
			}
			fmt.Fprint(w, `{"success":true,"summary":{"total_messages":2,"total_earnings":0.0150}}`)
		case "/api/v1/iprn/messages":
			fmt.Fprint(w, `{"success":true,"data":[{"source":"WhatsApp","number":"+12025551234","message":"Your code is 123456","rate":0.0150,"status":"delivered","received_at":"2026-09-28T10:30:05Z"},{"source":"WhatsApp","number":"+12025551234","message":"Your code is 123456","status":"failed","received_at":"2026-09-28T10:30:05Z"}],"pagination":{"last_page":1}}`)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	gate := isolatedBudget()
	a := contractAdapter(server, "augestel", gate)
	ctx := context.Background()
	if e := a.Test(ctx); e != nil {
		t.Fatal(e)
	}
	numbers, e := a.Numbers(ctx, 1, "US Premium")
	if e != nil || len(numbers.Data) != 1 || numbers.Data[0].Rate.String() != "0.0150" {
		t.Fatal(numbers, e)
	}
	if _, e = a.Statistics(ctx, "2026-09-01", "2026-09-28", "week"); e != nil {
		t.Fatal(e)
	}
	events, _, e := a.Poll(ctx, "")
	if e != nil || len(events) != 1 || events[0].DeliveryStatus != "delivered" || events[0].Sender != "WhatsApp" {
		t.Fatal("failed traffic reached OTP ingestion", events, e)
	}
	second := contractAdapter(server, "augestel", gate)
	if e = second.Test(ctx); e != nil {
		t.Fatal(e)
	}
	e = second.Test(ctx)
	var limited *ProviderError
	if !errors.As(e, &limited) || limited.Status != 429 || !limited.RetryAt.After(time.Now()) || requests.Load() != 5 {
		t.Fatal("shared key budget exceeded", requests.Load(), e)
	}
}

func TestProviderFailuresAndBackoff(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.Header().Set("Retry-After", "45")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"success":false,"error":{"code":"FIXTURE_ERROR","retry_after":30}}`)
			}))
			defer server.Close()
			a := contractAdapter(server, "augestel", isolatedBudget())
			e := a.Test(context.Background())
			var p *ProviderError
			if !errors.As(e, &p) || p.Status != status || p.Code != "FIXTURE_ERROR" || strings.Contains(SafeError(e), "fixture-scoped-token") {
				t.Fatal(e)
			}
			if status == 429 {
				if time.Until(p.RetryAt) < 44*time.Second {
					t.Fatal("Retry-After ignored")
				}
				_ = a.Test(context.Background())
				if count.Load() != 1 {
					t.Fatal("retried before budget renewal")
				}
			}
		})
	}
}

func TestSocketIOHandshakeHeartbeatAndSMS(t *testing.T) {
	pong := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("EIO") != "4" || r.URL.Query().Get("transport") != "websocket" || r.URL.Query().Get("token") != "fixture-token" {
			t.Error("missing socket credentials/protocol")
		}
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`0{"sid":"fixture","pingInterval":1000,"pingTimeout":3000}`))
		_, p, e := c.ReadMessage()
		if e != nil || string(p) != "40" {
			t.Error("missing namespace handshake")
			return
		}
		_ = c.WriteMessage(websocket.TextMessage, []byte(`40{"sid":"fixture"}`))
		_ = c.WriteMessage(websocket.TextMessage, []byte("2"))
		_, p, e = c.ReadMessage()
		if e != nil || string(p) != "3" {
			t.Error("missing pong")
			return
		}
		close(pong)
		_ = c.WriteMessage(websocket.TextMessage, []byte(`42["sms",{"id":"row-1","number":"+12025551234","service":"WhatsApp","sender":"WA","message":"Your code is 123456","otp":"123456","received_at":"2026-09-28T10:30:05Z"}]`))
		_, _, _ = c.ReadMessage()
	}))
	defer server.Close()
	a := newSocketIOAdapter(domain.Panel{ID: 88, Kind: "socketio", Config: map[string]any{"stream_url": "ws" + strings.TrimPrefix(server.URL, "http") + "/socket.io/", "token": "fixture-token"}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if e := a.Test(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-pong:
	case <-ctx.Done():
		t.Fatal("heartbeat absent")
	}
	if strings.Contains(a.ConnectionStatus(), "SMS received") {
		t.Fatal("delivery claimed before SMS processing")
	}
	events, _, e := a.Poll(ctx, "")
	if e != nil || len(events) != 1 || events[0].Service != "WhatsApp" {
		t.Fatal(events, e)
	}
	if a.ConnectionStatus() != "SMS received" {
		t.Fatal(a.ConnectionStatus())
	}
}

func TestSocketIOAuthenticationFailureAndCredentialSplit(t *testing.T) {
	fields, e := ParseSocketCredentials("wss://example.test:2087/socket.io/?token=secret%2Bvalue&user=owner&EIO=4&transport=websocket")
	if e != nil || fields["stream_url"] != "wss://example.test:2087/socket.io/" || fields["token"] != "secret+value" || fields["user"] != "owner" {
		t.Fatal("signed URL was not split", e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`0{"sid":"x"}`))
		_, _, _ = c.ReadMessage()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`44{"message":"denied"}`))
	}))
	defer server.Close()
	a := newSocketIOAdapter(domain.Panel{Config: map[string]any{"stream_url": "ws" + strings.TrimPrefix(server.URL, "http")}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e = a.Test(ctx)
	var p *ProviderError
	if !errors.As(e, &p) || p.Status != 401 {
		t.Fatal("namespace rejection accepted", e)
	}
}

func TestIVASNormalLoginAndChallengeRenewal(t *testing.T) {
	for _, challenge := range []bool{false, true} {
		t.Run(fmt.Sprint(challenge), func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" && r.Method == http.MethodGet {
					if challenge {
						fmt.Fprint(w, "<div>Verify you are human</div>")
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture", Path: "/"})
					fmt.Fprint(w, `<form action="/login"><input type="hidden" name="_token" value="fixture-csrf"></form>`)
					return
				}
				if r.URL.Path == "/login" && r.Method == http.MethodPost {
					posts.Add(1)
					_ = r.ParseForm()
					cookie, _ := r.Cookie("session")
					if r.Form.Get("_token") != "fixture-csrf" || r.Form.Get("email") != "fixture@example.test" || r.Form.Get("password") != "fixture-password" || cookie == nil {
						t.Error("normal CSRF/cookie login failed")
					}
					fmt.Fprint(w, "Signed in")
					return
				}
				if r.URL.Path == "/portal/live/my_sms" {
					fmt.Fprint(w, "Authenticated portal: a stream URL must be renewed manually")
					return
				}
				t.Error("unexpected portal route")
			}))
			defer server.Close()
			client := server.Client()
			client.Jar, _ = cookiejar.New(nil)
			a := newIVASAdapter(domain.Panel{Kind: "ivas", Config: map[string]any{"base_url": server.URL, "username": "fixture@example.test", "password": "fixture-password"}}, client)
			defer a.Close()
			e := a.Test(context.Background())
			if !errors.Is(e, ErrProviderAccessRequired) {
				t.Fatal("manual renewal not reported", e)
			}
			if challenge && posts.Load() != 0 || !challenge && posts.Load() != 1 {
				t.Fatal("challenge bypass attempted or login absent", posts.Load())
			}
		})
	}
}

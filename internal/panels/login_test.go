package panels

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
)

func TestLoginDiscoversFormAndAgentSMSWithSessionRenewal(t *testing.T) {
	var posts, dataCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ints/login":
			http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "fixture", Path: "/ints"})
			fmt.Fprint(w, `<form method=post action="signin"><input value="a&amp;b" name="csrf_token" type="hidden"><input type=email name=account_login><input name=account_secret type=password></form>`)
		case "/ints/signin":
			posts.Add(1)
			_ = r.ParseForm()
			cookie, _ := r.Cookie("csrf")
			if r.Form.Get("account_login") != "fixture@example.test" || r.Form.Get("account_secret") != "fixture-password" || r.Form.Get("csrf_token") != "a&b" || cookie == nil || r.Header.Get("Referer") != "http://"+r.Host+"/ints/login" || r.Header.Get("Origin") != "http://"+r.Host {
				t.Error("form fields, hidden values or headers lost", r.Form)
			}
			http.SetCookie(w, &http.Cookie{Name: "auth", Value: "fixture", Path: "/ints"})
			http.Redirect(w, r, "/ints/agent/SMSDashboard", http.StatusSeeOther)
		case "/ints/agent/SMSDashboard":
			fmt.Fprint(w, `<a href="SMSCDRStats">SMS report</a><a>Logout</a>`)
		case "/ints/agent/SMSCDRStats":
			fmt.Fprint(w, `<script>table({"sAjaxSource":"res/data_smscdr.php?sesskey=a%2Bb"});</script>`)
		case "/ints/agent/res/data_smscdr.php":
			count := dataCalls.Add(1)
			if count == 2 {
				fmt.Fprint(w, `<form method=post><input name=user><input name=password type=password></form>`)
				return
			}
			auth, _ := r.Cookie("auth")
			if auth == nil || r.URL.Query().Get("sesskey") != "a+b" || r.URL.Query().Get("sEcho") != "1" || r.Header.Get("Referer") != "http://"+r.Host+"/ints/agent/SMSCDRStats" || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Error("SMS session/headers wrong", r.URL.Query())
			}
			want := "200"
			if count == 1 {
				want = "1"
			}
			if r.URL.Query().Get("iDisplayLength") != want {
				t.Error("incorrect record limit")
			}
			fmt.Fprint(w, `{"aaData":[["2026-09-30 10:00:00","+12025550123","WhatsApp","Your code is 123456"]]}`)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapter, err := NewAdapter(domain.Panel{ID: 7, Kind: "login", Config: map[string]any{"base_url": server.URL + "/ints", "username": "fixture@example.test", "password": "fixture-password"}})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if err = adapter.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, _, err := adapter.Poll(context.Background(), "")
	if err != nil || len(events) != 1 || events[0].Service != "WhatsApp" {
		t.Fatal(events, err)
	}
	if posts.Load() != 2 {
		t.Fatal("expired session was not renewed", posts.Load())
	}
}

func TestLoginAnswersVisibleArithmeticQuestion(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "Mozilla/5.0") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/ints/login":
			fmt.Fprint(w, `<form action="signin" method="post"><input type="hidden" name="csrf" value="token"><input name="username"><input type="password" name="password"><label>What is 4 + 7?</label><input type="number" name="answer"></form>`)
		case "/ints/signin":
			posts.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("username") != "fixture-user" || r.Form.Get("password") != "fixture-password" || r.Form.Get("answer") != "11" || r.Form.Get("csrf") != "token" {
				t.Error("arithmetic login form was submitted incorrectly")
			}
			http.Redirect(w, r, "/ints/agent/SMSDashboard", http.StatusSeeOther)
		case "/ints/agent/SMSDashboard":
			fmt.Fprint(w, `<a href="SMSCDRStats">SMS report</a><a>Logout</a><form method="post"><input name="username"><input type="password" name="new_password"></form>`)
		case "/ints/agent/SMSCDRStats":
			fmt.Fprint(w, `<script>table({"sAjaxSource":"res/data_smscdr.php?sesskey=fixture"});</script>`)
		case "/ints/agent/res/data_smscdr.php":
			fmt.Fprint(w, `{"aaData":[["2026-09-30 10:00:00","+12025550123","WhatsApp","Your code is 123456"]]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a, err := NewAdapter(domain.Panel{Kind: "login", Config: map[string]any{"base_url": server.URL + "/ints", "username": "fixture-user", "password": "fixture-password"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatal("expected one sign-in request")
	}
}

func TestLoginDoesNotAcceptFailedOrCrossHostForms(t *testing.T) {
	for _, mode := range []string{"invalid", "invalid-challenge", "cross-host", "missing-form", "challenge", "captcha-field", "bad-sms"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					if mode == "invalid" {
						fmt.Fprint(w, `<form method=post><input name=user><input type=password name=password></form>`)
					} else if mode == "invalid-challenge" {
						fmt.Fprint(w, `<a href="/dashboard">Dashboard</a><form method=post><input name=user><input type=password name=password><input name=capt type=number></form>`)
					} else {
						fmt.Fprint(w, `Logout Dashboard`)
					}
					return
				}
				if r.URL.Path == "/login" {
					switch mode {
					case "cross-host":
						fmt.Fprint(w, `<form action="https://other.example/signin" method=post><input name=user><input name=password type=password></form>`)
					case "missing-form":
						fmt.Fprint(w, `Welcome`)
					case "challenge":
						fmt.Fprint(w, `Verify you are human`)
					case "captcha-field":
						fmt.Fprint(w, `<form method=post><input name=username><input name=password type=password><input name=capt type=number></form>`)
					case "invalid-challenge":
						fmt.Fprint(w, `<form method=post><input name=user><input type=password name=password><label>What is 4 + 7?</label><input name=capt type=number></form>`)
					default:
						fmt.Fprint(w, `<form method=post><input name=user><input name=password type=password></form>`)
					}
					return
				}
				if strings.Contains(r.URL.Path, "data_smscdr") {
					fmt.Fprint(w, `{"dashboard":"not SMS"}`)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			a, _ := NewAdapter(domain.Panel{Kind: "login", Config: map[string]any{"base_url": server.URL, "username": "u", "password": "p"}})
			defer a.Close()
			if err := a.Test(context.Background()); err == nil {
				t.Fatal("invalid connection accepted")
			}
			if (mode == "cross-host" || mode == "missing-form" || mode == "challenge" || mode == "captcha-field") && posts.Load() != 0 {
				t.Fatal("unsafe or unverified form submitted")
			}
		})
	}
}
func TestLegacyAPIContractsAndErrors(t *testing.T) {
	for _, kind := range []string{"token_api", "legacy_api"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				if q.Get("token") != "fixture" || q.Get("cursor") != "" {
					t.Error("token/cursor parameters changed")
				}
				want := "200"
				if calls == 1 {
					want = "1"
				}
				if q.Get("records") != want {
					t.Error("test fetched an unbounded list")
				}
				if kind == "token_api" {
					if q.Get("dt1") == "" || q.Get("dt2") == "" || q.Get("fromdate") != "" {
						t.Error("CR parameters incorrect")
					}
					fmt.Fprint(w, `{"status":"Success","data":[{"dt":"2026-09-30 10:00:00","num":"+12025550123","cli":"WhatsApp","message":"Code 123456"}]}`)
				} else {
					if q.Get("fromdate") == "" || q.Get("todate") == "" || q.Get("dt1") != "" {
						t.Error("reseller parameters incorrect")
					}
					fmt.Fprint(w, `{"status":"Success","data":[{"datetime":"2026-09-30 10:00:00","number":"+12025550123","cli":"WhatsApp","message":"Code 123456"}]}`)
				}
			}))
			defer server.Close()
			apiType := "crapi"
			if kind == "legacy_api" {
				apiType = "reseller"
			}
			a, _ := NewAdapter(domain.Panel{Kind: kind, Config: map[string]any{"url": server.URL + "/mdr.php", "token": "fixture", "api_type": apiType}})
			defer a.Close()
			if err := a.Test(context.Background()); err != nil {
				t.Fatal(err)
			}
			events, _, err := a.Poll(context.Background(), "old-id")
			if err != nil || len(events) != 1 || events[0].ProviderTimestamp == nil || !events[0].ProviderTimestamp.Equal(time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)) {
				t.Fatal(events, err)
			}
		})
	}
	for _, body := range []string{`{}`, `{"status":"Invalid Token","data":[]}`, `{"status":"Error: bad credentials","data":[]}`, `{"data":[{"error":"bad token"}]}`} {
		if _, _, err := decodePanelResponse(strings.NewReader(body), domain.Panel{Kind: "token_api"}, ""); err == nil {
			t.Fatal("invalid API response accepted", body)
		}
	}
}

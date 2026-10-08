package panels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/gorilla/websocket"
)

type ConnectionReporter interface{ ConnectionStatus() string }

func ParseSocketCredentials(value string) (map[string]string, error) {
	u, e := url.Parse(strings.TrimSpace(value))
	if e != nil || u.Scheme != "wss" || u.Hostname() == "" || u.User != nil || len(value) > 16000 {
		return nil, errors.New("invalid secure stream URL")
	}
	q := u.Query()
	if q.Get("token") == "" {
		return nil, errors.New("authenticated stream token is missing")
	}
	token, user := q.Get("token"), q.Get("user")
	u.RawQuery = ""
	u.Fragment = ""
	return map[string]string{"stream_url": u.String(), "token": token, "user": user}, nil
}

type socketIOAdapter struct {
	panel               domain.Panel
	mu, writeMu         sync.Mutex
	conn                *websocket.Conn
	records             chan []byte
	failures            chan error
	heartbeat, received atomic.Bool
	closed              bool
}

func newSocketIOAdapter(p domain.Panel) *socketIOAdapter {
	return &socketIOAdapter{panel: p, records: make(chan []byte, 512), failures: make(chan error, 1)}
}
func (a *socketIOAdapter) ConnectionStatus() string {
	if a.received.Load() {
		return "SMS received"
	}
	if a.heartbeat.Load() {
		return "Heartbeat verified · waiting for SMS"
	}
	return "Authenticated socket · waiting for heartbeat/SMS"
}
func (a *socketIOAdapter) socketURL() (string, error) {
	endpoint := firstConfig(a.panel.Config, "stream_url", "url")
	u, e := url.Parse(endpoint)
	if e != nil || u.Hostname() == "" || u.User != nil {
		return "", errors.New("invalid Socket.IO endpoint")
	}
	if u.Scheme != "wss" && !(u.Scheme == "ws" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
		return "", errors.New("WSS is required")
	}
	q := u.Query()
	if token := stringConfig(a.panel.Config, "token"); token != "" {
		q.Set("token", token)
	}
	if user := stringConfig(a.panel.Config, "user"); user != "" {
		q.Set("user", user)
	}
	q.Set("EIO", "4")
	q.Set("transport", "websocket")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func firstConfig(c map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := stringConfig(c, k); v != "" {
			return v
		}
	}
	return ""
}
func (a *socketIOAdapter) write(conn *websocket.Conn, payload string) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, []byte(payload))
}
func (a *socketIOAdapter) connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("stream is closed")
	}
	select {
	case <-a.failures:
		if a.conn != nil {
			_ = a.conn.Close()
			a.conn = nil
		}
	default:
	}
	if a.conn != nil {
		return nil
	}
	a.heartbeat.Store(false)
	a.received.Store(false)
	endpoint, e := a.socketURL()
	if e != nil {
		return e
	}
	headers := http.Header{}
	origin := stringConfig(a.panel.Config, "origin")
	if origin == "" && a.panel.Kind == "ivas" {
		origin = "https://www.ivasms.com"
	}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	conn, resp, e := dialer.DialContext(ctx, endpoint, headers)
	if e != nil {
		if resp != nil {
			return &ProviderError{Status: resp.StatusCode, Code: "STREAM_HANDSHAKE_REJECTED"}
		}
		return errors.New("stream connection failed")
	}
	conn.SetReadLimit(4 << 20)
	ready := make(chan error, 1)
	go a.readLoop(conn, ready)
	select {
	case e = <-ready:
		if e != nil {
			conn.Close()
			return e
		}
	case <-ctx.Done():
		conn.Close()
		return ctx.Err()
	}
	a.conn = conn
	return nil
}
func (a *socketIOAdapter) readLoop(conn *websocket.Conn, ready chan<- error) {
	connected := false
	heartbeatWindow := 60 * time.Second
	fail := func(e error) {
		if !connected {
			select {
			case ready <- e:
			default:
			}
		}
		select {
		case a.failures <- e:
		default:
		}
		conn.Close()
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(heartbeatWindow))
		kind, payload, e := conn.ReadMessage()
		if e != nil {
			fail(errors.New("stream disconnected; reconnect required"))
			return
		}
		if kind != websocket.TextMessage || len(payload) == 0 {
			continue
		}
		packet := string(payload)
		switch packet[0] {
		case '0':
			var hello struct {
				PingInterval int `json:"pingInterval"`
				PingTimeout  int `json:"pingTimeout"`
			}
			if json.Unmarshal(payload[1:], &hello) != nil {
				fail(errors.New("invalid Engine.IO handshake"))
				return
			}
			if hello.PingInterval > 0 && hello.PingTimeout > 0 {
				heartbeatWindow = time.Duration(hello.PingInterval+hello.PingTimeout) * time.Millisecond
			}
			if e = a.write(conn, "40"); e != nil {
				fail(errors.New("Socket.IO authentication failed"))
				return
			}
		case '1':
			fail(errors.New("stream closed; session renewal required"))
			return
		case '2':
			a.heartbeat.Store(true)
			if e = a.write(conn, "3"+packet[1:]); e != nil {
				fail(errors.New("heartbeat response failed"))
				return
			}
		case '4':
			if strings.HasPrefix(packet, "40") {
				if !connected {
					connected = true
					ready <- nil
				}
				continue
			}
			if strings.HasPrefix(packet, "44") {
				fail(&ProviderError{Status: 401, Code: "STREAM_AUTHENTICATION_FAILED"})
				return
			}
			if strings.HasPrefix(packet, "41") {
				fail(errors.New("stream namespace disconnected"))
				return
			}
			if !strings.HasPrefix(packet, "42") {
				continue
			}
			record, e := socketEventPayload(packet)
			if e != nil {
				continue
			}
			select {
			case a.records <- record:
			case <-time.After(5 * time.Second):
				fail(errors.New("stream receive buffer full; reconnect required"))
				return
			}
		}
	}
}
func socketEventPayload(packet string) ([]byte, error) {
	start := strings.IndexByte(packet, '[')
	if start < 2 {
		return nil, errors.New("invalid Socket.IO event")
	}
	var event []json.RawMessage
	if e := json.Unmarshal([]byte(packet[start:]), &event); e != nil || len(event) < 2 {
		return nil, errors.New("invalid event payload")
	}
	var records any
	if e := json.Unmarshal(event[1], &records); e != nil {
		return nil, e
	}
	if text, ok := records.(string); ok {
		if e := json.Unmarshal([]byte(text), &records); e != nil {
			return nil, e
		}
	}
	return json.Marshal(records)
}
func (a *socketIOAdapter) Test(ctx context.Context) error { return a.connect(ctx) }
func (a *socketIOAdapter) Poll(ctx context.Context, cursor string) ([]domain.OTPEvent, string, error) {
	if e := a.connect(ctx); e != nil {
		return nil, cursor, e
	}
	events := []domain.OTPEvent{}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case record := <-a.records:
			decoded, next, e := decodePanelResponse(bytes.NewReader(record), a.panel, cursor)
			if e != nil {
				return nil, cursor, e
			}
			cursor = next
			events = append(events, decoded...)
			if len(decoded) > 0 {
				a.received.Store(true)
			}
			if len(events) >= 100 {
				return events, cursor, nil
			}
		case e := <-a.failures:
			a.mu.Lock()
			if a.conn != nil {
				a.conn.Close()
				a.conn = nil
			}
			a.mu.Unlock()
			if len(events) > 0 {
				return events, cursor, nil
			}
			return nil, cursor, e
		case <-timer.C:
			return events, cursor, nil
		case <-ctx.Done():
			if len(events) > 0 {
				return events, cursor, nil
			}
			return nil, cursor, ctx.Err()
		}
	}
}
func (a *socketIOAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

type ivasAdapter struct {
	panel  domain.Panel
	client *http.Client
	stream *socketIOAdapter
}

func newIVASAdapter(p domain.Panel, c *http.Client) *ivasAdapter {
	return &ivasAdapter{panel: p, client: c}
}
func (a *ivasAdapter) ConnectionStatus() string {
	if a.stream != nil {
		return a.stream.ConnectionStatus()
	}
	return "Session renewal required"
}
func (a *ivasAdapter) Close() error {
	a.client.CloseIdleConnections()
	if a.stream != nil {
		return a.stream.Close()
	}
	return nil
}
func (a *ivasAdapter) Test(ctx context.Context) error {
	return a.renewAuthentication(ctx, a.ensureStream(ctx))
}
func (a *ivasAdapter) Poll(ctx context.Context, cursor string) ([]domain.OTPEvent, string, error) {
	if e := a.Test(ctx); e != nil {
		return nil, cursor, e
	}
	events, next, e := a.stream.Poll(ctx, cursor)
	return events, next, a.renewAuthentication(ctx, e)
}
func (a *ivasAdapter) renewAuthentication(ctx context.Context, e error) error {
	var provider *ProviderError
	if errors.As(e, &provider) && provider.Status == 401 && stringConfig(a.panel.Config, "username") != "" && stringConfig(a.panel.Config, "password") != "" {
		if a.stream != nil {
			_ = a.stream.Close()
		}
		a.stream = nil
		// Only ordinary provider login may renew a session. A challenge is
		// reported to the administrator for manual renewal.
		original := a.panel.Config
		a.panel.Config = map[string]any{}
		for k, v := range original {
			if k != "stream_url" && k != "token" && k != "user" && !(k == "url" && strings.HasPrefix(fmt.Sprint(v), "ws")) {
				a.panel.Config[k] = v
			}
		}
		return a.ensureStream(ctx)
	}
	return e
}

var formAction = regexp.MustCompile(`(?i)<form[^>]*action=["']([^"']+)["']`)
var socketURLPattern = regexp.MustCompile(`wss://[^\s"'<>]+`)

func (a *ivasAdapter) ensureStream(ctx context.Context) error {
	if a.stream != nil {
		return a.stream.Test(ctx)
	}
	if endpoint := firstConfig(a.panel.Config, "stream_url", "url"); strings.HasPrefix(endpoint, "wss://") || strings.HasPrefix(endpoint, "ws://") {
		a.stream = newSocketIOAdapter(a.panel)
		return a.stream.Test(ctx)
	}
	endpoint := firstConfig(a.panel.Config, "base_url", "url")
	if endpoint == "" {
		endpoint = "https://www.ivasms.com/portal/live/my_sms"
	}
	base, e := url.Parse(endpoint)
	if e != nil || base.Scheme != "https" || base.Hostname() == "" {
		return errors.New("IVAS requires an HTTPS portal or WSS stream")
	}
	origin := base.Scheme + "://" + base.Host
	a.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Host != base.Host || req.URL.Scheme != "https" {
			return errors.New("portal redirect rejected")
		}
		return nil
	}
	fetch := func(method, target string, form url.Values) ([]byte, error) {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, e := http.NewRequestWithContext(ctx, method, target, body)
		if e != nil {
			return nil, e
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, e := a.client.Do(req)
		if e != nil {
			return nil, errors.New("IVAS portal connection failed")
		}
		defer resp.Body.Close()
		data, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if e != nil {
			return nil, e
		}
		if providerChallenge(data) {
			return nil, ErrProviderAccessRequired
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &ProviderError{Status: resp.StatusCode, Code: "PORTAL_ACCESS_FAILED"}
		}
		return data, nil
	}
	page, e := fetch(http.MethodGet, origin+"/login", nil)
	if e != nil {
		return e
	}
	username := stringConfig(a.panel.Config, "username")
	password := stringConfig(a.panel.Config, "password")
	if username == "" || password == "" {
		return ErrProviderAccessRequired
	}
	form := hiddenInputs(page)
	form.Set("email", username)
	form.Set("password", password)
	target := origin + "/login"
	if match := formAction.FindSubmatch(page); len(match) > 1 {
		candidate, e := url.Parse(html.UnescapeString(string(match[1])))
		if e != nil {
			return errors.New("invalid portal login form")
		}
		candidate = base.ResolveReference(candidate)
		if candidate.Host != base.Host || candidate.Scheme != "https" {
			return errors.New("portal login action rejected")
		}
		target = candidate.String()
	}
	if _, e = fetch(http.MethodPost, target, form); e != nil {
		return e
	}
	page, e = fetch(http.MethodGet, origin+"/portal/live/my_sms", nil)
	if e != nil {
		return e
	}
	if strings.Contains(strings.ToLower(string(page)), `name="password"`) {
		return &ProviderError{Status: 401, Code: "PORTAL_LOGIN_FAILED"}
	}
	clean := strings.ReplaceAll(html.UnescapeString(string(page)), `\/`, `/`)
	stream := socketURLPattern.FindString(clean)
	if stream == "" {
		return ErrProviderAccessRequired
	}
	p := a.panel
	p.Config = map[string]any{"stream_url": stream, "origin": origin}
	a.stream = newSocketIOAdapter(p)
	return a.stream.Test(ctx)
}

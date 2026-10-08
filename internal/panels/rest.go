package panels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

type ProviderError struct {
	Status  int
	Code    string
	RetryAt time.Time
}

func (e *ProviderError) Error() string { return fmt.Sprintf("provider HTTP %d (%s)", e.Status, e.Code) }

type RequestGate interface {
	TakeProviderRequest(context.Context, string, int) (time.Time, error)
	UpdateProviderBudget(context.Context, string, int, time.Time) error
}
type memoryBudget struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	pauses   map[string]time.Time
	limits   map[string]int
}

var defaultBudget = &memoryBudget{requests: map[string][]time.Time{}, pauses: map[string]time.Time{}, limits: map[string]int{}}

func (g *memoryBudget) TakeProviderRequest(ctx context.Context, key string, limit int) (time.Time, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n := g.limits[key]; n > 0 && n < limit {
		limit = n
	}
	now := time.Now()
	window := time.Minute
	if key == "augestel:source-ip" || strings.HasPrefix(key, "provider-ip:") {
		window = 20 * time.Minute
	}
	kept := []time.Time{}
	for _, t := range g.requests[key] {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	g.requests[key] = kept
	next := g.pauses[key]
	if len(kept) >= limit && kept[len(kept)-limit].Add(window).After(next) {
		next = kept[len(kept)-limit].Add(window)
	}
	if next.After(now) {
		return next, nil
	}
	g.requests[key] = append(kept, now)
	return time.Time{}, nil
}
func (g *memoryBudget) UpdateProviderBudget(ctx context.Context, key string, limit int, until time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if limit > 0 && (g.limits[key] == 0 || limit < g.limits[key]) {
		g.limits[key] = limit
	}
	if until.After(g.pauses[key]) {
		g.pauses[key] = until
	}
	return nil
}

func NewAdapterWithGate(panel domain.Panel, gate RequestGate) (Adapter, error) {
	a, e := NewAdapter(panel)
	if r, ok := a.(*restAdapter); ok && gate != nil {
		r.gate = gate
	}
	return a, e
}

type ProviderNumber struct {
	Number string      `json:"number"`
	Range  string      `json:"range_name"`
	Rate   json.Number `json:"a2p_rate"`
	Limit  int         `json:"portal_limit_a2p"`
}
type NumberPage struct {
	Data                  []ProviderNumber
	Page, LastPage, Total int
}
type NumberProvider interface {
	Numbers(context.Context, int, string) (NumberPage, error)
}
type StatisticsProvider interface {
	Statistics(context.Context, string, string, string) (map[string]any, error)
}
type restAdapter struct {
	panel  domain.Panel
	client *http.Client
	gate   RequestGate
	key    string
	ipKey  string
}

func newRESTAdapter(p domain.Panel, c *http.Client, g RequestGate) *restAdapter {
	if g == nil {
		g = defaultBudget
	}
	base := firstConfig(p.Config, "api_base", "url", "base_url")
	if base == "" {
		if p.Kind == "axon_asp" {
			base = "https://axonsms.xyz"
		} else {
			base = "https://augestel.com"
		}
	}
	u, _ := url.Parse(base)
	origin := ""
	if u != nil {
		scheme := strings.ToLower(u.Scheme)
		host := strings.ToLower(u.Hostname())
		port := u.Port()
		if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
			port = ""
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		if port != "" {
			host += ":" + port
		}
		origin = scheme + "://" + host
	}
	h := sha256.Sum256([]byte(origin + "\x00" + stringConfig(p.Config, "token")))
	ipHash := sha256.Sum256([]byte(origin))
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
			return errors.New("provider redirect rejected")
		}
		return nil
	}
	return &restAdapter{panel: p, client: c, gate: g, key: hex.EncodeToString(h[:]), ipKey: "provider-ip:" + hex.EncodeToString(ipHash[:])}
}
func (a *restAdapter) Close() error { a.client.CloseIdleConnections(); return nil }
func (a *restAdapter) Test(ctx context.Context) error {
	q := url.Values{"page": {"1"}}
	path := "messages"
	if a.panel.Kind == "axon_asp" {
		path = "sms"
		q.Set("limit", "1")
	} else {
		q.Set("per_page", "1")
	}
	_, e := a.request(ctx, path, q)
	return e
}

// RESTEndpoint exposes the adapter's exact endpoint selection for setup review.
func RESTEndpoint(kind, base, operation string) (string, error) {
	if kind != "axon_asp" && kind != "augestel" {
		return "", errors.New("not a REST API provider")
	}
	a := restAdapter{panel: domain.Panel{Kind: kind, Config: map[string]any{"api_base": base}}}
	return a.endpoint(operation)
}

func (a *restAdapter) endpoint(path string) (string, error) {
	explicitBase := stringConfig(a.panel.Config, "api_base")
	base := strings.TrimRight(firstConfig(a.panel.Config, "api_base", "url"), "/")
	if base == "" {
		base = strings.TrimRight(stringConfig(a.panel.Config, "base_url"), "/")
	}
	if base == "" {
		if a.panel.Kind == "axon_asp" {
			base = "https://axonsms.xyz"
		} else {
			base = "https://augestel.com"
		}
	}
	u, e := url.Parse(base)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid credential-free provider endpoint")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
		return "", errors.New("HTTPS is required")
	}
	if override := stringConfig(a.panel.Config, "api_path"); override != "" {
		if !strings.HasPrefix(override, "/") || strings.ContainsAny(override, "?#") || strings.HasPrefix(override, "//") {
			return "", errors.New("invalid API path")
		}
		u.Path = override
		u.RawPath = ""
		base = strings.TrimRight(u.String(), "/")
		explicitBase = base
	}
	if explicitBase != "" {
		if u.Path == "" || u.Path == "/" {
			base += domain.Profile(a.panel.Kind).APIPath
		}
		if a.panel.Kind == "augestel" {
			for _, suffix := range []string{"/numbers", "/messages", "/statistics"} {
				base = strings.TrimSuffix(base, suffix)
			}
			base += "/" + path
		}
		return base, nil
	}
	if a.panel.Kind == "axon_asp" {
		if !strings.HasSuffix(base, "/api/sms") {
			base += "/api/sms"
		}
	} else {
		for _, suffix := range []string{"/numbers", "/messages", "/statistics"} {
			base = strings.TrimSuffix(base, suffix)
		}
		if !strings.HasSuffix(base, "/api/v1/iprn") {
			base += "/api/v1/iprn"
		}
		base += "/" + path
	}
	return base, nil
}
func (a *restAdapter) request(ctx context.Context, path string, q url.Values) (map[string]any, error) {
	token := stringConfig(a.panel.Config, "token")
	if token == "" {
		return nil, &ProviderError{Status: 401, Code: "MISSING_TOKEN"}
	}
	limit := 30
	if a.panel.Kind == "augestel" {
		limit = 5
	}
	next, e := a.gate.TakeProviderRequest(ctx, a.key, limit)
	if e != nil {
		return nil, e
	}
	if !next.IsZero() {
		return nil, &ProviderError{Status: 429, Code: "LOCAL_REQUEST_BUDGET", RetryAt: next}
	}
	if a.panel.Kind == "augestel" {
		next, e = a.gate.TakeProviderRequest(ctx, a.ipKey, 1000)
		if e != nil {
			return nil, e
		}
		if !next.IsZero() {
			return nil, &ProviderError{Status: 429, Code: "SOURCE_IP_BUDGET", RetryAt: next}
		}
	}
	endpoint, e := a.endpoint(path)
	if e != nil {
		return nil, e
	}
	u, _ := url.Parse(endpoint)
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e := a.client.Do(req)
	if e != nil {
		return nil, errors.New("provider connection failed")
	}
	defer resp.Body.Close()
	var payload map[string]any
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	decoder.UseNumber()
	decodeErr := decoder.Decode(&payload)
	observed, _ := strconv.Atoi(resp.Header.Get("X-RateLimit-Limit"))
	remaining, remainingErr := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
	until := time.Time{}
	if resp.StatusCode == 429 {
		seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if seconds <= 0 {
			if t, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
				until = t
			}
		}
		if detail, ok := payload["error"].(map[string]any); ok && seconds <= 0 {
			seconds = intValue(detail["retry_after"])
		}
		if seconds <= 0 {
			seconds = 60
		}
		if until.IsZero() {
			until = time.Now().Add(time.Duration(seconds) * time.Second)
		}
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && time.Unix(reset, 0).After(until) {
			until = time.Unix(reset, 0)
		}
	} else if remainingErr == nil && remaining == 0 {
		until = time.Now().Add(time.Minute)
	}
	if e = a.gate.UpdateProviderBudget(ctx, a.key, observed, until); e != nil {
		return nil, e
	}
	code := ""
	if detail, ok := payload["error"].(map[string]any); ok {
		code = scalarString(detail["code"])
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ProviderError{Status: resp.StatusCode, Code: code, RetryAt: until}
	}
	if decodeErr != nil {
		return nil, errors.New("invalid provider JSON response")
	}
	if success, ok := payload["success"].(bool); ok && !success {
		return nil, &ProviderError{Status: 400, Code: code}
	}
	if _, ok := payload["data"]; path != "statistics" && !ok {
		return nil, errors.New("provider response is missing data")
	}
	return payload, nil
}
func intValue(v any) int { n, _ := strconv.Atoi(scalarString(v)); return n }

type restCursor struct {
	Version  int       `json:"v"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Page     int       `json:"page"`
	LastPage int       `json:"last_page"`
	FreshAt  time.Time `json:"fresh_at"`
	Done     bool      `json:"done"`
}

func (a *restAdapter) Poll(ctx context.Context, cursor string) ([]domain.OTPEvent, string, error) {
	now := time.Now().UTC()
	state := restCursor{Version: 1, From: now.Add(-24 * time.Hour).Format("2006-01-02"), To: now.Format("2006-01-02"), Page: 1}
	if cursor != "" {
		if e := json.Unmarshal([]byte(cursor), &state); e != nil {
			return nil, cursor, errors.New("invalid provider checkpoint")
		}
	}
	if state.Page < 1 {
		state.Page = 1
	}
	fresh := state.Page > 1 && !state.FreshAt.After(now)
	page := state.Page
	q := url.Values{"page": {strconv.Itoa(page)}}
	path := "messages"
	from, to := state.From, state.To
	if fresh {
		page = 1
		from = now.Add(-24 * time.Hour).Format("2006-01-02")
		to = now.Format("2006-01-02")
		q.Set("page", "1")
	}
	if a.panel.Kind == "axon_asp" {
		path = "sms"
		q.Set("limit", "100")
		q.Set("from", from)
		q.Set("to", to)
		q.Set("includeSummary", strconv.FormatBool(page == 1))
		for _, key := range []string{"search", "service", "sender", "number", "rangeName"} {
			if v := stringConfig(a.panel.Config, key); v != "" {
				q.Set(key, v)
			}
		}
		if stringConfig(a.panel.Config, "includeFacets") == "true" {
			q.Set("includeFacets", "true")
		}
	} else {
		q.Set("per_page", "200")
		q.Set("start_date", from)
		q.Set("end_date", to)
		q.Set("type", "all")
		if v := stringConfig(a.panel.Config, "number"); v != "" {
			q.Set("number", v)
		}
	}
	payload, e := a.request(ctx, path, q)
	if e != nil {
		return nil, cursor, e
	}
	records, ok := payload["data"].([]any)
	if !ok {
		return nil, cursor, errors.New("provider data must be a list")
	}
	events := []domain.OTPEvent{}
	for _, v := range records {
		record, ok := v.(map[string]any)
		if !ok {
			return nil, cursor, errors.New("invalid SMS record")
		}
		phone := firstString(record, "number")
		message := firstString(record, "message")
		if phone == "" || message == "" {
			continue
		}
		status := strings.ToLower(firstString(record, "status"))
		if a.panel.Kind == "augestel" && status != "delivered" {
			continue
		}
		timestamp := parseTime(firstString(record, "received_at"))
		if timestamp == nil {
			return nil, cursor, errors.New("provider SMS is missing a valid timestamp")
		}
		sender := firstString(record, "sender", "source")
		service := firstString(record, "service")
		id := firstString(record, "id")
		profit := firstString(record, "profit", "rate")
		if profit != "" {
			if n, e := strconv.ParseFloat(profit, 64); e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, cursor, errors.New("invalid provider payout")
			}
		}
		event := domain.OTPEvent{PanelID: a.panel.ID, PanelName: a.panel.Name, BotInstanceID: a.panel.BotInstanceID, Phone: phone, NormalizedPhone: store.NormalizePhone(phone), Service: service, Sender: sender, Message: message, Code: firstString(record, "otp"), ProviderTimestamp: timestamp, ReceivedAt: now, ProviderRecordID: id, DeliveryStatus: firstString(record, "status"), ProviderRange: firstString(record, "range_name"), ProviderProfit: profit, ProviderCurrency: firstString(record, "currency")}
		identity := id
		if identity == "" {
			identity = event.NormalizedPhone + "|" + strings.ToLower(service) + "|" + sender + "|" + timestamp.UTC().Format(time.RFC3339Nano) + "|" + message
		}
		event.DedupKey = store.DedupKey(fmt.Sprintf("%s:account:%d", a.panel.Kind, a.panel.ID), event.NormalizedPhone, identity)
		events = append(events, event)
	}
	pagination, ok := payload["pagination"].(map[string]any)
	if !ok {
		return nil, cursor, errors.New("provider pagination is missing")
	}
	last := intValue(pagination["last_page"])
	if a.panel.Kind == "axon_asp" {
		last = intValue(pagination["totalPages"])
	}
	if last < page {
		last = page
	}
	if fresh {
		state.FreshAt = now.Add(30 * time.Second)
	} else {
		state.Done = page >= last
		state.LastPage = last
		state.Page = page + 1
		if page >= last {
			state.From = now.Add(-24 * time.Hour).Format("2006-01-02")
			state.To = now.Format("2006-01-02")
			state.Page = 1
		}
		state.FreshAt = now.Add(30 * time.Second)
	}
	raw, _ := json.Marshal(state)
	return events, string(raw), nil
}
func (a *restAdapter) Numbers(ctx context.Context, page int, rangeName string) (NumberPage, error) {
	if a.panel.Kind != "augestel" {
		return NumberPage{}, errors.New("this provider does not publish a number inventory API")
	}
	if page < 1 {
		page = 1
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
	if rangeName != "" {
		q.Set("range_name", rangeName)
	}
	payload, e := a.request(ctx, "numbers", q)
	if e != nil {
		return NumberPage{}, e
	}
	raw, _ := json.Marshal(payload["data"])
	out := NumberPage{Page: page}
	if e = json.Unmarshal(raw, &out.Data); e != nil {
		return out, e
	}
	pagination, ok := payload["pagination"].(map[string]any)
	if !ok {
		return out, errors.New("number pagination is missing")
	}
	out.LastPage = intValue(pagination["last_page"])
	out.Total = intValue(pagination["total"])
	return out, nil
}
func (a *restAdapter) Statistics(ctx context.Context, from, to, group string) (map[string]any, error) {
	for _, date := range []string{from, to} {
		if _, e := time.Parse("2006-01-02", date); e != nil {
			return nil, errors.New("use YYYY-MM-DD dates")
		}
	}
	if from > to {
		return nil, errors.New("start date must precede end date")
	}
	if group != "day" && group != "week" && group != "month" {
		return nil, errors.New("group must be day, week or month")
	}
	if a.panel.Kind == "axon_asp" {
		return a.request(ctx, "sms", url.Values{"from": {from}, "to": {to}, "limit": {"1"}, "page": {"1"}, "includeSummary": {"true"}, "includeFacets": {"true"}})
	}
	return a.request(ctx, "statistics", url.Values{"start_date": {from}, "end_date": {to}, "group_by": {group}})
}

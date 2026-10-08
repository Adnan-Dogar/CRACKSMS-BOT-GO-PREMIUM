package panels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/gorilla/websocket"
)

type Adapter interface {
	Test(context.Context) error
	Poll(context.Context, string) ([]domain.OTPEvent, string, error)
	Close() error
}

func NewAdapter(panel domain.Panel) (Adapter, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 20 * time.Second, Jar: jar}
	if panel.Kind == "token_api" || panel.Kind == "legacy_api" {
		client.CheckRedirect = loginRedirectPolicy
	}
	switch panel.Kind {
	case "ivas":
		if len(panel.Config) == 0 {
			return nil, ErrProviderAccessRequired
		}
		return newIVASAdapter(panel, client), nil
	case "socketio":
		return newSocketIOAdapter(panel), nil
	case "axon_asp", "augestel":
		return newRESTAdapter(panel, client, nil), nil
	case "token_api", "legacy_api":
		return &httpAdapter{panel: panel, client: client}, nil
	case "login":
		client.CheckRedirect = loginRedirectPolicy
		return &loginAdapter{panel: panel, client: client}, nil
	case "websocket":
		return &websocketAdapter{panel: panel}, nil
	default:
		return nil, fmt.Errorf("unsupported panel kind %q", panel.Kind)
	}
}

type httpAdapter struct {
	panel  domain.Panel
	client *http.Client
}

func (a *httpAdapter) Test(ctx context.Context) error {
	_, _, err := a.poll(ctx, "", 1)
	return err
}

func (a *httpAdapter) Poll(ctx context.Context, cursor string) ([]domain.OTPEvent, string, error) {
	return a.poll(ctx, cursor, min(200, intConfig(a.panel.Config, "records", 200)))
}

func (a *httpAdapter) poll(ctx context.Context, cursor string, records int) ([]domain.OTPEvent, string, error) {
	endpoint := stringConfig(a.panel.Config, "url")
	if endpoint == "" {
		endpoint = stringConfig(a.panel.Config, "sms_url")
	}
	if endpoint == "" {
		return nil, cursor, errors.New("panel URL is missing")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, cursor, err
	}
	query := u.Query()
	token := stringConfig(a.panel.Config, "token")
	if token != "" {
		query.Set(defaultString(a.panel.Config, "token_param", "token"), token)
	}
	now := time.Now()
	apiType := strings.ToLower(stringConfig(a.panel.Config, "api_type"))
	switch apiType {
	case "old", "crapi", "":
		query.Set("dt1", now.Add(-24*time.Hour).Format("2006-01-02 15:04:05"))
		query.Set("dt2", now.Format("2006-01-02 15:04:05"))
	case "mo", "ps", "reseller":
		query.Set("fromdate", now.Add(-24*time.Hour).Format("2006-01-02 15:04:05"))
		query.Set("todate", now.Format("2006-01-02 15:04:05"))
	}
	query.Set("records", strconv.Itoa(records))
	if param := stringConfig(a.panel.Config, "cursor_param"); param != "" && cursor != "" {
		query.Set(param, cursor)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, cursor, err
	}
	setHeaders(req, a.panel.Config)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, cursor, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, cursor, legacyHTTPError(resp)
	}
	return decodePanelResponse(resp.Body, a.panel, cursor)
}

func (a *httpAdapter) Close() error {
	a.client.CloseIdleConnections()
	return nil
}

type websocketAdapter struct {
	panel domain.Panel
	mu    sync.Mutex
	conn  *websocket.Conn
}

func (a *websocketAdapter) Test(ctx context.Context) error {
	return a.connect(ctx)
}

func (a *websocketAdapter) connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn != nil {
		return nil
	}
	headers := http.Header{}
	if token := stringConfig(a.panel.Config, "token"); token != "" {
		headers.Set(defaultString(a.panel.Config, "token_header", "Authorization"), token)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, stringConfig(a.panel.Config, "url"), headers)
	if err != nil {
		return err
	}
	a.conn = conn
	return nil
}

func (a *websocketAdapter) Poll(ctx context.Context, cursor string) ([]domain.OTPEvent, string, error) {
	if err := a.connect(ctx); err != nil {
		return nil, cursor, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = a.conn.SetReadDeadline(deadline)
	_, payload, err := a.conn.ReadMessage()
	if err != nil {
		a.conn.Close()
		a.conn = nil
		if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			return nil, cursor, nil
		}
		return nil, cursor, err
	}
	return decodePanelResponse(bytes.NewReader(payload), a.panel, cursor)
}

func (a *websocketAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		return err
	}
	return nil
}

func decodePanelResponse(reader io.Reader, panel domain.Panel, cursor string) ([]domain.OTPEvent, string, error) {
	var payload any
	decoder := json.NewDecoder(io.LimitReader(reader, 10<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, cursor, fmt.Errorf("decode panel response: %w", err)
	}
	if object, ok := payload.(map[string]any); ok {
		if failed, ok := object["success"].(bool); ok && !failed {
			return nil, cursor, errors.New("provider rejected the request")
		}
		if status, ok := object["status"].(string); ok && (strings.Contains(strings.ToLower(status), "error") || strings.Contains(strings.ToLower(status), "fail") || strings.Contains(strings.ToLower(status), "invalid") || strings.Contains(strings.ToLower(status), "unauthorized")) {
			return nil, cursor, errors.New("provider rejected the request")
		}
		if detail, ok := object["error"].(string); ok && strings.TrimSpace(detail) != "" {
			return nil, cursor, errors.New("provider rejected the request")
		}
	}
	if panel.Kind == "token_api" || panel.Kind == "legacy_api" || panel.Kind == "login" {
		if !recordEnvelope(payload) {
			return nil, cursor, errors.New("provider response is not a recognized SMS list")
		}
	}
	records := collectRecords(payload)
	events := make([]domain.OTPEvent, 0, len(records))
	newCursor := cursor
	for _, record := range records {
		phone, service, message, timestamp, recordCursor := normalizeRecord(record)
		normalized := store.NormalizePhone(phone)
		if normalized == "" || strings.TrimSpace(message) == "" {
			continue
		}
		if recordCursor != "" {
			newCursor = recordCursor
		}
		dedupPayload := message
		if recordCursor != "" {
			dedupPayload = recordCursor + "|" + message
		} else if timestamp != nil {
			dedupPayload = timestamp.UTC().Format(time.RFC3339Nano) + "|" + message
		}
		event := domain.OTPEvent{
			BotInstanceID: panel.BotInstanceID, PanelID: panel.ID, PanelName: panel.Name, Phone: phone, NormalizedPhone: normalized,
			Service: service, Message: message, ProviderTimestamp: timestamp, ReceivedAt: time.Now(),
			LegacyDedupKey: store.DedupKey(fmt.Sprintf("%d:%s", panel.ID, panel.Name), normalized, dedupPayload),
			DedupKey:       store.DedupKey(fmt.Sprintf("panel:%d", panel.ID), normalized, strings.ToLower(strings.TrimSpace(service))+"|"+dedupPayload),
		}
		if fields, ok := record.(map[string]any); ok {
			event.Sender = firstString(fields, "sender", "source", "cli", "originator")
			event.Code = firstString(fields, "otp", "code")
			event.Country = firstString(fields, "country")
			event.ProviderRecordID = firstString(fields, "id", "message_id")
			event.DeliveryStatus = firstString(fields, "status")
			if event.DeliveryStatus != "delivered" && event.DeliveryStatus != "failed" && event.DeliveryStatus != "undelivered" {
				event.DeliveryStatus = ""
			}
			event.ProviderRange = firstString(fields, "range_name", "range")
		}
		events = append(events, event)
	}
	if (panel.Kind == "token_api" || panel.Kind == "legacy_api" || panel.Kind == "login") && len(records) > 0 && len(events) == 0 {
		return nil, cursor, errors.New("SMS list contains no recognizable records")
	}
	return events, newCursor, nil
}

func collectRecords(value any) []any {
	switch value := value.(type) {
	case []any:
		return value
	case map[string]any:
		for _, key := range []string{"data", "records", "sms", "messages", "aaData", "result"} {
			if nested, ok := value[key]; ok {
				if records := collectRecords(nested); len(records) > 0 {
					return records
				}
			}
		}
		// A WebSocket event may itself be one record.
		if firstString(value, "phone", "number", "recipient", "num", "msisdn") != "" {
			return []any{value}
		}
	}
	return nil
}

func normalizeRecord(value any) (string, string, string, *time.Time, string) {
	switch record := value.(type) {
	case map[string]any:
		phone := firstString(record, "phone", "number", "recipient", "num", "msisdn")
		service := firstString(record, "service", "cli", "sender", "originator", "app")
		message := firstString(record, "message", "text", "body", "content", "sms")
		timestampText := firstString(record, "datetime", "dt", "date", "timestamp", "time", "received_at")
		cursor := firstString(record, "id", "cursor", "message_id")
		return phone, service, message, parseTime(timestampText), cursor
	case []any:
		parts := make([]string, len(record))
		for i := range record {
			parts[i] = scalarString(record[i])
		}
		// Common panel row layouts: [time, number, service, message] and
		// [time, _, number, service, message].
		if len(parts) >= 5 {
			return parts[2], parts[3], parts[4], parseTime(parts[0]), parts[0] + "|" + parts[2]
		}
		if len(parts) >= 4 {
			return parts[1], parts[2], parts[3], parseTime(parts[0]), parts[0] + "|" + parts[1]
		}
	}
	return "", "", "", nil, ""
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text := scalarString(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func scalarString(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	default:
		return ""
	}
}

func parseTime(value string) *time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "02-01-2006 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return &parsed
		}
	}
	return nil
}

func setHeaders(req *http.Request, config map[string]any) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CrackSMS-vNext/1.0")
	if headers, ok := config["headers"].(map[string]any); ok {
		for key, value := range headers {
			req.Header.Set(key, scalarString(value))
		}
	}
}

func stringConfig(config map[string]any, key string) string { return scalarString(config[key]) }
func defaultString(config map[string]any, key, fallback string) string {
	if value := stringConfig(config, key); value != "" {
		return value
	}
	return fallback
}
func intConfig(config map[string]any, key string, fallback int) int {
	value, err := strconv.Atoi(stringConfig(config, key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
func absoluteURL(base, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

var hiddenInputPattern = regexp.MustCompile(`(?i)<input[^>]+name=["']([^"']+)["'][^>]*value=["']([^"']*)["'][^>]*>`)

func hiddenInputs(body []byte) url.Values {
	values := url.Values{}
	for _, match := range hiddenInputPattern.FindAllSubmatch(body, -1) {
		values.Set(string(match[1]), string(match[2]))
	}
	return values
}

var ErrProviderAccessRequired = errors.New("IVAS portal requires session renewal. Paste a current authenticated Socket.IO URL in Edit Credentials")

func SafeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrProviderAccessRequired) {
		return err.Error()
	}
	var provider *ProviderError
	if errors.As(err, &provider) {
		switch provider.Status {
		case 401:
			return "Authentication failed. Rotate the token or renew the portal session."
		case 403:
			return "Account or token scope is not allowed by the provider."
		case 429:
			return "Provider request budget reached. Retry at the scheduled time."
		case 400:
			code := provider.Code
			if len(code) > 80 || !regexp.MustCompile(`^[A-Z0-9_]+$`).MatchString(code) {
				code = "INVALID_PARAMETERS"
			}
			return "Provider rejected the parameters (" + code + ")."
		}
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "login form") {
		return "Login form was not found or authentication returned the login page. Check the panel URL and credentials."
	}
	if strings.Contains(text, "credential fields") {
		return "Login fields could not be detected. Configure username/password field overrides."
	}
	if strings.Contains(text, "recognized sms list") || strings.Contains(text, "recognizable records") {
		return "SMS endpoint returned an unexpected response. Check the discovered endpoint or provider format."
	}
	if strings.Contains(text, "redirect rejected") || strings.Contains(text, "downgraded login") {
		return "Provider redirected to a different host or downgraded transport. Check the configured panel URL."
	}
	if strings.Contains(text, "403") || strings.Contains(text, "cloudflare") || strings.Contains(text, "challenge") {
		return "Provider challenge or access restriction. Check supported access / allowlisting."
	}
	if strings.Contains(text, "401") || strings.Contains(text, "expired") || strings.Contains(text, "credentials") {
		return "Authentication failed or session expired. Update credentials."
	}
	if strings.Contains(text, "429") {
		return "Provider rate limit. Retry later."
	}
	return "Connection failed. Check the endpoint, credentials, and provider availability."
}

func providerChallenge(body []byte) bool {
	v := strings.ToLower(string(body))
	return strings.Contains(v, "cf-chl-") || strings.Contains(v, "challenge-platform") || strings.Contains(v, "just a moment...") || strings.Contains(v, "verify you are human")
}

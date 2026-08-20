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
	switch panel.Kind {
	case "token_api", "legacy_api":
		return &httpAdapter{panel: panel, client: client}, nil
	case "login":
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
	_, _, err := a.Poll(ctx, "")
	return err
}

func (a *httpAdapter) Poll(ctx context.Context, cursor string) ([]domain.OTPEvent, string, error) {
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
	query.Set("records", strconv.Itoa(intConfig(a.panel.Config, "records", 200)))
	if cursor != "" {
		query.Set(defaultString(a.panel.Config, "cursor_param", "cursor"), cursor)
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
		return nil, cursor, fmt.Errorf("panel returned HTTP %d", resp.StatusCode)
	}
	return decodePanelResponse(resp.Body, a.panel, cursor)
}

func (a *httpAdapter) Close() error {
	a.client.CloseIdleConnections()
	return nil
}

type loginAdapter struct {
	panel    domain.Panel
	client   *http.Client
	mu       sync.Mutex
	loggedIn bool
}

func (a *loginAdapter) Test(ctx context.Context) error {
	if err := a.login(ctx); err != nil {
		return err
	}
	_, _, err := a.Poll(ctx, "")
	return err
}

func (a *loginAdapter) login(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	baseURL := strings.TrimRight(stringConfig(a.panel.Config, "base_url"), "/")
	loginURL := absoluteURL(baseURL, defaultString(a.panel.Config, "login_path", "/login"))
	if baseURL == "" {
		return errors.New("base_url is missing")
	}
	// Load the form first so cookie/CSRF-based panels can establish a session.
	var hidden = url.Values{}
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil); err == nil {
		if resp, getErr := a.client.Do(req); getErr == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			resp.Body.Close()
			hidden = hiddenInputs(body)
		}
	}
	form := hidden
	form.Set(defaultString(a.panel.Config, "username_field", "username"), stringConfig(a.panel.Config, "username"))
	form.Set(defaultString(a.panel.Config, "password_field", "password"), stringConfig(a.panel.Config, "password"))
	action := absoluteURL(baseURL, defaultString(a.panel.Config, "signin_path", "/signin"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, action, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setHeaders(req, a.panel.Config)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	lower := strings.ToLower(string(body))
	if resp.StatusCode < 200 || resp.StatusCode >= 400 || strings.Contains(lower, "invalid password") || strings.Contains(lower, "invalid credentials") {
		return fmt.Errorf("login failed with HTTP %d", resp.StatusCode)
	}
	a.loggedIn = true
	return nil
}

func (a *loginAdapter) Poll(ctx context.Context, cursor string) ([]domain.OTPEvent, string, error) {
	if !a.loggedIn {
		if err := a.login(ctx); err != nil {
			return nil, cursor, err
		}
	}
	baseURL := strings.TrimRight(stringConfig(a.panel.Config, "base_url"), "/")
	endpoint := absoluteURL(baseURL, defaultString(a.panel.Config, "sms_path", "/res/data_smscdr.php"))
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, cursor, err
	}
	query := u.Query()
	now := time.Now()
	query.Set("fdate1", now.Add(-24*time.Hour).Format("2006-01-02 15:04:05"))
	query.Set("fdate2", now.Format("2006-01-02 15:04:05"))
	query.Set("iDisplayStart", "0")
	query.Set("iDisplayLength", strconv.Itoa(intConfig(a.panel.Config, "records", 200)))
	if sessionKey := stringConfig(a.panel.Config, "sesskey"); sessionKey != "" {
		query.Set("sesskey", sessionKey)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, cursor, err
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	setHeaders(req, a.panel.Config)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, cursor, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		a.loggedIn = false
		return nil, cursor, fmt.Errorf("session expired: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, cursor, fmt.Errorf("SMS endpoint returned HTTP %d", resp.StatusCode)
	}
	return decodePanelResponse(resp.Body, a.panel, cursor)
}

func (a *loginAdapter) Close() error {
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
		events = append(events, domain.OTPEvent{
			BotInstanceID: panel.BotInstanceID, PanelID: panel.ID, PanelName: panel.Name, Phone: phone, NormalizedPhone: normalized,
			Service: service, Message: message, ProviderTimestamp: timestamp, ReceivedAt: time.Now(),
			DedupKey: store.DedupKey(fmt.Sprintf("%d:%s", panel.ID, panel.Name), normalized, dedupPayload),
		})
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
		if firstString(value, "phone", "number", "recipient", "num") != "" {
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
		timestampText := firstString(record, "datetime", "date", "timestamp", "time", "received_at")
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

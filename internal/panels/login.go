package panels

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	htmlnode "golang.org/x/net/html"
)

// Session details are private to one adapter/account and are rediscovered on renewal.
type loginAdapter struct {
	panel                        domain.Panel
	client                       *http.Client
	mu                           sync.Mutex
	loggedIn                     bool
	smsURL, statsURL, sessionKey string
}

const loginUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

func (a *loginAdapter) Test(ctx context.Context) error { _, _, err := a.poll(ctx, "", 1); return err }
func (a *loginAdapter) Poll(ctx context.Context, cursor string) ([]domain.OTPEvent, string, error) {
	return a.poll(ctx, cursor, min(200, intConfig(a.panel.Config, "records", 200)))
}
func (a *loginAdapter) Close() error { a.client.CloseIdleConnections(); return nil }

func loginPageURL(config map[string]any) (string, error) {
	base := strings.TrimRight(stringConfig(config, "base_url"), "/")
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", errors.New("login base URL is invalid")
	}
	if configured := stringConfig(config, "login_path"); configured != "" {
		return absoluteURL(base, configured), nil
	}
	if strings.EqualFold(path.Base(u.Path), "login") || strings.EqualFold(path.Base(u.Path), "signin") {
		return base, nil
	}
	return base + "/login", nil
}
func (a *loginAdapter) page(ctx context.Context, link string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", loginUserAgent)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > 2<<20 {
		return nil, "", errors.New("login page exceeds size limit")
	}
	if providerChallenge(body) {
		return nil, "", errors.New("provider challenge requires manual access")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", legacyHTTPError(resp)
	}
	return body, resp.Request.URL.String(), nil
}
func attr(n *htmlnode.Node, key string) string {
	for _, v := range n.Attr {
		if strings.EqualFold(v.Key, key) {
			return v.Val
		}
	}
	return ""
}
func walkHTML(n *htmlnode.Node, visit func(*htmlnode.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTML(c, visit)
	}
}

var arithmeticQuestion = regexp.MustCompile(`\b(\d{1,5})\s*([+*\-])\s*(\d{1,5})\b`)

func arithmeticAnswer(root *htmlnode.Node) (string, bool) {
	var visible strings.Builder
	var collect func(*htmlnode.Node)
	collect = func(n *htmlnode.Node) {
		if n.Type == htmlnode.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == htmlnode.TextNode {
			visible.WriteString(n.Data)
			visible.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(root)
	matches := arithmeticQuestion.FindAllStringSubmatch(visible.String(), -1)
	if len(matches) != 1 {
		return "", false
	}
	left, _ := strconv.ParseInt(matches[0][1], 10, 64)
	right, _ := strconv.ParseInt(matches[0][3], 10, 64)
	switch matches[0][2] {
	case "+":
		return strconv.FormatInt(left+right, 10), true
	case "-":
		return strconv.FormatInt(left-right, 10), true
	case "*":
		return strconv.FormatInt(left*right, 10), true
	}
	return "", false
}

func hasPasswordForm(body []byte) bool {
	root, err := htmlnode.Parse(bytes.NewReader(body))
	if err != nil {
		return false
	}
	found := false
	walkHTML(root, func(n *htmlnode.Node) {
		if n.Type != htmlnode.ElementNode || n.Data != "form" {
			return
		}
		walkHTML(n, func(child *htmlnode.Node) {
			if child.Type == htmlnode.ElementNode && child.Data == "input" && strings.EqualFold(attr(child, "type"), "password") {
				found = true
			}
		})
	})
	return found
}

func parseLoginForm(body []byte, config map[string]any) (string, url.Values, error) {
	root, err := htmlnode.Parse(bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	var form *htmlnode.Node
	walkHTML(root, func(n *htmlnode.Node) {
		if n.Type == htmlnode.ElementNode && n.Data == "form" && form == nil {
			hasPassword := false
			walkHTML(n, func(i *htmlnode.Node) {
				if i.Data == "input" && (strings.EqualFold(attr(i, "type"), "password") || attr(i, "name") == stringConfig(config, "password_field")) {
					hasPassword = true
				}
			})
			if hasPassword {
				form = n
			}
		}
	})
	if form == nil {
		return "", nil, errors.New("login form with password field was not found")
	}
	if method := attr(form, "method"); method != "" && !strings.EqualFold(method, "post") {
		return "", nil, errors.New("unsupported login form method")
	}
	values := url.Values{}
	username, password := stringConfig(config, "username_field"), stringConfig(config, "password_field")
	var arithmeticField string
	walkHTML(form, func(n *htmlnode.Node) {
		if n.Type != htmlnode.ElementNode || n.Data != "input" {
			return
		}
		name, kind := attr(n, "name"), strings.ToLower(attr(n, "type"))
		if name == "" {
			return
		}
		if kind == "hidden" {
			values.Add(name, attr(n, "value"))
			return
		}
		hint := strings.ToLower(name + " " + attr(n, "placeholder") + " " + attr(n, "autocomplete"))
		if strings.Contains(hint, "capt") || strings.Contains(hint, "answer") || strings.Contains(hint, "result") || strings.Contains(hint, "calc") || strings.Contains(hint, "sum") {
			arithmeticField = name
			return
		}
		if password == "" && (kind == "password" || strings.Contains(hint, "password") || strings.Contains(hint, "pwd")) {
			password = name
		}
		if username == "" && (kind == "email" || strings.Contains(hint, "user") || strings.Contains(hint, "email") || strings.Contains(hint, "login") || strings.Contains(hint, "uname")) {
			username = name
		}
	})
	if arithmeticField != "" {
		answer, ok := arithmeticAnswer(form)
		if !ok {
			answer, ok = arithmeticAnswer(root)
		}
		if !ok {
			return "", nil, errors.New("provider sign-in challenge requires manual access")
		}
		values.Set(arithmeticField, answer)
	}
	if username == "" {
		walkHTML(form, func(n *htmlnode.Node) {
			if username == "" && n.Data == "input" && attr(n, "name") != "" && (attr(n, "type") == "" || attr(n, "type") == "text") {
				username = attr(n, "name")
			}
		})
	}
	if username == "" || password == "" {
		return "", nil, errors.New("credential fields could not be detected; configure field overrides")
	}
	values.Set(username, stringConfig(config, "username"))
	values.Set(password, stringConfig(config, "password"))
	return strings.TrimSpace(attr(form, "action")), values, nil
}
func sameLoginOrigin(base, target string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	u := b.ResolveReference(r)
	if u.User != nil || !strings.EqualFold(b.Hostname(), u.Hostname()) || (u.Scheme != "https" && u.Scheme != "http") || (b.Scheme == "https" && u.Scheme != "https") {
		return "", errors.New("cross-host or downgraded login endpoint rejected")
	}
	return u.String(), nil
}

var ajaxSource = regexp.MustCompile(`(?i)(?:sAjaxSource|["']?url["']?\s*|["']?ajax["']?)\s*["']?\s*:\s*["']([^"']+)["']`)

func discoverAJAX(body []byte, statsURL string) (string, string) {
	for _, m := range ajaxSource.FindAllSubmatch(body, -1) {
		link, err := sameLoginOrigin(statsURL, string(m[1]))
		if err != nil {
			continue
		}
		u, _ := url.Parse(link)
		if strings.Contains(strings.ToLower(u.Path), "sms") || strings.Contains(strings.ToLower(u.Path), "cdr") {
			return link, u.Query().Get("sesskey")
		}
	}
	return "", ""
}
func (a *loginAdapter) login(ctx context.Context) error {
	a.loggedIn = false
	a.smsURL = ""
	a.statsURL = ""
	a.sessionKey = ""
	loginURL, err := loginPageURL(a.panel.Config)
	if err != nil {
		return err
	}
	body, pageURL, err := a.page(ctx, loginURL)
	if err != nil {
		return err
	}
	action, values, err := parseLoginForm(body, a.panel.Config)
	if err != nil {
		return err
	}
	if override := stringConfig(a.panel.Config, "signin_path"); override != "" {
		action = absoluteURL(strings.TrimRight(stringConfig(a.panel.Config, "base_url"), "/"), override)
	}
	action, err = sameLoginOrigin(pageURL, action)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, action, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Referer", pageURL)
	u, _ := url.Parse(pageURL)
	req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	req.Header.Set("User-Agent", loginUserAgent)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if providerChallenge(body) {
		return errors.New("provider challenge requires manual access")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return legacyHTTPError(resp)
	}
	lower := strings.ToLower(string(body))
	authenticated := false
	for _, signal := range []string{"logout", "log out", "sign out", "smsdashboard", "smscdr", "dashboard", "my account"} {
		if strings.Contains(lower, signal) {
			authenticated = true
			break
		}
	}
	final := resp.Request.URL.String()
	finalURL, _ := url.Parse(final)
	finalPath := strings.ToLower(finalURL.Path)
	redirectedToAccount := strings.Contains(finalPath, "dashboard") || strings.Contains(finalPath, "smscdr")
	if !redirectedToAccount && hasPasswordForm(body) {
		return errors.New("authentication failed; login form was returned")
	}
	if !authenticated && !redirectedToAccount {
		return errors.New("authentication could not be verified")
	}
	if configured := stringConfig(a.panel.Config, "sms_path"); configured != "" {
		a.smsURL, err = sameLoginOrigin(final, absoluteURL(stringConfig(a.panel.Config, "base_url"), configured))
		if err != nil {
			return err
		}
		a.statsURL = final
		a.sessionKey = stringConfig(a.panel.Config, "sesskey")
		a.loggedIn = true
		return nil
	}
	if endpoint, key := discoverAJAX(body, final); endpoint != "" {
		a.smsURL, a.statsURL, a.sessionKey = endpoint, final, key
		a.loggedIn = true
		return nil
	}
	parsed, _ := url.Parse(final)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = path.Dir(parsed.Path) + "/"
	redirectBase := strings.TrimRight(parsed.String(), "/")
	base := strings.TrimRight(stringConfig(a.panel.Config, "base_url"), "/")
	var candidates []string
	root, _ := htmlnode.Parse(bytes.NewReader(body))
	walkHTML(root, func(n *htmlnode.Node) {
		if n.Data == "a" {
			href := attr(n, "href")
			if strings.Contains(strings.ToLower(href), "smscdrstats") {
				if link, e := sameLoginOrigin(final, href); e == nil {
					candidates = append(candidates, link)
				}
			}
		}
	})
	for _, b := range []string{redirectBase, base} {
		for _, p := range []string{"/SMSCDRStats", "/client/SMSCDRStats", "/smscdrstats"} {
			candidates = append(candidates, b+p)
		}
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		stats, actual, e := a.page(ctx, candidate)
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if endpoint, key := discoverAJAX(stats, actual); endpoint != "" {
			a.smsURL, a.statsURL, a.sessionKey = endpoint, actual, key
			break
		}
	}
	if a.smsURL == "" {
		a.smsURL = redirectBase + "/res/data_smscdr.php"
		a.statsURL = redirectBase + "/SMSCDRStats"
	}
	a.loggedIn = true
	return nil
}
func (a *loginAdapter) poll(ctx context.Context, cursor string, records int) ([]domain.OTPEvent, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if !a.loggedIn {
			if err := a.login(ctx); err != nil {
				return nil, cursor, err
			}
		}
		u, err := url.Parse(a.smsURL)
		if err != nil {
			return nil, cursor, err
		}
		q := u.Query()
		now := time.Now()
		q.Set("fdate1", now.Add(-24*time.Hour).Format("2006-01-02 15:04:05"))
		q.Set("fdate2", now.Format("2006-01-02 15:04:05"))
		q.Set("sEcho", "1")
		q.Set("iDisplayStart", "0")
		q.Set("iDisplayLength", strconv.Itoa(records))
		q.Set("iSortCol_0", "0")
		q.Set("sSortDir_0", "desc")
		if a.sessionKey != "" {
			q.Set("sesskey", a.sessionKey)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, cursor, err
		}
		setHeaders(req, a.panel.Config)
		req.Header.Set("User-Agent", loginUserAgent)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Referer", a.statsURL)
		resp, err := a.client.Do(req)
		if err != nil {
			return nil, cursor, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (10<<20)+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, cursor, readErr
		}
		if len(body) > 10<<20 {
			return nil, cursor, errors.New("SMS response exceeds size limit")
		}
		if providerChallenge(body) {
			a.loggedIn = false
			return nil, cursor, errors.New("provider challenge requires manual access")
		}
		_, _, formErr := parseLoginForm(body, a.panel.Config)
		if resp.StatusCode == 401 || formErr == nil {
			a.loggedIn = false
			if attempt == 0 {
				continue
			}
			return nil, cursor, errors.New("session expired; authentication failed")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, cursor, legacyHTTPError(resp)
		}
		return decodePanelResponse(bytes.NewReader(body), a.panel, cursor)
	}
	return nil, cursor, errors.New("authentication failed")
}
func legacyHTTPError(resp *http.Response) error {
	e := &ProviderError{Status: resp.StatusCode}
	if resp.StatusCode == 429 {
		seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if seconds < 1 {
			seconds = 60
		}
		e.RetryAt = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	return e
}
func recordEnvelope(value any) bool {
	switch v := value.(type) {
	case []any:
		return true
	case map[string]any:
		for _, key := range []string{"data", "records", "sms", "messages", "aaData", "result"} {
			if nested, ok := v[key]; ok {
				if _, list := nested.([]any); list {
					return true
				}
				if child, ok := nested.(map[string]any); ok && recordEnvelope(child) {
					return true
				}
			}
		}
	}
	return false
}

func loginRedirectPolicy(req *http.Request, via []*http.Request) error {
	if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || len(via) > 5 || (len(via) > 0 && (!strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) || (via[0].URL.Scheme == "https" && req.URL.Scheme != "https"))) {
		return errors.New("login redirect rejected")
	}
	return nil
}

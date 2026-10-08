package panels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

func TestGenericRESTPathsAndProviderScopedBudgets(t *testing.T) {
	for _, tc := range []struct{ kind, base, operation, want string }{
		{"axon_asp", "https://asp.example:8443", "sms", "https://asp.example:8443/api/sms"},
		{"axon_asp", "https://asp.example:8443/custom/inbox", "sms", "https://asp.example:8443/custom/inbox"},
		{"augestel", "https://iprn.example:9443", "numbers", "https://iprn.example:9443/api/v1/iprn/numbers"},
		{"augestel", "https://iprn.example/custom/v2", "statistics", "https://iprn.example/custom/v2/statistics"},
		{"augestel", "https://iprn.example/custom/v2/messages", "numbers", "https://iprn.example/custom/v2/numbers"},
	} {
		got, err := RESTEndpoint(tc.kind, tc.base, tc.operation)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, base := range []string{"http://public.example/api", "https://name:secret@example/api", "https://example/api?token=secret", "https://example/api#secret"} {
		if _, err := RESTEndpoint("axon_asp", base, "sms"); err == nil {
			t.Fatal("invalid endpoint accepted")
		}
	}
	adapter := func(base, token string) *restAdapter {
		return newRESTAdapter(domain.Panel{Kind: "augestel", Config: map[string]any{"api_base": base, "token": token}}, &http.Client{}, isolatedBudget())
	}
	a, same := adapter("https://API.example:443/custom", "key"), adapter("https://api.example/other", "key")
	other, rotated := adapter("https://other.example/custom", "key"), adapter("https://api.example/custom", "other-key")
	if a.key != same.key || a.ipKey != same.ipKey || a.key == other.key || a.ipKey == other.ipKey || a.key == rotated.key || a.ipKey != rotated.ipKey {
		t.Fatal("provider budgets were not scoped to normalized origin and credential")
	}
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing bearer header")
		}
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"hasMore":false}}`))
	}))
	defer server.Close()
	a = newRESTAdapter(domain.Panel{Kind: "axon_asp", Config: map[string]any{"api_base": server.URL + "/custom/inbox", "token": "fixture"}}, server.Client(), isolatedBudget())
	if err := a.Test(context.Background()); err != nil || path != "/custom/inbox" {
		t.Fatal("custom endpoint not used on the wire", path, err)
	}
}

func TestHTTPLoginSourceAndRedirectPolicy(t *testing.T) {
	if !store.ValidPanelSource("login", "http://login.example:8080/panel") || store.ValidPanelSource("axon_asp", "http://api.example") || store.ValidPanelSource("login", "http://name:secret@example") {
		t.Fatal("incorrect HTTP login source validation")
	}
	adapter, err := NewAdapter(domain.Panel{Kind: "login"})
	if err != nil {
		t.Fatal(err)
	}
	a := adapter.(*loginAdapter)
	defer a.Close()
	for _, tc := range []struct {
		from, to string
		allowed  bool
	}{
		{"http://login.example:8080", "http://login.example:8080/home", true},
		{"http://login.example", "https://login.example/home", true},
		{"https://login.example", "http://login.example/home", false},
		{"http://login.example", "https://other.example/home", false},
		{"http://login.example", "ftp://login.example/home", false},
	} {
		from, _ := http.NewRequest(http.MethodPost, tc.from, strings.NewReader("fixture"))
		to, _ := http.NewRequest(http.MethodGet, tc.to, nil)
		if got := a.client.CheckRedirect(to, []*http.Request{from}) == nil; got != tc.allowed {
			t.Fatal(tc, got)
		}
	}
}

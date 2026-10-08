package panels

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
)

func TestDecodePanelResponseDedupUsesProviderRecordID(t *testing.T) {
	panel := domain.Panel{ID: 7, Name: "provider"}
	payload := `{"data":[
		{"id":"message-1","number":"+923001234567","service":"WhatsApp","message":"Your code is 123456"},
		{"id":"message-2","number":"+923001234567","service":"WhatsApp","message":"Your code is 123456"}
	]}`
	events, cursor, err := decodePanelResponse(strings.NewReader(payload), panel, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%d, want 2", len(events))
	}
	if events[0].DedupKey == events[1].DedupKey {
		t.Fatal("different provider record IDs produced the same dedup key")
	}
	if cursor != "message-2" {
		t.Fatalf("cursor=%q, want message-2", cursor)
	}
}

func TestProviderChallengeAndSafeErrors(t *testing.T) {
	for _, body := range []string{`<script src="/cdn-cgi/challenge-platform/test"></script>`, `Just a moment...`, `<div>Verify you are human</div>`} {
		if !providerChallenge([]byte(body)) {
			t.Fatal("challenge not recognized")
		}
	}
	if providerChallenge([]byte(`{"data":[]}`)) {
		t.Fatal("normal response treated as challenge")
	}
	secret := fmt.Errorf("Get https://example.test/sms?token=fixture-secret: connection refused")
	if strings.Contains(SafeError(secret), "fixture-secret") {
		t.Fatal("provider error exposed a credential")
	}
	if _, e := NewAdapter(domain.Panel{Kind: "ivas"}); !errors.Is(e, ErrProviderAccessRequired) {
		t.Fatal("IVAS must require supported access")
	}
}

func TestProviderErrorPayloadDoesNotPassConnectionTest(t *testing.T) {
	for _, payload := range []string{`{"success":false,"message":"bad credentials"}`, `{"status":"error","data":[]}`, `{"error":"invalid token fixture-secret"}`} {
		_, _, e := decodePanelResponse(strings.NewReader(payload), domain.Panel{}, "")
		if e == nil {
			t.Fatal("provider rejection accepted", payload)
		}
		if strings.Contains(e.Error(), "fixture-secret") {
			t.Fatal("provider response secret exposed")
		}
	}
	if _, _, e := decodePanelResponse(strings.NewReader(`{"success":true,"data":[]}`), domain.Panel{}, ""); e != nil {
		t.Fatal("empty successful response rejected", e)
	}
}

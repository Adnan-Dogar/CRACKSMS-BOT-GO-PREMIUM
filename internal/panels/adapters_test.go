package panels

import (
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

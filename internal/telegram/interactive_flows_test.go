package telegram

import "testing"

func TestWithdrawalAccountValidation(t *testing.T) {
	details, hint, err := validateWithdrawalDetails("jazzcash", "03001234567 | Adnan")
	if err != nil || details != "03001234567 | Adnan" || hint != "0300•••567" {
		t.Fatalf("jazzcash details=%q hint=%q err=%v", details, hint, err)
	}
	if _, _, err := validateWithdrawalDetails("usdt_bep20", "0x1234"); err == nil {
		t.Fatal("short BEP20 address was accepted")
	}
	address := "0x1234567890abcdef1234567890abcdef12345678"
	if _, hint, err := validateWithdrawalDetails("usdt_bep20", address); err != nil || hint != "0x1234•••5678" {
		t.Fatalf("BEP20 hint=%q err=%v", hint, err)
	}
}

func TestPanelURLValidation(t *testing.T) {
	for _, item := range []struct {
		kind string
		url  string
		ok   bool
	}{
		{"login", "https://panel.example", true},
		{"token_api", "http://panel.example/api", true},
		{"websocket", "wss://panel.example/socket", true},
		{"websocket", "https://panel.example/socket", false},
		{"login", "javascript:alert(1)", false},
	} {
		if got := validPanelURL(item.kind, item.url); got != item.ok {
			t.Fatalf("validPanelURL(%q,%q)=%v want %v", item.kind, item.url, got, item.ok)
		}
	}
}

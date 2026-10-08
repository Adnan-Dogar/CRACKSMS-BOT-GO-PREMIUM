package delivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
)

func TestFormatMessageGroupContainsOTPAndMaskedPhone(t *testing.T) {
	event := domain.OTPEvent{PanelName: "Panel", Service: "WhatsApp", NormalizedPhone: "923001234567", Code: "123456", Message: "Code 123456"}
	message := FormatMessage(event, false)
	if !strings.Contains(message, "123456") {
		t.Fatal("group message must contain OTP in its body")
	}
	if strings.Contains(message, "+923001234567") {
		t.Fatal("group message exposed the full phone")
	}
}

func TestFormatMessageUserContainsFullPhone(t *testing.T) {
	event := domain.OTPEvent{PanelName: "Panel", NormalizedPhone: "923001234567", Code: "123456", Message: "Code 123456"}
	if message := FormatMessage(event, true); !strings.Contains(message, "+923001234567") {
		t.Fatal("assigned user message must contain the full phone")
	}
}

func TestOTPKeyboardCarriesPremiumBotAPIFields(t *testing.T) {
	raw, err := json.Marshal(OTPKeyboard("123456", "https://t.me/channel", "https://t.me/number_bot"))
	if err != nil {
		t.Fatal(err)
	}
	markup := string(raw)
	for _, field := range []string{`"copy_text":{"text":"123456"}`, `"style":"success"`, `"style":"primary"`, `"icon_custom_emoji_id"`} {
		if !strings.Contains(markup, field) {
			t.Fatalf("premium markup is missing %s: %s", field, markup)
		}
	}
}

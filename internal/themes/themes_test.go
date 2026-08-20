package themes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
)

func TestAllTenThemesRenderPremiumMarkup(t *testing.T) {
	event := domain.OTPEvent{PanelName: "Panel", NormalizedPhone: "923001234567", Service: "WhatsApp", Code: "123456", Message: "Your code is 123456"}
	items := Catalog()
	if len(items) != 10 {
		t.Fatalf("theme count=%d, want 10", len(items))
	}
	for id := 0; id < 10; id++ {
		body := Format(event, id, true, "visible")
		if !strings.Contains(body, "123456") {
			t.Fatalf("theme %d did not render the OTP", id)
		}
		raw, err := json.Marshal(Keyboard(event, id, Links{
			Channel: "https://t.me/community", NumberBot: "https://t.me/numbers",
			Developer: "https://t.me/developer", Support: "https://t.me/support",
		}, true))
		if err != nil {
			t.Fatal(err)
		}
		markup := string(raw)
		if !strings.Contains(markup, `"style":`) || !strings.Contains(markup, `"icon_custom_emoji_id":`) {
			t.Fatalf("theme %d is missing premium button fields: %s", id, markup)
		}
	}
}

func TestHiddenGroupDoesNotLeakOTP(t *testing.T) {
	event := domain.OTPEvent{PanelName: "Panel", NormalizedPhone: "923001234567", Service: "WhatsApp", Code: "123456", Message: "Your code is 123456"}
	body := Format(event, 9, false, "hidden")
	if strings.Contains(body, "123456") || !strings.Contains(body, "[HIDDEN]") {
		t.Fatalf("hidden group leaked or omitted marker: %s", body)
	}
	raw, err := json.Marshal(Keyboard(event, 9, Links{Channel: "https://t.me/community"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "123456") || strings.Contains(string(raw), "copy_text") {
		t.Fatalf("hidden group keyboard leaked OTP: %s", raw)
	}
}

package themes

import (
	"strings"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
)

func sampleOTP() domain.OTPEvent {
	return domain.OTPEvent{PanelName: "Private Panel Name", NormalizedPhone: "923001234567", Service: "WhatsApp", Code: "123456", Message: "Your code is 123456"}
}

func TestAllThemesRemovePanelName(t *testing.T) {
	event := sampleOTP()
	if len(Catalog()) != 10 {
		t.Fatalf("theme count=%d", len(Catalog()))
	}
	for id := 0; id < 10; id++ {
		body := Format(event, id, true, "visible")
		if strings.Contains(body, event.PanelName) || strings.Contains(strings.ToLower(body), "panel:") || strings.Contains(body, "panel=") {
			t.Fatalf("theme %d exposed panel name: %s", id, body)
		}
		for _, customID := range []string{"5334998226636390258", "5224637061985742245"} {
			if !strings.Contains(body, `emoji-id="`+customID+`"`) {
				t.Fatalf("theme %d missing custom emoji %s: %s", id, customID, body)
			}
		}
		if id != 1 && !strings.Contains(body, event.Code) {
			t.Fatalf("theme %d lost the visible OTP: %s", id, body)
		}
	}
	if body := Format(event, 0, true, "visible"); strings.Contains(body, "━━━━━━━━") || strings.Contains(body, "❄️") {
		t.Fatalf("Classic retained the snow frame: %s", body)
	}
}

func TestMinimalBodyAndKeyboard(t *testing.T) {
	event := sampleOTP()
	body := Format(event, 1, true, "visible")
	for _, wanted := range []string{"+923<b>•••••</b>4567", `emoji-id="5334998226636390258"`, `emoji-id="5224637061985742245"`} {
		if !strings.Contains(body, wanted) {
			t.Fatalf("Minimal body missing %q: %s", wanted, body)
		}
	}
	for _, forbidden := range []string{event.Code, event.NormalizedPhone, event.Message, event.PanelName, "Pakistan", "OTP:"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Minimal body exposed %q: %s", forbidden, body)
		}
	}
	if strings.Contains(body, "\n") {
		t.Fatalf("Minimal body must be one line: %s", body)
	}
	links := Links{Group: "https://t.me/otp_group", Channel: "https://t.me/channel", NumberBot: "https://t.me/numbers"}
	keyboard := Keyboard(event, 1, links, true, true)
	if len(keyboard.InlineKeyboard) != 3 || len(keyboard.InlineKeyboard[0]) != 1 || len(keyboard.InlineKeyboard[1]) != 2 || len(keyboard.InlineKeyboard[2]) != 1 {
		t.Fatalf("Minimal rows are wrong: %+v", keyboard.InlineKeyboard)
	}
	copyButton := keyboard.InlineKeyboard[0][0]
	if copyButton.CopyText == nil || copyButton.CopyText.Text != event.Code || copyButton.Text != "●●●●●●" || copyButton.IconCustomEmojiID != "6319056439096644016" {
		t.Fatalf("Minimal copy button is wrong: %+v", copyButton)
	}
	if keyboard.InlineKeyboard[1][0].URL != links.Group || keyboard.InlineKeyboard[1][1].URL != links.Channel || keyboard.InlineKeyboard[2][0].URL != links.NumberBot {
		t.Fatalf("Minimal link order is wrong: %+v", keyboard.InlineKeyboard)
	}
	if got := Keyboard(event, 1, Links{}, true, true); len(got.InlineKeyboard) != 1 {
		t.Fatalf("unset links left empty rows: %+v", got.InlineKeyboard)
	}
	if body := Format(event, 1, false, "hidden"); strings.Contains(body, event.Code) || strings.Contains(body, "OTP:") {
		t.Fatalf("Minimal body exposed OTP in hidden group: %s", body)
	}
	event.NormalizedPhone = "0"
	if body := Format(event, 1, true, "visible"); strings.Contains(body, "+0") || !strings.Contains(body, "+<b>••••</b>") {
		t.Fatalf("short number was not masked: %s", body)
	}
}

func TestHiddenGroupCopyKeepsCodeOffScreen(t *testing.T) {
	event := sampleOTP()
	body := Format(event, 9, false, "hidden")
	if strings.Contains(body, event.Code) || !strings.Contains(body, "[HIDDEN]") {
		t.Fatalf("hidden group body is wrong: %s", body)
	}
	keyboard := Keyboard(event, 9, Links{Channel: "https://t.me/community"}, false, false)
	if len(keyboard.InlineKeyboard) == 0 || keyboard.InlineKeyboard[0][0].CopyText == nil || keyboard.InlineKeyboard[0][0].CopyText.Text != event.Code || strings.Contains(keyboard.InlineKeyboard[0][0].Text, event.Code) {
		t.Fatalf("hidden group copy button is wrong: %+v", keyboard.InlineKeyboard)
	}
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if button.Text == "Full Message" {
				t.Fatal("hidden group exposed the full-message copy button")
			}
		}
	}
}

func TestEveryGroupThemeMasksNumberIncludingSMSMessage(t *testing.T) {
	event := sampleOTP()
	event.Phone = "+923001234567"
	event.Message = "Number +923001234567 or 923001234567 received code 123456"
	for id := 0; id < 10; id++ {
		body := Format(event, id, false, "visible")
		if strings.Contains(body, "+923001234567") || strings.Contains(body, "923001234567") {
			t.Fatalf("group theme %d exposed full number: %s", id, body)
		}
		if !strings.Contains(body, "••") {
			t.Fatalf("group theme %d did not show masked number: %s", id, body)
		}
	}
	group := Keyboard(event, 0, Links{}, true, false)
	if len(group.InlineKeyboard) < 2 || group.InlineKeyboard[1][0].CopyText == nil {
		t.Fatalf("group full-message copy button missing: %+v", group.InlineKeyboard)
	}
	copyMessage := group.InlineKeyboard[1][0].CopyText.Text
	if strings.Contains(copyMessage, "923001234567") || !strings.Contains(copyMessage, "+923•••••4567") {
		t.Fatalf("group full-message copy exposed number: %s", copyMessage)
	}
	private := Keyboard(event, 0, Links{}, true, true)
	if !strings.Contains(private.InlineKeyboard[1][0].CopyText.Text, event.Phone) {
		t.Fatal("private full-message copy lost the original phone")
	}
	event.Message = "Number +92 (300) 123-4567 received code 123456"
	if body := Format(event, 0, false, "visible"); strings.Contains(body, "+92 (300) 123-4567") || !strings.Contains(body, "+923•••••4567") {
		t.Fatalf("formatted number leaked in group SMS text: %s", body)
	}
	event.NormalizedPhone = "0"
	event.Phone = "+0"
	event.Message = "+0 code 123456"
	for id := 0; id < 10; id++ {
		body := Format(event, id, false, "visible")
		if strings.Contains(body, "+0") || !strings.Contains(body, "+••••") && !strings.Contains(body, "+<b>••••</b>") {
			t.Fatalf("group theme %d exposed a short number: %s", id, body)
		}
	}
}

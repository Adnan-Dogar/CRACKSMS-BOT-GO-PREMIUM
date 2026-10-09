package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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

type fakeTelegram struct {
	requests []string
	reject   bool
}

func (f *fakeTelegram) Do(request *http.Request) (*http.Response, error) {
	_ = request.ParseForm()
	f.requests = append(f.requests, request.PostForm.Get("text")+"\n"+request.PostForm.Get("reply_markup"))
	body := `{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}}`
	if f.reject && strings.Contains(request.PostForm.Get("text"), "tg-emoji") {
		body = `{"ok":false,"error_code":400,"description":"Bad Request: CUSTOM_EMOJI_INVALID"}`
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func TestRejectedCustomEmojiFallsBackToPlainOTP(t *testing.T) {
	transport := &fakeTelegram{reject: true}
	bot := &tgbotapi.BotAPI{Token: "test", Client: transport, Buffer: 1, Self: tgbotapi.User{UserName: "test_bot"}}
	bot.SetAPIEndpoint(tgbotapi.APIEndpoint)
	service := NewRouted(nil, staticBotProvider{bot: bot}, 1000, "", "", "", "", "", &Metrics{})
	job := domain.DeliveryJob{BotInstanceID: 1, TargetKind: "user", TargetID: 42, Event: domain.OTPEvent{
		Service: "WhatsApp", NormalizedPhone: "923001234567", Code: "123456", Message: "Code 123456"}}
	if err := service.sendContext(context.Background(), job); err != nil {
		t.Fatalf("fallback send failed: %v", err)
	}
	if len(transport.requests) != 2 {
		t.Fatalf("requests = %d, want rejected premium send plus one plain resend", len(transport.requests))
	}
	if retry := transport.requests[1]; strings.Contains(retry, "tg-emoji") || !strings.Contains(retry, "123456") {
		t.Fatalf("plain resend must keep the OTP without custom emoji: %s", retry)
	}
}

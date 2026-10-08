package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

func groupUpdate(t *testing.T, app *App, raw string) {
	t.Helper()
	updates, e := decodeUpdates(json.RawMessage("[" + raw + "]"))
	if e != nil || len(updates) != 1 {
		t.Fatal(e)
	}
	app.handleUpdate(context.WithValue(context.Background(), entityContextKey{}, updates[0]), updates[0].Update)
}
func groupCommand(t *testing.T, app *App, user int64, command string) {
	t.Helper()
	groupUpdate(t, app, fmt.Sprintf(`{"update_id":1,"message":{"message_id":0,"ephemeral_message_id":17,"message_thread_id":9,"date":%d,"from":{"id":%d,"first_name":"Test"},"chat":{"id":-123,"type":"supergroup"},"text":"/%s","entities":[{"type":"bot_command","offset":0,"length":%d}]}}`, time.Now().Unix(), user, command, len(command)+1))
}

func TestEphemeralMetadataAndActorScopedGroupScreens(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	if _, e := pool.Exec(ctx, `UPDATE users SET balance_pkr=CASE id WHEN 1 THEN 123 WHEN 2 THEN 456 ELSE 0 END`); e != nil {
		t.Fatal(e)
	}
	for _, user := range []int64{1, 2} {
		f.reset()
		groupCommand(t, app, user, "balance")
		screen := lastScreen(t, f)
		var params map[string]any
		if e := json.Unmarshal([]byte(screen.Params.Get("ephemeral_message_parameters")), &params); e != nil || int64(params["receiver_user_id"].(float64)) != user {
			t.Fatal("receiver omitted or shared", screen, e)
		}
		if screen.Params.Get("chat_id") != "-123" || screen.Params.Get("message_thread_id") != "9" || !strings.Contains(screen.Params.Get("reply_parameters"), `"ephemeral_message_id":17`) {
			t.Fatal("lost group/thread/reply metadata", screen)
		}
		for _, call := range f.snapshot() {
			if (call.Method == "sendMessage" || call.Method == "sendRichMessage") && call.Params.Get("chat_id") == "-123" && call.Params.Get("ephemeral_message_parameters") == "" {
				t.Fatal("personal screen became public", call)
			}
			if call.Method == "setMyCommands" && strings.Contains(call.Params.Get("scope"), "chat_member") && !strings.Contains(call.Params.Get("commands"), `"is_ephemeral":true`) {
				t.Fatal("group commands are public")
			}
		}
	}
	// Receiver metadata cannot be forged to inspect a different user's screen.
	f.reset()
	groupUpdate(t, app, `{"callback_query":{"id":"forged","from":{"id":2},"data":"menu:profile","message":{"ephemeral_message_id":88,"receiver_user":{"id":1},"chat":{"id":-123,"type":"supergroup"}}}}`)
	if len(f.snapshot()) != 0 {
		t.Fatal("receiver mismatch handled")
	}
}

func TestGroupNavigationKeepsPrivateCredentialFlow(t *testing.T) {
	app, f, _ := newIntegrationApp(t)
	ctx := context.Background()
	flow := store.TelegramFlow{Kind: "panel_account", Step: "password", Data: map[string]string{"fixture": "private"}}
	if e := app.store.SetTelegramFlow(ctx, 1, 1, flow); e != nil {
		t.Fatal(e)
	}
	groupCommand(t, app, 1, "start")
	saved, e := app.store.TelegramFlow(ctx, 1, 1)
	if e != nil || saved.Step != "password" {
		t.Fatal("group navigation cleared private setup", saved, e)
	}
	f.reset()
	groupUpdate(t, app, `{"callback_query":{"id":"admin-handoff","from":{"id":1},"data":"admin:panels","message":{"ephemeral_message_id":88,"receiver_user":{"id":1},"chat":{"id":-123,"type":"supergroup"}}}}`)
	screen := lastScreen(t, f)
	if screen.Method != "editEphemeralMessageText" || screen.Params.Get("receiver_user_id") != "1" || !strings.Contains(screen.Params.Get("reply_markup"), "start=open_admin") {
		t.Fatal("sensitive flow not handed off privately", screen)
	}
	saved, e = app.store.TelegramFlow(ctx, 1, 1)
	if e != nil || saved.Step != "password" {
		t.Fatal("handoff altered private setup", saved, e)
	}
}

func TestGroupRenderingFallbackPreservesPrivacyAndColors(t *testing.T) {
	for _, test := range []struct {
		name, message string
		status        int
		wantDM        bool
	}{{"emoji", "Bad Request: custom emoji invalid", 400, false}, {"unsupported", "Forbidden: ephemeral messages unavailable", 403, true}} {
		t.Run(test.name, func(t *testing.T) {
			bot, f := newFakeTelegram(t)
			app := New(bot, nil, time.Minute, time.UTC)
			app.group = &groupInteraction{Chat: -123, User: 7, Screen: 88, Callback: "cb"}
			f.reject = test.message
			f.rejectCode = test.status
			e := app.renderGroupDocument(-123, screenDocument{ClassicHTML: "Balance 👋", Keyboard: userBackMenu()})
			if !test.wantDM && e != nil {
				t.Fatal(e)
			}
			dm := false
			for _, c := range f.snapshot() {
				if c.Params.Get("chat_id") == "-123" && (c.Method == "sendMessage" || c.Method == "sendRichMessage" || c.Method == "editMessageText") {
					t.Fatal("private rendering fell back to public", c)
				}
				if c.Method == "editEphemeralMessageText" && (c.Params.Get("receiver_user_id") != "7" || c.Params.Get("ephemeral_message_id") != "88") {
					t.Fatal("edit lost actor", c)
				}
				if c.Params.Get("chat_id") == "7" && c.Method == "sendMessage" {
					dm = true
					if strings.Contains(c.Params.Get("text"), "Balance") {
						t.Fatal("sensitive fallback content")
					}
				}
			}
			if dm != test.wantDM {
				t.Fatal("missing private handoff", dm)
			}
			if !dm {
				screen := lastScreen(t, f)
				var k premium.InlineKeyboard
				_ = json.Unmarshal([]byte(screen.Params.Get("reply_markup")), &k)
				for _, row := range k.InlineKeyboard {
					for _, b := range row {
						if b.Style != "primary" {
							t.Fatal("fallback removed navigation color", b)
						}
					}
				}
			}
		})
	}
}

func TestUnsupportedOrdinaryGroupCommandDoesNotAllocateStock(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	if _, e := app.store.AddNumbers(context.Background(), "WhatsApp", "Pakistan", "PK", 1, 0, 1, []string{"923001234567"}); e != nil {
		t.Fatal(e)
	}
	groupUpdate(t, app, `{"message":{"message_id":55,"from":{"id":2},"chat":{"id":-123,"type":"supergroup"},"text":"/getnumber","entities":[{"type":"bot_command","offset":0,"length":10}]}}`)
	var count int
	if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM assignments`).Scan(&count); e != nil || count != 0 {
		t.Fatal("failed private response allocated inventory", count, e)
	}
	for _, c := range f.snapshot() {
		if c.Params.Get("chat_id") == "-123" && c.Method == "sendMessage" && c.Params.Get("ephemeral_message_parameters") == "" {
			t.Fatal("public handoff", c)
		}
	}
}

func TestGroupActivityHasManualRefreshAndNoPrivateLiveSession(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	app.group = &groupInteraction{Chat: -123, User: 2, Incoming: 17}
	app.startLive(ctx, -123, 2, "countries", "24h", 0, false)
	screen := lastScreen(t, f)
	markup := screen.Params.Get("reply_markup")
	if strings.Contains(markup, "live:resume") || strings.Contains(markup, "live:pause") || strings.Contains(markup, "live:refresh") || !strings.Contains(markup, "activity:countries:24h:0") {
		t.Fatal("group manual controls incorrect", markup)
	}
	var count int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM live_screen_sessions`).Scan(&count); e != nil || count != 0 {
		t.Fatal("group created persistent refresh session", count, e)
	}
	if len(app.ui.sessions) != 0 {
		t.Fatal("group overwrote a private live session")
	}
}

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/db"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/secure"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type telegramCall struct {
	Method string
	Params url.Values
}
type fakeTelegram struct {
	mu         sync.Mutex
	calls      []telegramCall
	reject     string
	rejectCode int
	member     string
	fileBody   []byte
}

func (f *fakeTelegram) snapshot() []telegramCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]telegramCall(nil), f.calls...)
}
func (f *fakeTelegram) reset() { f.mu.Lock(); defer f.mu.Unlock(); f.calls = nil }
func newFakeTelegram(t *testing.T) (*tgbotapi.BotAPI, *fakeTelegram) {
	t.Helper()
	f := &fakeTelegram{member: "member"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, telegramCall{method, r.Form})
		w.Header().Set("Content-Type", "application/json")
		if f.reject != "" && (method == "sendMessage" || method == "sendRichMessage" || method == "editMessageText" || method == "editEphemeralMessageText") {
			message := f.reject
			f.reject = ""
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": func() int {
				if f.rejectCode != 0 {
					return f.rejectCode
				}
				return 400
			}(), "description": message})
			return
		}
		result := any(true)
		switch method {
		case "getMe":
			result = map[string]any{"id": 999, "is_bot": true, "first_name": "Test", "username": "test_bot"}
		case "sendMessage", "sendRichMessage", "editMessageText", "editEphemeralMessageText", "sendDocument":
			result = map[string]any{"message_id": 42, "chat": map[string]any{"id": 1, "type": "private"}, "text": r.Form.Get("text")}
			if r.Form.Get("ephemeral_message_parameters") != "" {
				result = map[string]any{"ephemeral_message_id": 88, "chat": map[string]any{"id": -123, "type": "supergroup"}}
			}
		case "getChatMember":
			result = map[string]any{"status": f.member, "user": map[string]any{"id": 1, "first_name": "Test"}}
		case "getChat":
			result = map[string]any{"id": -123, "type": "supergroup", "title": "Test Group"}
		case "getFile":
			result = map[string]any{"file_id": "fixture", "file_path": "numbers.txt", "file_size": len(f.fileBody)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(server.Close)
	bot, err := tgbotapi.NewBotAPIWithClient("test", server.URL+"/bot%s/%s", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	f.reset()
	return bot, f
}

func TestScreenRendering(t *testing.T) {
	for _, test := range []struct {
		name, rejection string
		edit            bool
		want            []string
	}{
		{"send", "", false, []string{"sendMessage"}},
		{"edit", "", true, []string{"editMessageText"}},
		{"unchanged", "Bad Request: message is not modified", true, []string{"editMessageText"}},
		{"deleted", "Bad Request: message to edit not found", true, []string{"editMessageText", "sendMessage"}},
		{"emoji fallback", "Bad Request: custom emoji invalid", false, []string{"sendMessage", "sendMessage"}},
		{"no retry on timeout", "Gateway timeout", false, []string{"sendMessage"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bot, f := newFakeTelegram(t)
			f.reject = test.rejection
			app := New(bot, nil, time.Minute, time.UTC)
			if test.edit {
				app.screenChatID = 1
				app.screenMessageID = 42
			}
			err := app.renderScreen(1, "Hello 👋", compactMenu(false, true))
			if test.name == "no retry on timeout" {
				if err == nil {
					t.Fatal("expected error")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			calls := f.snapshot()
			if len(calls) != len(test.want) {
				t.Fatalf("calls=%v", calls)
			}
			for i, want := range test.want {
				if calls[i].Method != want {
					t.Fatalf("method=%s want=%s", calls[i].Method, want)
				}
			}
			if test.name == "emoji fallback" {
				last := calls[len(calls)-1].Params
				if strings.Contains(last.Get("text"), "tg-emoji") || strings.Contains(last.Get("reply_markup"), "icon_custom_emoji_id") {
					t.Fatal("fallback retained premium fields")
				}
			}
		})
	}
}

func TestUpdateShardAndStableInventory(t *testing.T) {
	user := &tgbotapi.User{ID: 128}
	if updateShard(tgbotapi.Update{Message: &tgbotapi.Message{From: user}}, 32) != updateShard(tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{From: user}}, 32) {
		t.Fatal("user changed queue")
	}
	before := servicesMenu(map[string][]store.CatalogCountry{"WhatsApp": nil}).InlineKeyboard[0][0].CallbackData
	after := servicesMenu(map[string][]store.CatalogCountry{"AAA": nil, "WhatsApp": nil}).InlineKeyboard[0][1].CallbackData
	if before != after {
		t.Fatal("catalog reorder changed service identity")
	}
	country := countriesMenu("WhatsApp", []store.CatalogCountry{{Country: "Pakistan"}}).InlineKeyboard[0][0].CallbackData
	if len(country) > 64 || country != assignmentMenu("WhatsApp", "Pakistan").InlineKeyboard[0][0].CallbackData {
		t.Fatal("invalid stable callback")
	}
}

func TestGuidedValidation(t *testing.T) {
	for _, tc := range []struct {
		kind, value string
		valid       bool
	}{
		{"id", "-1", false}, {"id", "123", true}, {"chat", "-100123", true},
		{"rewards", "30=30,100=50", true}, {"rewards", "30=NaN", false}, {"rewards", "30=1,30=2", false},
		{"regex", "([0-9]{6})", true}, {"regex", "[0-9]{6}", false}, {"regex", "(a|b)", true},
		{"secret", "bad token", false}, {"date", "none", true}, {"date", "2026-13-99", false},
		{"permissions", "manage_panels,view_analytics", true}, {"permissions", "administrator", false},
		{"target", "group:-100", true}, {"target", "user:no", false}, {"text", "x|y", false},
	} {
		if got := validateGuidedField(guidedField{Label: "test", Kind: tc.kind}, tc.value, time.UTC) == nil; got != tc.valid {
			t.Errorf("%s %q valid=%v", tc.kind, tc.value, got)
		}
	}
}

func newIntegrationApp(t *testing.T) (*App, *fakeTelegram, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("telegram_test_%d", time.Now().UnixNano())
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = root.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		root.Close()
	})
	if err = db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := store.New(pool, time.UTC, cipher)
	bot, f := newFakeTelegram(t)
	app := New(bot, repo, 20*time.Minute, time.UTC)
	for _, id := range []int64{1, 2, 3} {
		if err = repo.EnsureUserForInstance(ctx, 1, id, "test", "Test", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.AddInstanceAdmin(ctx, 1, 1, []string{"*"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.AddInstanceAdmin(ctx, 1, 3, []string{"manage_panels"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetUserTier(ctx, 1, 1, "enterprise", nil, 1); err != nil {
		t.Fatal(err)
	}
	return app, f, pool
}

func testCallback(app *App, user int64, data string) {
	app.handleUpdate(context.Background(), tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{ID: "test", Data: data, From: &tgbotapi.User{ID: user, FirstName: "Test"}, Message: &tgbotapi.Message{MessageID: 42, Chat: &tgbotapi.Chat{ID: user, Type: "private"}}}})
}
func testText(app *App, user int64, text string) {
	m := &tgbotapi.Message{MessageID: 43, Text: text, From: &tgbotapi.User{ID: user, FirstName: "Test"}, Chat: &tgbotapi.Chat{ID: user, Type: "private"}}
	if strings.HasPrefix(text, "/") {
		m.Entities = []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(strings.Split(text, " ")[0])}}
	}
	app.handleUpdate(context.Background(), tgbotapi.Update{Message: m})
}
func lastScreen(t *testing.T, f *fakeTelegram) telegramCall {
	t.Helper()
	calls := f.snapshot()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Method == "sendMessage" || calls[i].Method == "sendRichMessage" || calls[i].Method == "editMessageText" || calls[i].Method == "editEphemeralMessageText" {
			call := calls[i]
			if call.Params.Get("text") == "" && call.Params.Get("rich_message") != "" {
				var document inputRichMessage
				if json.Unmarshal([]byte(call.Params.Get("rich_message")), &document) == nil {
					values := url.Values{}
					for key, value := range call.Params {
						values[key] = append([]string(nil), value...)
					}
					content := strings.NewReplacer("</td><td>", ": ", "</tr>", "\n", "<br/>", "\n", "<h3>", "<b>", "</h3>", "</b>\n").Replace(document.HTML)
					values.Set("text", content)
					call.Params = values
				}
			}
			return call
		}
	}
	t.Fatal("no screen response")
	return telegramCall{}
}

func TestMenuCallbacksIntegration(t *testing.T) {
	app, f, _ := newIntegrationApp(t)
	menus := []premium.InlineKeyboard{compactMenu(true, true), fullMenu(true, true, themes.Links{}), adminDashboard(), settingsMenu(), profileMenu(), adminNumbersMenu(), adminSettingsMenu(), adminGroupsMenu(nil), adminPanelMenu(nil), adminRewardsMenu(nil), adminUsersMenu(nil), adminAdminsMenu(nil), adminPatternsMenu(nil), adminRequiredMenu(nil), adminTutorialsMenu(nil), integrationMenu("webhook", "unhook"), integrationMenu("schedule", "unschedule"), integrationMenu("apikey", "revoke")}
	seen := map[string]bool{}
	for _, menu := range menus {
		for _, row := range menu.InlineKeyboard {
			for _, button := range row {
				if button.CallbackData == "" || seen[button.CallbackData] {
					continue
				}
				seen[button.CallbackData] = true
				t.Run(button.CallbackData, func(t *testing.T) {
					f.reset()
					testCallback(app, 1, button.CallbackData)
					screen := lastScreen(t, f)
					if strings.Contains(screen.Params.Get("text"), "no longer available") || strings.Contains(screen.Params.Get("text"), "could not be completed") {
						t.Fatalf("unhandled route %s: %s", button.CallbackData, screen.Params.Get("text"))
					}
				})
			}
		}
	}
	t.Run("admin command", func(t *testing.T) {
		f.reset()
		testText(app, 1, "/admin")
		if !strings.Contains(lastScreen(t, f).Params.Get("text"), "ADMIN PANEL") {
			t.Fatal("admin command failed")
		}
	})
	t.Run("permission filtered dashboard", func(t *testing.T) {
		f.reset()
		testCallback(app, 3, "menu:admin")
		markup := lastScreen(t, f).Params.Get("reply_markup")
		if strings.Contains(markup, "admin:admins") || !strings.Contains(markup, "admin:panels") {
			t.Fatal(markup)
		}
	})
	t.Run("non admin denial", func(t *testing.T) {
		f.reset()
		testCallback(app, 2, "admin:groups")
		if !strings.Contains(lastScreen(t, f).Params.Get("text"), "permission") {
			t.Fatal("missing denial")
		}
	})
	t.Run("unknown callback", func(t *testing.T) {
		f.reset()
		testCallback(app, 1, "obsolete:action")
		if !strings.Contains(lastScreen(t, f).Params.Get("text"), "no longer available") {
			t.Fatal("unknown callback was silent")
		}
	})
	t.Run("empty inventory", func(t *testing.T) {
		f.reset()
		testCallback(app, 1, "menu:services")
		if !strings.Contains(lastScreen(t, f).Params.Get("text"), "No") {
			t.Fatal("missing empty state")
		}
	})
	t.Run("old positional inventory", func(t *testing.T) {
		f.reset()
		testCallback(app, 1, "buy:c:0:0")
		if !strings.Contains(lastScreen(t, f).Params.Get("text"), "outdated") {
			t.Fatal("unsafe positional callback")
		}
	})
}

func TestGuidedConfirmationIntegration(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	testCallback(app, 1, "admin:tutorial:add")
	testText(app, 1, "Getting started")
	testText(app, 1, "A short guide")
	testText(app, 1, "Choose Get Number")
	flow, err := app.store.TelegramFlow(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	confirm := "guide:confirm:" + flow.Data["nonce"]
	// A new App simulates restart; all form state is in PostgreSQL.
	restarted := New(app.bot, app.store, time.Minute, time.UTC)
	testCallback(restarted, 1, confirm)
	testCallback(restarted, 1, confirm)
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM tutorials").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate mutation count=%d", count)
	}
	testCallback(app, 1, "admin:tutorial:add")
	f.reset()
	testCallback(app, 1, confirm)
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "replaced") {
		t.Fatal("old confirmation accepted")
	}
	testCallback(app, 1, "menu:compact")
	if _, err = app.store.TelegramFlow(ctx, 1, 1); err == nil {
		t.Fatal("Home did not cancel form")
	}
	testCallback(app, 1, "admin:tutorial:add")
	if _, err = pool.Exec(ctx, "UPDATE telegram_flows SET expires_at=now()-interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	f.reset()
	testCallback(app, 1, confirm)
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "expired") {
		t.Fatal("expired form accepted")
	}
}

func TestMembershipAndClaimsIntegration(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	if err := app.store.UpsertRequiredChat(ctx, store.RequiredChat{BotInstanceID: 1, ChatID: -100, Title: "Updates", InviteURL: "https://t.me/test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	f.member = "left"
	testCallback(app, 2, "menu:services")
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "Join all") {
		t.Fatal("callback bypassed membership")
	}
	flow := store.TelegramFlow{Kind: "guided", Step: "1", Data: map[string]string{"nonce": "unique"}}
	if err := app.store.SetTelegramFlow(ctx, 1, 1, flow); err != nil {
		t.Fatal(err)
	}
	flow, err := app.store.TelegramFlow(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, e := app.store.ClaimTelegramFlow(ctx, 1, 1, flow)
			if e != nil {
				t.Error(e)
			}
			results <- ok
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for ok := range results {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("claimed %d times", count)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM required_chats"); err != nil {
		t.Fatal(err)
	}
}

func TestWithdrawalConfirmationOnceIntegration(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE users SET balance_pkr=1000 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	account, err := app.store.AddWithdrawalAccount(ctx, 1, 1, "jazzcash", "0300•••567", "03001234567 | Test")
	if err != nil {
		t.Fatal(err)
	}
	testCallback(app, 1, fmt.Sprintf("wd:use:%d", account))
	testText(app, 1, "100")
	flow, err := app.store.TelegramFlow(ctx, 1, 1)
	if err != nil || flow.Step != "confirm" {
		t.Fatalf("withdrawal not ready: %v %+v", err, flow)
	}
	testCallback(app, 1, "wd:confirm")
	testCallback(app, 1, "wd:confirm")
	var count int
	var balance float64
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM withdrawals WHERE user_id=1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "SELECT balance_pkr FROM users WHERE id=1").Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if count != 1 || balance != 900 {
		t.Fatalf("duplicate withdrawal: count=%d balance=%f", count, balance)
	}
	f.reset()
	testCallback(app, 3, "guide:start:admin")
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "permission") {
		t.Fatal("guided action bypassed role gate")
	}
}

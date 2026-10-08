package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/broadcast"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const modernEntities = `[{"type":"custom_emoji","offset":3,"length":2,"custom_emoji_id":"5334544901428229844"},{"type":"bold","offset":6,"length":5}]`

func TestLosslessUpdatesAndUTF16CommandContent(t *testing.T) {
	raw := json.RawMessage(`[{"update_id":18,"message":{"message_id":43,"text":"🚀 🤩 hello","from":{"id":1},"chat":{"id":1,"type":"private"},"entities":` + modernEntities + `}}]`)
	updates, err := decodeUpdates(raw)
	if err != nil || len(updates) != 1 {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), entityContextKey{}, updates[0])
	entities := originalEntities(ctx, updates[0].Update.Message, false)
	if string(entities) != modernEntities {
		t.Fatal("premium entity lost", string(entities))
	}
	if err = validateBroadcastEntities(updates[0].Update.Message.Text, entities); err != nil {
		t.Fatal(err)
	}
	commandEntities := json.RawMessage(`[{"type":"bot_command","offset":0,"length":10},{"type":"custom_emoji","offset":14,"length":2,"custom_emoji_id":"5334544901428229844"},{"type":"bold","offset":17,"length":5}]`)
	body, adjusted, err := commandContent("/broadcast 🚀 🤩 hello", commandEntities)
	if err != nil || body != "🚀 🤩 hello" {
		t.Fatal(body, err)
	}
	var got, want any
	_ = json.Unmarshal(adjusted, &got)
	_ = json.Unmarshal([]byte(modernEntities), &want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatal("UTF-16 offsets changed", string(adjusted))
	}
	if _, _, err = commandContent("/broadcast hello", json.RawMessage(`[{"type":"bold","offset":0,"length":16}]`)); err == nil {
		t.Fatal("formatting crossing the command prefix accepted")
	}
	if err = validateBroadcastEntities("🤩", json.RawMessage(`[{"type":"custom_emoji","offset":0,"length":2}]`)); err == nil {
		t.Fatal("missing custom emoji ID accepted")
	}
}

func TestPollingIsContextBoundAndPreservesUpdates(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "getMe") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`))
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("offset") != "19" || r.Form.Get("timeout") != "30" {
			t.Error("poll parameters changed")
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	bot, err := tgbotapi.NewBotAPIWithClient("test", server.URL+"/bot%s/%s", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	a := New(bot, nil, time.Minute, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := a.pollUpdates(ctx, 19); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel did not stop polling")
		}
	case <-time.After(time.Second):
		t.Fatal("long poll ignored cancellation")
	}
}

func TestRichRenderingAndClassicFallback(t *testing.T) {
	for _, test := range []struct {
		name, reject string
		classic      bool
		methods      []string
	}{
		{"rich", "", false, []string{"sendRichMessage"}},
		{"classic preference", "", true, []string{"sendMessage"}},
		{"unsupported", "Bad Request: unsupported rich_message", false, []string{"sendRichMessage", "sendMessage"}},
		{"unknown method", "Not Found: method not found", false, []string{"sendRichMessage", "sendMessage"}},
		{"bad content fallback", "Bad Request: invalid content", false, []string{"sendRichMessage", "sendMessage"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bot, f := newFakeTelegram(t)
			f.reject = test.reject
			a := New(bot, nil, time.Minute, time.UTC)
			if test.classic {
				a.displayFormat = "classic"
			}
			doc := screenDocument{RichHTML: `<h2>Countries</h2><table><tr><td>Pakistan</td><td>5</td></tr></table>`, ClassicHTML: "<b>Countries</b>\nPakistan — 5", Keyboard: userBackMenu()}
			if err := a.renderDocument(1, doc); err != nil {
				t.Fatal(err)
			}
			calls := f.snapshot()
			if len(calls) != len(test.methods) {
				t.Fatalf("unexpected calls %+v", calls)
			}
			for i, call := range calls {
				if call.Method != test.methods[i] {
					t.Fatal(call.Method)
				}
			}
			if len(calls) > 1 && !strings.Contains(calls[1].Params.Get("text"), "Pakistan") {
				t.Fatal("fallback lost table data")
			}
		})
	}
}

type failRichClient struct {
	base  tgbotapi.HTTPClient
	calls int
}

func (c *failRichClient) Do(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "sendRichMessage") {
		c.calls++
		return nil, context.DeadlineExceeded
	}
	return c.base.Do(req)
}
func TestRichTransportTimeoutDoesNotDuplicate(t *testing.T) {
	bot, f := newFakeTelegram(t)
	client := &failRichClient{base: bot.Client}
	bot.Client = client
	a := New(bot, nil, time.Minute, time.UTC)
	err := a.renderDocument(1, screenDocument{RichHTML: "<p>One message</p>", ClassicHTML: "One message", Keyboard: userBackMenu()})
	if !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || len(f.snapshot()) != 0 {
		t.Fatal("uncertain rich send duplicated", err)
	}
}

func TestCommandRegistryAndSemanticIcons(t *testing.T) {
	seen := map[string]bool{}
	pattern := regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	for _, d := range commandRegistry {
		if seen[d.Name] || !pattern.MatchString(d.Name) || d.Description == "" || len(d.Description) > 256 {
			t.Fatal("invalid command", d)
		}
		seen[d.Name] = true
	}
	if len(seen) > 100 {
		t.Fatal("Telegram command list too long")
	}
	for _, name := range []string{"liveotp", "topapps", "topcountries", "favorites", "lastselection", "alerts", "broadcast", "settheme", "cancel"} {
		if !seen[name] {
			t.Fatal("missing command", name)
		}
	}
	if premium.ID("phone") == premium.AppEmojiID("Instagram") || premium.ID("phone") == premium.AppEmojiID("Telegram") {
		t.Fatal("Get Number uses an app logo")
	}
	if premium.CountryEmojiID("unknown") == premium.CountryEmojiID("UA") {
		t.Fatal("unknown country presented as Ukraine")
	}
	if premium.AppEmojiID("nottelegram") == premium.AppEmojiID("Telegram") {
		t.Fatal("substring application misclassified")
	}
	for _, menu := range []premium.InlineKeyboard{compactMenu(true, true), fullMenu(true, true, themes.Links{}), assignmentMenu("WhatsApp", "Pakistan")} {
		for _, row := range menu.InlineKeyboard {
			if len(row) > 2 {
				t.Fatal("crowded main-menu row")
			}
			for _, button := range row {
				if len(button.CallbackData) > 64 {
					t.Fatal("callback exceeds Telegram limit")
				}
				if button.CallbackData == "menu:home" && button.IconCustomEmojiID != premium.ID("home") {
					t.Fatal("inconsistent home icon")
				}
			}
		}
	}
}

func TestPremiumBroadcastRoundTripIntegration(t *testing.T) {
	for _, kind := range []string{"text", "photo", "video"} {
		t.Run(kind, func(t *testing.T) {
			a, f, _ := newIntegrationApp(t)
			ctx := context.Background()
			testCallback(a, 1, "admin:broadcast")
			media := ""
			body := `"text":"🚀 🤩 hello","entities":` + modernEntities
			if kind != "text" {
				body = `"caption":"🚀 🤩 hello","caption_entities":` + modernEntities
				if kind == "photo" {
					media = `,"photo":[{"file_id":"fixture-photo","width":10,"height":10}]`
				} else {
					media = `,"video":{"file_id":"fixture-video","width":10,"height":10,"duration":1}`
				}
			}
			raw := json.RawMessage(`[{"update_id":19,"message":{"message_id":43,"from":{"id":1},"chat":{"id":1,"type":"private"},` + body + media + `}}]`)
			updates, err := decodeUpdates(raw)
			if err != nil {
				t.Fatal(err)
			}
			a.handleUpdate(context.WithValue(ctx, entityContextKey{}, updates[0]), updates[0].Update)
			flow, err := a.store.TelegramFlow(ctx, 1, 1)
			if err != nil || flow.Step != "audience" {
				t.Fatal("preview failed", err)
			}
			var draft store.Broadcast
			if err = json.Unmarshal([]byte(flow.Data["content"]), &draft); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(draft.Entities), "custom_emoji_id") {
				t.Fatal("draft lost animation")
			}
			testCallback(a, 1, "admin:broadcast:audience:"+flow.Data["nonce"]+":all")
			testCallback(a, 1, "admin:broadcast:confirm:"+flow.Data["nonce"])
			for {
				more, e := a.store.PrepareBroadcastBatch(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if !more {
					break
				}
			}
			job, err := a.store.ClaimBroadcast(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.reset()
			if err = broadcast.Send(a.bot, job, job.Recipient); err != nil {
				t.Fatal(err)
			}
			field := "entities"
			if kind != "text" {
				field = "caption_entities"
			}
			var delivered any
			_ = json.Unmarshal([]byte(f.snapshot()[0].Params.Get(field)), &delivered)
			if !strings.Contains(fmt.Sprint(delivered), "5334544901428229844") {
				t.Fatal("queued delivery lost premium emoji", delivered)
			}
		})
	}
}

func TestActivityPrivacyPaginationAndToolsIntegration(t *testing.T) {
	a, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if _, err := a.store.AddNumbers(ctx, "WhatsApp", fmt.Sprintf("Country %02d", i), "", 1, 0, 1, []string{fmt.Sprintf("90000000%04d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range []domain.OTPEvent{
		{DedupKey: "one", NormalizedPhone: "100", Phone: "100", Message: "private-secret", Code: "111111", Service: "WhatsApp", Country: "Country 00", BotInstanceID: 1},
		{DedupKey: "two", NormalizedPhone: "101", Phone: "101", Message: "private-other", Code: "222222", Service: "Telegram", Country: "Country 00", BotInstanceID: 1},
		{DedupKey: "old", NormalizedPhone: "102", Phone: "102", Message: "private-old", Code: "333333", Service: "Other App", Country: "Historical", BotInstanceID: 1, ReceivedAt: time.Now().Add(-40 * 24 * time.Hour)},
		{DedupKey: "no-code", NormalizedPhone: "103", Phone: "103", Message: "non-otp", Service: "WhatsApp", Country: "Country 00", BotInstanceID: 1},
	} {
		if _, err := a.store.AcceptOTP(ctx, event); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.AcceptOTP(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := a.store.Activity(ctx, 1, "24h")
	if err != nil || snapshot.Total != 2 || len(snapshot.Countries) != 13 {
		t.Fatalf("counts or zero countries wrong %+v %v", snapshot, err)
	}
	child, err := a.store.CreateChildBotRequest(ctx, 1, "Activity child", "fixture-activity-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE otp_events SET bot_instance_id=$1 WHERE dedup_key='two'`, child); err != nil {
		t.Fatal(err)
	}
	snapshot, err = a.store.Activity(ctx, 1, "24h")
	if err != nil || snapshot.Total != 1 {
		t.Fatal("child activity leaked", snapshot.Total, err)
	}
	childSnapshot, err := a.store.Activity(ctx, child, "24h")
	if err != nil || childSnapshot.Total != 1 {
		t.Fatal("child counts wrong", err)
	}
	doc, err := a.liveDocument(ctx, liveSession{User: 2, View: "countries", Period: "24h", Page: 0})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.RichHTML, "private-") || strings.Contains(doc.ClassicHTML, "111111") || !strings.Contains(doc.ClassicHTML, "Page 1/2") {
		t.Fatal("privacy or pagination failed", doc.ClassicHTML)
	}
	doc, err = a.liveDocument(ctx, liveSession{User: 2, View: "countries", Period: "24h", Page: 1})
	if err != nil || !strings.Contains(doc.ClassicHTML, "Historical") {
		t.Fatal("historical zero-count country absent", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE otp_events SET assigned_user_id=2 WHERE dedup_key='one'`); err != nil {
		t.Fatal(err)
	}
	personal, err := a.liveDocument(ctx, liveSession{User: 2, View: "private", Period: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(personal.ClassicHTML, "111111") || strings.Contains(personal.ClassicHTML, "222222") {
		t.Fatal("personal OTP isolation failed")
	}
	if err = a.store.SaveLastSelection(ctx, 1, 2, "WhatsApp", "Country 00"); err != nil {
		t.Fatal(err)
	}
	testCallback(a, 2, "tools:add:"+selectionKey("WhatsApp")+":"+selectionKey("Country 00"))
	saved, err := a.store.SavedSelections(ctx, 1, 2, false)
	if err != nil || len(saved) != 1 {
		t.Fatal("favorite not saved", err)
	}
	f.reset()
	testCallback(a, 3, fmt.Sprintf("tools:use:%d", saved[0].ID))
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "removed") {
		t.Fatal("another user accessed a favorite")
	}
	testCallback(a, 2, "prefs:display:classic")
	pref, err := a.store.Preference(ctx, 1, 2)
	if err != nil || pref.DisplayFormat != "classic" {
		t.Fatal("display format not saved", err)
	}
	testCallback(a, 2, "menu:liveotp")
	testCallback(a, 2, "live:pause")
	a.ui.mu.Lock()
	paused := a.ui.sessions[2].Paused
	a.ui.mu.Unlock()
	if !paused {
		t.Fatal("pause failed")
	}
	testCallback(a, 2, "live:resume")
	a.ui.mu.Lock()
	a.ui.sessions = map[int64]liveSession{}
	a.ui.mu.Unlock()
	testCallback(a, 2, "live:resume")
	a.ui.mu.Lock()
	restored := a.ui.sessions[2]
	a.ui.mu.Unlock()
	if restored.Paused || restored.Message != 42 {
		t.Fatal("persisted screen did not resume after restart")
	}
	testCallback(a, 2, "menu:home")
	a.ui.mu.Lock()
	paused = a.ui.sessions[2].Paused
	a.ui.mu.Unlock()
	if !paused {
		t.Fatal("navigation did not pause updates")
	}
}

func TestLiveRefreshOnlyEditsChangedContentIntegration(t *testing.T) {
	a, f, _ := newIntegrationApp(t)
	ctx := context.Background()
	session := liveSession{User: 2, Chat: 2, View: "private", Period: "24h", Paused: false, Expires: time.Now().Add(10 * time.Minute)}
	if err := a.refreshLive(ctx, &session, true); err != nil {
		t.Fatal(err)
	}
	f.reset()
	if err := a.refreshLive(ctx, &session, false); err != nil {
		t.Fatal(err)
	}
	if len(f.snapshot()) != 0 {
		t.Fatal("unchanged dashboard edited")
	}
	session.Paused = true
	if err := a.refreshLive(ctx, &session, false); err != nil {
		t.Fatal(err)
	}
	if len(f.snapshot()) != 1 {
		t.Fatal("pause state change was not rendered")
	}
}

func TestCommandPermissionsAndAvailabilityAlertsIntegration(t *testing.T) {
	a, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	public, err := a.commandMenu(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range public {
		if v.Command == "broadcast" || v.Command == "admin" || v.Command == "settheme" {
			t.Fatal("admin command in public menu", v)
		}
	}
	if err = a.store.AddInstanceAdmin(ctx, 1, 3, []string{"broadcast"}); err != nil {
		t.Fatal(err)
	}
	restricted, err := a.commandMenu(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range restricted {
		if v.Command == "broadcast" {
			found = true
		}
		if v.Command == "addpanel" || v.Command == "addadmin" {
			t.Fatal("restricted admin command leak", v)
		}
	}
	if !found {
		t.Fatal("authorized broadcast missing")
	}
	if err = a.syncCommandMenu(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err = a.store.RemoveInstanceAdmin(ctx, 1, 3); err != nil {
		t.Fatal(err)
	}
	f.reset()
	if err = a.syncCommandMenu(ctx, 3); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.snapshot() {
		if call.Method == "setMyCommands" && strings.Contains(call.Params.Get("commands"), "broadcast") {
			t.Fatal("revoked admin menu retained")
		}
	}
	for _, command := range []string{"/getnumber", "/settheme", "/broadcast", "/addpanel", "/createbot"} {
		f.reset()
		testText(a, 1, command)
		screen := lastScreen(t, f)
		if strings.Contains(screen.Params.Get("text"), "Usage:") || strings.Contains(screen.Params.Get("text"), "could not be completed") {
			t.Fatal("command did not open a workflow", command, screen.Params.Get("text"))
		}
	}
	if _, err = a.store.AddNumbers(ctx, "WatchApp", "WatchCountry", "", 1, 0, 1, []string{"923000001234"}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE numbers SET state='assigned' WHERE service='WatchApp'`); err != nil {
		t.Fatal(err)
	}
	watch, err := a.store.AddSavedSelection(ctx, 1, 2, "WatchApp", "WatchCountry", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.CheckAvailabilityWatches(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ui_notification_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unavailable inventory alerted", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE numbers SET state='available' WHERE service='WatchApp';INSERT INTO number_user_exclusions(number_id,user_id,expires_at) SELECT id,2,now()+interval '1 hour' FROM numbers WHERE service='WatchApp'`); err != nil {
		t.Fatal(err)
	}
	if err = a.store.CheckAvailabilityWatches(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ui_notification_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("personal cooldown ignored", err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM number_user_exclusions WHERE user_id=2`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = a.store.CheckAvailabilityWatches(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ui_notification_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatal("transition duplicated", count, err)
	}
	f.reset()
	a.deliverAvailabilityNotification(ctx)
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ui_notification_jobs WHERE state='sent'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("alert not delivered", err)
	}
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "WatchApp") {
		t.Fatal("alert payload missing")
	}
	if err = a.store.RemoveSavedSelection(ctx, 1, 2, watch, true); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE availability_watches SET was_available=false,last_notified_at=NULL WHERE id=$1`, watch); err != nil {
		t.Fatal(err)
	}
	if err = a.store.CheckAvailabilityWatches(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ui_notification_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatal("unsubscribed watch alerted", err)
	}
	for i := 0; i < 21; i++ {
		name := fmt.Sprintf("LimitCountry%02d", i)
		if _, err = a.store.AddNumbers(ctx, "LimitApp", name, "", 1, 0, 1, []string{fmt.Sprintf("93000000%04d", i)}); err != nil {
			t.Fatal(err)
		}
		_, err = a.store.AddSavedSelection(ctx, 1, 2, "LimitApp", name, false)
		if (i < 20 && err != nil) || (i == 20 && err == nil) {
			t.Fatal("favorite limit incorrect", i, err)
		}
		if i < 11 {
			_, err = a.store.AddSavedSelection(ctx, 1, 2, "LimitApp", name, true)
			if (i < 10 && err != nil) || (i == 10 && err == nil) {
				t.Fatal("alert limit incorrect", i, err)
			}
		}
	}
	if _, err = a.store.AddSavedSelection(ctx, 1, 2, "LimitApp", "LimitCountry00", false); err != nil {
		t.Fatal("duplicate favorite at capacity was rejected", err)
	}
}

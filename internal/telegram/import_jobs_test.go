package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type importFileClient struct {
	Body  []byte
	Calls int
}

func (c *importFileClient) Do(req *http.Request) (*http.Response, error) {
	c.Calls++
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(c.Body)), Request: req}, nil
}
func uploadFixture(app *App, user int64, caption string) {
	app.handleUpdate(context.Background(), tgbotapi.Update{Message: &tgbotapi.Message{MessageID: 43, Caption: caption, From: &tgbotapi.User{ID: user}, Chat: &tgbotapi.Chat{ID: user, Type: "private"}, Document: &tgbotapi.Document{FileID: "fixture", FileName: "allocation.txt", FileSize: 30}}})
}

func TestImportCSVUsesPhoneColumnAndKeepsCounts(t *testing.T) {
	for _, body := range []string{
		"\ufeffphone,price,range\n\"+12025550123\",1,US\n+12025550124,2,US\n+12025550123,3,US\ninvalid,4,US\n",
		"range;phone_number;price\nUS;+12025550123;1\nUS;+12025550124;2\nUS;+12025550123;3\nUS;invalid;4\n",
		"\"+12025550123\"\n+12025550124\n+12025550123\ninvalid\n",
	} {
		phones, invalid, repeated := parseImportPhones([]byte(body))
		if len(phones) != 2 || invalid != 1 || repeated != 1 {
			t.Fatal("CSV/TXT counts changed", phones, invalid, repeated)
		}
	}
}

func TestQueuedUploadResumesInNewWorkerAndEditsReceipt(t *testing.T) {
	app, fake, pool := newIntegrationApp(t)
	ctx := context.Background()
	app.fileClient = &importFileClient{Body: []byte("+12025550123")}
	uploadFixture(app, 1, "/addnumbers WhatsApp|United States|US|1|0|3")
	flow, err := app.store.TelegramFlow(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	testCallback(app, 1, "admin:upload:confirm~"+flow.Data["nonce"])
	fake.reset()
	restarted := New(app.bot, app.store, time.Minute, time.UTC)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); restarted.runImports(runCtx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(6 * time.Second)
	for {
		var state string
		var notified bool
		err = pool.QueryRow(ctx, `SELECT state,notified_at IS NOT NULL FROM number_import_jobs WHERE nonce=$1`, flow.Data["nonce"]).Scan(&state, &notified)
		if err != nil {
			t.Fatal(err)
		}
		if state == "succeeded" && notified {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restarted worker did not finish queued upload", state)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if call := lastScreen(t, fake); call.Method != "editMessageText" || !strings.Contains(call.Params.Get("text"), "published together") {
		t.Fatal("worker did not replace progress with receipt", call.Method)
	}
}

func TestDraftCancelIsScopedToCurrentUpload(t *testing.T) {
	app, _, _ := newIntegrationApp(t)
	ctx := context.Background()
	app.fileClient = &importFileClient{Body: []byte("+12025550123")}
	uploadFixture(app, 1, "")
	first, _ := app.store.TelegramFlow(ctx, 1, 1)
	uploadFixture(app, 1, "")
	current, _ := app.store.TelegramFlow(ctx, 1, 1)
	testCallback(app, 1, "admin:upload:cancel~"+first.Data["nonce"])
	if flow, err := app.store.TelegramFlow(ctx, 1, 1); err != nil || flow.Data["nonce"] != current.Data["nonce"] {
		t.Fatal("old cancel changed current upload", err)
	}
	testCallback(app, 1, "admin:upload:cancel~"+current.Data["nonce"])
	job, err := app.store.ImportByNonce(ctx, 1, 1, current.Data["nonce"])
	if err != nil || job.State != "cancelled" {
		t.Fatal("draft not cancelled", job.State, err)
	}
	if _, err = app.store.TelegramFlow(ctx, 1, 1); err == nil {
		t.Fatal("cancelled form remained active")
	}
}
func TestDirectUploadMultiSelectionUsesOneMessageAndOneDownload(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	files := &importFileClient{Body: []byte("+12025550123\n+12025550124\n+12025550123\ninvalid")}
	app.fileClient = files
	uploadFixture(app, 1, "")
	flow, err := app.store.TelegramFlow(ctx, 1, 1)
	if err != nil || flow.Step != "multi_service" {
		t.Fatal("direct upload failed", flow.Step, err)
	}
	nonce := flow.Data["nonce"]
	if files.Calls != 1 {
		t.Fatal("file not downloaded once")
	}
	f.reset()
	for _, name := range []string{"WhatsApp", "Telegram", "WhatsApp", "Telegram"} {
		testCallback(app, 1, "admin:upload:mp:"+selectionKey(name)+"~"+nonce)
	}
	flow, err = app.store.TelegramFlow(ctx, 1, 1)
	if err != nil || len(importServices(flow)) != 0 {
		t.Fatal("empty selection restored an old app", importServices(flow), err)
	}
	for _, call := range f.snapshot() {
		if call.Method == "sendMessage" || call.Method == "sendRichMessage" {
			t.Fatal("selection sent another message", call.Method)
		}
	}
	testCallback(app, 1, "admin:upload:mp:"+selectionKey("WhatsApp")+"~stale")
	flow, _ = app.store.TelegramFlow(ctx, 1, 1)
	if len(importServices(flow)) != 0 {
		t.Fatal("stale button changed selection")
	}
	for _, name := range []string{"WhatsApp", "Telegram"} {
		testCallback(app, 1, "admin:upload:mp:"+selectionKey(name)+"~"+nonce)
	}
	testCallback(app, 1, "admin:upload:mdone~"+nonce)
	if text := lastScreen(t, f).Params.Get("text"); !strings.Contains(text, "WhatsApp, Telegram") {
		t.Fatal("multi-app pricing mislabeled", text)
	}
	testCallback(app, 1, "admin:upload:pricing:default~"+nonce)
	if text := lastScreen(t, f).Params.Get("text"); !strings.Contains(text, "2 additions") || !strings.Contains(text, "App inventory additions") {
		t.Fatal("preview lost app counts", text)
	}
	testCallback(app, 1, "admin:upload:confirm~"+nonce)
	testCallback(app, 1, "admin:upload:confirm~"+nonce)
	if files.Calls != 1 {
		t.Fatal("preview or confirmation downloaded file again", files.Calls)
	}
	job, err := app.store.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.store.ExecuteImport(ctx, job); err != nil {
		t.Fatal(err)
	}
	var rows, jobs int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM numbers),(SELECT count(*) FROM number_import_jobs)`).Scan(&rows, &jobs); err != nil || rows != 4 || jobs != 1 {
		t.Fatal("wrong import counts", rows, jobs, err)
	}
}
func TestCaptionUploadKeepsPricingAndPermissionChecks(t *testing.T) {
	app, _, pool := newIntegrationApp(t)
	ctx := context.Background()
	files := &importFileClient{Body: []byte("+923001234567")}
	app.fileClient = files
	uploadFixture(app, 2, "")
	uploadFixture(app, 3, "")
	if files.Calls != 0 {
		t.Fatal("unauthorized upload downloaded a file")
	}
	uploadFixture(app, 1, "/addnumbers WhatsApp|Pakistan|PK|7|0.25|2")
	flow, err := app.store.TelegramFlow(ctx, 1, 1)
	if err != nil || flow.Step != "confirm" {
		t.Fatal("caption did not prefill confirmation", flow, err)
	}
	testCallback(app, 1, "admin:upload:confirm~"+flow.Data["nonce"])
	job, err := app.store.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.store.ExecuteImport(ctx, job); err != nil {
		t.Fatal(err)
	}
	var price float64
	var cycle int
	if err = pool.QueryRow(ctx, `SELECT price_pkr,numbers_per_cycle FROM service_countries WHERE service='WhatsApp' AND country='Pakistan'`).Scan(&price, &cycle); err != nil || price != 7 || cycle != 2 {
		t.Fatal(price, cycle, err)
	}
}
func TestSelectorPaginationLimitSearchAndLeavingProgress(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO service_countries(service,country,country_code) VALUES($1,'United States','US')`, fmt.Sprintf("Fixture App %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	app.fileClient = &importFileClient{Body: []byte("+12025550123")}
	uploadFixture(app, 1, "")
	flow, _ := app.store.TelegramFlow(ctx, 1, 1)
	nonce := flow.Data["nonce"]
	for _, route := range []string{"pickpage", "page:1", "pickpage", "page:2", "pickpage"} {
		testCallback(app, 1, "admin:upload:"+route+"~"+nonce)
	}
	flow, _ = app.store.TelegramFlow(ctx, 1, 1)
	if len(importServices(flow)) != 20 {
		t.Fatal("selection limit failed", len(importServices(flow)))
	}
	testCallback(app, 1, "admin:upload:search~"+nonce)
	testText(app, 1, "WhatsApp")
	flow, _ = app.store.TelegramFlow(ctx, 1, 1)
	if flow.Step != "multi_service" || flow.Data["search_query"] != "WhatsApp" || len(importServices(flow)) != 20 {
		t.Fatal(flow)
	}
	testCallback(app, 1, "admin:upload:clear~"+nonce)
	testCallback(app, 1, "admin:upload:mp:"+selectionKey("WhatsApp")+"~"+nonce)
	testCallback(app, 1, "admin:upload:mdone~"+nonce)
	testCallback(app, 1, "admin:upload:pricing:default~"+nonce)
	testCallback(app, 1, "admin:upload:confirm~"+nonce)
	job, err := app.store.ImportByNonce(ctx, 1, 1, nonce)
	if err != nil {
		t.Fatal(err)
	}
	testCallback(app, 1, "menu:home")
	f.reset()
	if err = app.importBackgroundScreen(ctx, job.ID, 1); err != nil {
		t.Fatal(err)
	}
	if len(f.snapshot()) != 0 {
		t.Fatal("background receipt replaced the main menu")
	}
}
func TestStreamCategoryAndGenericProviderNames(t *testing.T) {
	app, f, _ := newIntegrationApp(t)
	flat := fmt.Sprint(panelKindMenu())
	if strings.Contains(flat, "IVAS Socket.IO") || strings.Contains(flat, "Socket.IO Stream") || strings.Contains(flat, "WebSocket Stream") || !strings.Contains(flat, "Live SMS Stream") || !strings.Contains(flat, "ASP SMS API") || !strings.Contains(flat, "IPRN REST API") {
		t.Fatal("duplicate or branded panel types", flat)
	}
	testCallback(app, 1, "admin:panel:add")
	testText(app, 1, "Streams")
	testCallback(app, 1, "admin:panel:kind:stream")
	if menu := lastScreen(t, f).Params.Get("reply_markup"); !strings.Contains(menu, "IVAS Account") || !strings.Contains(menu, "Stream URL") || !strings.Contains(menu, "Plain WebSocket") {
		t.Fatal("stream methods missing", menu)
	}
	testCallback(app, 1, "admin:panel:kind:socketio")
	flow, err := app.store.TelegramFlow(context.Background(), 1, 1)
	if err != nil || flow.Data["kind"] != "socketio" {
		t.Fatal(flow, err)
	}
}
func TestInterfaceTablesAndOTPPreviewsPreserveOriginalThemes(t *testing.T) {
	app, f, _ := newIntegrationApp(t)
	app.sendHTML(1, "Settings\n\nTimezone: Asia/Karachi\nTheme: Classic", userBackMenu())
	call := lastScreen(t, f)
	var rich inputRichMessage
	if call.Method != "sendRichMessage" || json.Unmarshal([]byte(call.Params.Get("rich_message")), &rich) != nil || !strings.Contains(rich.HTML, "<table") || !strings.Contains(rich.HTML, "<tg-emoji") {
		t.Fatal("interface formatting missing", call)
	}
	f.reset()
	app.sendHTML(1, "Welcome to CrackSMS\n\nChoose Get Number to begin.", userBackMenu())
	call = lastScreen(t, f)
	if json.Unmarshal([]byte(call.Params.Get("rich_message")), &rich) != nil || strings.Contains(rich.HTML, "<table") || !strings.Contains(rich.HTML, "<p>") {
		t.Fatal("welcome prompt should be prose", call)
	}
	f.reset()
	message := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1, Type: "private"}, From: &tgbotapi.User{ID: 1}}
	app.handleTheme(context.Background(), message, "otpguipreview", "")
	event := domain.OTPEvent{PanelName: "Preview Panel", NormalizedPhone: "923001234567", Service: "WhatsApp", Code: "123456", Message: "Your WhatsApp verification code is 123456"}
	calls := f.snapshot()
	if len(calls) != 10 {
		t.Fatal("OTP theme count changed", len(calls))
	}
	for i, call := range calls {
		expected := premium.AnimateHTML(fmt.Sprintf("<b>T%d · %s</b>\n\n%s", i, themes.Get(i).Name, themes.Format(event, i, true, "visible")))
		if call.Method != "sendMessage" || call.Params.Get("text") != expected || call.Params.Get("rich_message") != "" {
			t.Fatal("OTP preview layout changed", i)
		}
	}
}

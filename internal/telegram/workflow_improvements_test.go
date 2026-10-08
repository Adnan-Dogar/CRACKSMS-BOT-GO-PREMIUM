package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/broadcast"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestAddSeveralCustomAppsKeepsSelectorAndDistinctEmojiIDs(t *testing.T) {
	a, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	a.fileClient = &importFileClient{Body: []byte("+12025550123")}
	uploadFixture(a, 1, "")
	flow, _ := a.store.TelegramFlow(ctx, 1, 1)
	nonce := flow.Data["nonce"]
	if !strings.Contains(lastScreen(t, f).Params.Get("reply_markup"), "Add App") {
		t.Fatal("Add App button absent")
	}
	for i, name := range []string{"Custom App One", "Custom App Two"} {
		testCallback(a, 1, "admin:upload:other~"+nonce)
		testText(a, 1, name)
		testText(a, 1, fmt.Sprint(5334998226636390258+int64(i)))
		flow, err := a.store.TelegramFlow(ctx, 1, 1)
		if err != nil || flow.Step != "multi_service" || len(importServices(flow)) != i+1 {
			t.Fatal("Add App skipped selector", flow, err)
		}
	}
	testCallback(a, 1, "admin:upload:other~"+nonce)
	testText(a, 1, "Not Saved")
	testCallback(a, 1, "admin:upload:apps~"+nonce)
	flow, _ = a.store.TelegramFlow(ctx, 1, 1)
	if flow.Step != "multi_service" || len(importServices(flow)) != 2 {
		t.Fatal("back lost selection")
	}
	testCallback(a, 1, "admin:upload:other~"+nonce)
	testText(a, 1, "custom app one")
	flow, _ = a.store.TelegramFlow(ctx, 1, 1)
	if flow.Step != "multi_service" || len(importServices(flow)) != 2 {
		t.Fatal("duplicate app accepted")
	}
	testCallback(a, 1, "admin:upload:mdone~"+nonce)
	testCallback(a, 1, "admin:upload:pricing:default~"+nonce)
	testCallback(a, 1, "admin:upload:confirm~"+nonce)
	job, err := a.store.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.ExecuteImport(ctx, job); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Custom App One", "Custom App Two"} {
		id, err := a.store.ServiceEmojiID(ctx, 1, name)
		if err != nil || id != fmt.Sprint(5334998226636390258+int64(i)) {
			t.Fatal("emoji assigned to wrong app", name, id, err)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM numbers`).Scan(&count); err != nil || count != 2 {
		t.Fatal("custom app inventory incomplete", count, err)
	}
}

func TestLimitedRewardWizardAsksOTPUsersThenAmount(t *testing.T) {
	a, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	testCallback(a, 1, "admin:reward:limited")
	if call := lastScreen(t, f); !strings.Contains(call.Params.Get("text"), "OTP target") && !strings.Contains(call.Params.Get("rich_message"), "OTP target") {
		t.Fatal("first step did not ask for OTP target")
	}
	testText(a, 1, "1000")
	testText(a, 1, "5")
	testText(a, 1, "2 USD")
	flow, err := a.store.TelegramFlow(ctx, 1, 1)
	if err != nil || flow.Step != "3" {
		t.Fatal("reward wizard did not reach review", flow, err)
	}
	confirm := "guide:confirm:" + flow.Data["nonce"]
	testCallback(a, 1, confirm)
	testCallback(a, 1, confirm)
	var threshold, maxUsers, schedules int
	var usd float64
	if err = pool.QueryRow(ctx, `SELECT r.threshold,r.max_users,r.reward_usd,(SELECT count(*) FROM reward_schedules) FROM reward_rules r`).Scan(&threshold, &maxUsers, &usd, &schedules); err != nil || threshold != 1000 || maxUsers != 5 || usd != 2 || schedules != 1 {
		t.Fatal("limited reward was not saved once", threshold, maxUsers, usd, schedules, err)
	}
}
func TestUserBackupUploadPreviewAndConfirm(t *testing.T) {
	a, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	parts, err := a.store.ExportUserBackup(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	a.fileClient = &importFileClient{Body: parts[0].File}
	upload := func(user int64) {
		a.handleUpdate(ctx, tgbotapi.Update{Message: &tgbotapi.Message{MessageID: 71, From: &tgbotapi.User{ID: user}, Chat: &tgbotapi.Chat{ID: user, Type: "private"}, Document: &tgbotapi.Document{FileID: "fixture", FileName: "users.csbackup", FileSize: len(parts[0].File)}}})
	}
	upload(2)
	var jobs int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_backup_jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("non-admin uploaded a backup", jobs, err)
	}
	upload(1)
	j, err := a.store.ClaimUserBackup(ctx)
	if err != nil || j.Phase != "inspect" {
		t.Fatal(j, err)
	}
	a.processUserBackup(ctx, j)
	j, err = a.store.UserBackupJob(ctx, 1, j.ID)
	if err != nil || j.State != "preview" {
		t.Fatal("upload did not produce review", j, err)
	}
	if !strings.Contains(lastScreen(t, f).Params.Get("reply_markup"), "Confirm Restore") {
		t.Fatal("restore confirmation absent")
	}
	var imports int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM number_import_jobs`).Scan(&imports); err != nil || imports != 0 {
		t.Fatal("backup interpreted as inventory", imports, err)
	}
	testCallback(a, 1, fmt.Sprintf("admin:backups:confirm:%d", j.ID))
	claimed, err := a.store.ClaimUserBackup(ctx)
	if err != nil || claimed.Phase != "restore" {
		t.Fatal(claimed, err)
	}
	a.processUserBackup(ctx, claimed)
	j, err = a.store.UserBackupJob(ctx, 1, j.ID)
	if err != nil || j.State != "succeeded" {
		t.Fatal(j, err)
	}
	testCallback(a, 1, fmt.Sprintf("admin:backups:confirm:%d", j.ID))
	var restores int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_backup_restores`).Scan(&restores); err != nil || restores != 1 {
		t.Fatal("confirmation repeated restore", restores, err)
	}
	commands, err := a.commandMenu(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backupusers", "restoreusers"} {
		found := false
		for _, c := range commands {
			if c.Command == name {
				found = true
			}
		}
		if !found {
			t.Fatal("backup command absent", name)
		}
	}
}

func TestUserBackupExportSendsEncryptedDocument(t *testing.T) {
	a, f, _ := newIntegrationApp(t)
	ctx := context.Background()
	j, err := a.store.CreateUserBackupJob(ctx, 1, 1, "export", "export-fixture")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := a.store.ClaimUserBackup(ctx)
	if err != nil || claimed.ID != j.ID {
		t.Fatal(claimed, err)
	}
	a.processUserBackup(ctx, claimed)
	job, err := a.store.UserBackupJob(ctx, 1, j.ID)
	if err != nil || job.State != "succeeded" || job.BackupID == "" {
		t.Fatal("backup export did not complete", job, err)
	}
	files, err := a.store.UserBackupFiles(ctx, 1, j.ID)
	if err != nil || len(files) != 1 || strings.Contains(string(files[0]), "balance_pkr") {
		t.Fatal("encrypted backup part unavailable", err)
	}
	found := false
	for _, call := range f.snapshot() {
		if call.Method == "sendDocument" {
			found = true
		}
	}
	if !found {
		t.Fatal("backup document was not sent")
	}
}
func TestChildUsesMainTemplateAndQueuesItsOwnCredentials(t *testing.T) {
	a, _, pool := newIntegrationApp(t)
	ctx := context.Background()
	child, err := a.store.CreateChildBotRequest(ctx, 22, "Child", "fixture-child-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.ApproveChildBot(ctx, child, 1, "pro"); err != nil {
		t.Fatal(err)
	}
	source, err := a.store.SavePanelSource(ctx, 1, 0, "Shared Login", "login", "http://panel.example/ints")
	if err != nil {
		t.Fatal(err)
	}
	app := NewForInstance(a.bot, a.store, child, false, time.Minute, time.UTC)
	testCallback(app, 22, fmt.Sprintf("admin:source:use:%d", source))
	testText(app, 22, "Own account")
	testText(app, 22, "child@example.test")
	testText(app, 22, "fixture-password")
	flow, err := a.store.TelegramFlow(ctx, child, 22)
	if err != nil {
		t.Fatal(err)
	}
	testCallback(app, 22, "admin:source:accountsave:"+flow.Data["nonce"])
	var tenant, accountSource int64
	var enabled bool
	if err = pool.QueryRow(ctx, `SELECT p.bot_instance_id,p.source_id,p.enabled FROM panels p WHERE p.bot_instance_id=$1`, child).Scan(&tenant, &accountSource, &enabled); err != nil || tenant != child || accountSource == source || enabled {
		t.Fatal("child account not isolated/queued", err)
	}
	var queued int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM panel_connection_tests`).Scan(&queued); err != nil || queued != 1 {
		t.Fatal("connection test ran in handler", queued, err)
	}
	commands, err := app.commandMenu(ctx, 22)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range commands {
		if c.Command == "backupusers" || c.Command == "restoreusers" {
			t.Fatal("main-admin backup exposed to child")
		}
	}
}

type blockingBulkClient struct {
	base             tgbotapi.HTTPClient
	started, release chan struct{}
	once             sync.Once
}

func (c *blockingBulkClient) Do(req *http.Request) (*http.Response, error) {
	if req.GetBody != nil {
		body, _ := req.GetBody()
		copy := req.Clone(req.Context())
		copy.Body = body
		_ = copy.ParseForm()
		body.Close()
		if copy.Form.Get("text") == "Bulk fixture" {
			c.once.Do(func() { close(c.started) })
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-c.release:
			}
		}
	}
	return c.base.Do(req)
}

type fixtureBroadcastBots struct{ bot *tgbotapi.BotAPI }

func (p fixtureBroadcastBots) Bot(id int64) (*tgbotapi.BotAPI, bool) { return p.bot, id == 1 }
func TestLargeBroadcastDoesNotBlockHelpOrCancellation(t *testing.T) {
	a, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id) SELECT generate_series(1000,10999);INSERT INTO bot_instance_users(bot_instance_id,user_id) SELECT 1,generate_series(1000,10999)`); err != nil {
		t.Fatal(err)
	}
	slow := &blockingBulkClient{base: a.bot.Client, started: make(chan struct{}), release: make(chan struct{})}
	a.bot.Client = tgtransport.New(slow, 25)
	id, err := a.store.CreateBroadcast(ctx, store.Broadcast{Instance: 1, Creator: 1, Kind: "text", Body: "Bulk fixture", Entities: json.RawMessage(`[]`), Audience: "all"}, "responsive")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); broadcast.Run(runCtx, a.store, fixtureBroadcastBots{a.bot}) }()
	defer func() { cancel(); <-done }()
	select {
	case <-slow.started:
	case <-time.After(8 * time.Second):
		t.Fatal("broadcast did not start")
	}
	f.reset()
	help := make(chan struct{})
	go func() { testText(a, 2, "/help"); close(help) }()
	select {
	case <-help:
	case <-time.After(2 * time.Second):
		t.Fatal("help blocked behind bulk send")
	}
	if !strings.Contains(strings.ToLower(lastScreen(t, f).Params.Get("text")), "help") {
		t.Fatal("help response missing")
	}
	bounded, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if err = a.store.CancelBroadcast(bounded, 1, id); err != nil {
		t.Fatal("cancellation blocked", err)
	}
	status, err := a.store.BroadcastStatus(ctx, 1, id)
	if err != nil || status.State != "cancelled" || status.Skipped < 10000 {
		t.Fatal("cancel failed", status, err)
	}
	close(slow.release)
}

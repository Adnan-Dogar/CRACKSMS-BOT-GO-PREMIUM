package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
	"time"
)

func TestPanelSourceAccountsIntegration(t *testing.T) {
	a, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	testCallback(a, 1, "admin:panel:add")
	testText(a, 1, "IVAS Panel")
	testCallback(a, 1, "admin:panel:kind:ivas")
	testText(a, 1, "https://www.ivasms.com/portal/live/my_sms")
	flow, e := a.store.TelegramFlow(ctx, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	testCallback(a, 1, "admin:source:save:stale")
	sources, _ := a.store.PanelSources(ctx, 1)
	if len(sources) != 0 {
		t.Fatal("stale confirmation saved panel")
	}
	testCallback(a, 1, "admin:source:save:"+flow.Data["nonce"])
	sources, e = a.store.PanelSources(ctx, 1)
	if e != nil || len(sources) != 1 || sources[0].Accounts != 0 {
		t.Fatalf("sources=%v err=%v", sources, e)
	}
	for _, label := range []string{"Personal", "Second account"} {
		id, e := a.store.SavePanelAccount(ctx, 1, sources[0].ID, 0, label, map[string]any{"username": "fixture@example.test", "password": "fixture-password"}, false)
		if e != nil {
			t.Fatal(e)
		}
		var enabled bool
		var raw string
		if e = pool.QueryRow(ctx, `SELECT enabled,config::text FROM panels WHERE id=$1`, id).Scan(&enabled, &raw); e != nil {
			t.Fatal(e)
		}
		if enabled || strings.Contains(raw, "fixture-password") {
			t.Fatal("pending account enabled or plaintext credential stored")
		}
	}
	accounts, e := a.store.SourceAccounts(ctx, 1, sources[0].ID)
	if e != nil || len(accounts) != 2 {
		t.Fatalf("accounts=%v err=%v", accounts, e)
	}
	f.reset()
	a.showPanelSource(ctx, 1, sources[0].ID)
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "authenticated Socket.IO") {
		t.Fatal("pending access explanation missing")
	}
}
func TestBroadcastQueueIntegration(t *testing.T) {
	a, _, pool := newIntegrationApp(t)
	ctx := context.Background()
	b := store.Broadcast{Instance: 1, Creator: 1, Kind: "photo", FileID: "fixture-photo", Body: "Caption", Entities: json.RawMessage(`[{"type":"bold","offset":0,"length":7}]`), Audience: "all"}
	id, e := a.store.CreateBroadcast(ctx, b, "one-confirmation")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.store.EnsureUserForInstance(ctx, 1, 4, "four", "Four", ""); e != nil {
		t.Fatal(e)
	}
	same, e := a.store.CreateBroadcast(ctx, b, "one-confirmation")
	if e != nil || same != id {
		t.Fatal("confirmation is not idempotent", e)
	}
	status, e := a.store.BroadcastStatus(ctx, 1, id)
	if e != nil || status.Total != 0 || status.State != "preparing" {
		t.Fatalf("audience changed: %+v %v", status, e)
	}
	for {
		more, e := a.store.PrepareBroadcastBatch(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if !more {
			break
		}
	}
	status, e = a.store.BroadcastStatus(ctx, 1, id)
	if e != nil || status.Total != 3 {
		t.Fatal("audience snapshot changed", status, e)
	}
	job, e := a.store.ClaimBroadcast(ctx)
	if e != nil || job.Kind != "photo" || job.FileID != "fixture-photo" {
		t.Fatal("claim failed", e)
	}
	if e = a.store.FinishBroadcastRecipient(ctx, job, "pending", time.Second); e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `UPDATE broadcast_recipients SET state='sending',claimed_at=now()-interval '10 minutes' WHERE broadcast_id=$1 AND user_id=$2`, job.ID, job.Recipient)
	if e != nil {
		t.Fatal(e)
	}
	job2, e := a.store.ClaimBroadcast(ctx)
	if e != nil || job2.Recipient == job.Recipient {
		t.Fatal("interrupted recipient retried", e)
	}
	_ = a.store.FinishBroadcastRecipient(ctx, job2, "sent", 0)
	if e = a.store.CancelBroadcast(ctx, 1, id); e != nil {
		t.Fatal(e)
	}
	_, e = a.store.ClaimBroadcast(ctx)
	if !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("cancelled broadcast claimed", e)
	}
	status, e = a.store.BroadcastStatus(ctx, 1, id)
	if e != nil || status.Uncertain != 1 || status.Sent != 1 || status.Skipped != 1 {
		t.Fatalf("bad final counts: %+v %v", status, e)
	}
}
func TestBroadcastPreviewAndSettingsIntegration(t *testing.T) {
	a, f, _ := newIntegrationApp(t)
	ctx := context.Background()
	testCallback(a, 1, "admin:broadcast")
	a.handleUpdate(ctx, tgbotapi.Update{Message: &tgbotapi.Message{MessageID: 43, Text: "Hello", From: &tgbotapi.User{ID: 1}, Chat: &tgbotapi.Chat{ID: 1, Type: "private"}, Entities: []tgbotapi.MessageEntity{{Type: "bold", Offset: 0, Length: 5}}}})
	flow, e := a.store.TelegramFlow(ctx, 1, 1)
	if e != nil || flow.Step != "audience" {
		t.Fatal("preview did not advance", e)
	}
	testCallback(a, 1, "admin:broadcast:audience:"+flow.Data["nonce"]+":free")
	testCallback(a, 1, "admin:broadcast:confirm:"+flow.Data["nonce"])
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "Broadcast #") {
		t.Fatal("broadcast status missing")
	}
	testCallback(a, 1, "prefs:mode:full")
	pref, e := a.store.Preference(ctx, 1, 1)
	if e != nil || pref.CompactMenu {
		t.Fatal("menu preference not saved", e)
	}
	testCallback(a, 1, "prefs:timezone")
	testText(a, 1, "Asia/Karachi")
	pref, e = a.store.Preference(ctx, 1, 1)
	if e != nil || pref.Timezone != "Asia/Karachi" {
		t.Fatal("timezone not saved", e)
	}
	for _, route := range []string{"stats:7", "stats:30", "stats:all", "admin:stats:7", "help:otp", "admin:bots:filter:running:0"} {
		f.reset()
		testCallback(a, 1, route)
		if strings.Contains(lastScreen(t, f).Params.Get("text"), "could not be completed") {
			t.Fatal("route failed", route)
		}
	}
}

func TestPeriodStatisticsScopeAndEarnings(t *testing.T) {
	a, _, pool := newIntegrationApp(t)
	ctx := context.Background()
	_, e := pool.Exec(ctx, `INSERT INTO otp_events(id,dedup_key,phone,normalized_phone,message,bot_instance_id,assigned_user_id,counted,received_at) VALUES('10000000-0000-0000-0000-000000000001','recent','123','123','fixture',1,2,true,now()),('10000000-0000-0000-0000-000000000002','old','123','123','fixture',1,2,true,now()-interval '45 days'),('10000000-0000-0000-0000-000000000003','other-user','123','123','fixture',1,3,true,now());INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id) VALUES(2,'PKR',12,'otp_earning','otp_event','10000000-0000-0000-0000-000000000001'),(2,'USD',0.5,'otp_earning','otp_event','10000000-0000-0000-0000-000000000001'),(2,'PKR',30,'otp_earning','otp_event','10000000-0000-0000-0000-000000000002')`)
	if e != nil {
		t.Fatal(e)
	}
	since := time.Now().Add(-7 * 24 * time.Hour)
	stats, e := a.store.PeriodStatistics(ctx, 1, 2, &since)
	if e != nil || stats.Events != 1 || stats.Counted != 1 || stats.BasePKR != 12 || stats.BaseUSD != 0.5 {
		t.Fatalf("wrong period/user stats: %+v %v", stats, e)
	}
	all, e := a.store.PeriodStatistics(ctx, 1, 2, nil)
	if e != nil || all.Events != 2 || all.BasePKR != 42 {
		t.Fatalf("wrong lifetime stats: %+v %v", all, e)
	}
}

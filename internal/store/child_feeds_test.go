package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

func TestChildTemplatesKeepAccountsSeparateAndSurviveSourceDeletion(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	importOwner(t, repo)
	child, err := repo.CreateChildBotRequest(ctx, 22, "Child", "fixture-child-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApproveChildBot(ctx, child, largeImportAdmin, "pro"); err != nil {
		t.Fatal(err)
	}
	source, err := repo.SavePanelSource(ctx, 1, 0, "Shared Login", "login", "http://panel.example/ints")
	if err != nil {
		t.Fatal(err)
	}
	mainAccount, err := repo.SavePanelAccount(ctx, 1, source, 0, "Main", map[string]any{"username": "main", "password": "main-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	list, err := repo.PanelSources(ctx, child)
	if err != nil || len(list) != 1 || !list[0].Shared || list[0].Accounts != 0 {
		t.Fatal("child did not see credential-free template", list, err)
	}
	if _, err = repo.SavePanelSource(ctx, child, source, "tampered", "login", "http://other.example"); err == nil {
		t.Fatal("child edited main template")
	}
	id, err := repo.SavePanelAccount(ctx, child, source, 0, "Own account", map[string]any{"username": "child", "password": "child-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := repo.UsePanelTemplate(ctx, child, source)
	if err != nil || clone == source {
		t.Fatal(clone, err)
	}
	own, err := repo.PanelSources(ctx, child)
	if err != nil || len(own) != 1 || own[0].Shared || own[0].ID != clone || own[0].Accounts != 1 {
		t.Fatal(own, err)
	}
	config, err := repo.PanelForInstance(ctx, child, id)
	if err != nil || config.Config["username"] != "child" {
		t.Fatal(config, err)
	}
	main, err := repo.PanelForInstance(ctx, 1, mainAccount)
	if err != nil || main.Config["username"] != "main" {
		t.Fatal("main credentials changed", err)
	}
	if _, err = repo.PanelForInstance(ctx, child, mainAccount); err == nil {
		t.Fatal("main account exposed to child")
	}
	if err = repo.DeletePanelSource(ctx, 1, source); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.PanelForInstance(ctx, child, id); err != nil {
		t.Fatal("main deletion removed child account", err)
	}
	var origin *int64
	if err = pool.QueryRow(ctx, `SELECT origin_source_id FROM panel_sources WHERE id=$1`, clone).Scan(&origin); err != nil || origin != nil {
		t.Fatal("deleted origin retained", err)
	}
}
func TestMainOTPSharingIsOptInAndAssignmentScoped(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	importOwner(t, repo)
	child, err := repo.CreateChildBotRequest(ctx, 22, "Child", "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApproveChildBot(ctx, child, largeImportAdmin, "pro"); err != nil {
		t.Fatal(err)
	}
	source, _ := repo.SavePanelSource(ctx, 1, 0, "Provider", "token_api", "https://panel.example/crapi")
	panel, err := repo.SavePanelAccount(ctx, 1, source, 0, "Main", map[string]any{"token": "main-secret"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AddNumbers(ctx, "WhatsApp", "United States", "US", 1, 0, 1, []string{"+12025550123", "+12025550124"}); err != nil {
		t.Fatal(err)
	}
	assignment, err := repo.AssignNumbersForInstance(ctx, child, 22, "WhatsApp", "United States", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.OTPEvent{BotInstanceID: 1, PanelID: panel, PanelName: "Main", Phone: assignment.Numbers[0].Phone, NormalizedPhone: assignment.Numbers[0].NormalizedPhone, Service: "WhatsApp", Message: "Code 123456", Code: "123456", ReceivedAt: time.Now(), DedupKey: "disabled"}
	if _, err = repo.AcceptOTP(ctx, event); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM panel_ingest_jobs WHERE shared_from_event_id IS NOT NULL`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if count() != 0 {
		t.Fatal("OTP leaked before enable")
	}
	if err = repo.SetChildOTPSharing(ctx, 22, child, true); err == nil {
		t.Fatal("child owner could enable main feed")
	}
	if err = repo.SetChildOTPSharing(ctx, largeImportAdmin, child, true); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AcceptOTP(ctx, event); err != nil || count() != 0 {
		t.Fatal("enabling replayed history", err)
	}
	old := time.Now().Add(-time.Hour)
	event.ProviderTimestamp = &old
	event.DedupKey = "historical"
	event.ReceivedAt = time.Now()
	if _, err = repo.AcceptOTP(ctx, event); err != nil || count() != 0 {
		t.Fatal("old provider record shared", err)
	}
	event.ProviderTimestamp = nil
	for i, service := range []string{"Telegram", "Unknown", ""} {
		event.Service = service
		event.DedupKey = fmt.Sprint("wrong-", i)
		if _, err = repo.AcceptOTP(ctx, event); err != nil || count() != 0 {
			t.Fatal("wrong service shared", err)
		}
	}
	event.Service = "WhatsApp"
	event.DedupKey = "enabled"
	event.ReceivedAt = time.Now()
	if _, err = repo.AcceptOTP(ctx, event); err != nil || count() != 1 {
		t.Fatal("matching event not queued", count(), err)
	}
	copy, err := repo.ClaimIngestJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if copy.Event.BotInstanceID != child || copy.Event.SharedFromEventID == "" || copy.Event.PanelID != 0 {
		t.Fatal("copied source/tenant incorrect", copy.Event)
	}
	result, err := repo.AcceptOTP(ctx, copy.Event)
	if err != nil || !result.Counted || result.AssignedUserID != 22 {
		t.Fatal("child assignment not credited", result, err)
	}
	if err = repo.CompleteIngestJob(ctx, copy, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AcceptOTP(ctx, event); err != nil || count() != 0 {
		t.Fatal("main duplicate shared twice", err)
	}
	if again, err := repo.AcceptOTP(ctx, copy.Event); err != nil || !again.Duplicate {
		t.Fatal("child copy credited twice", err)
	}
	if err = repo.SetChildOTPSharing(ctx, largeImportAdmin, child, false); err != nil {
		t.Fatal(err)
	}
	if allowed, err := repo.SharedDeliveryAllowed(ctx, result.EventID); err != nil || allowed {
		t.Fatal("queued delivery ignored disabled sharing", err)
	}
	// A copy claimed before disable cannot accept itself after disable.
	if _, err = repo.AssignNumbersForInstance(ctx, child, 22, "WhatsApp", "United States", 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetChildOTPSharing(ctx, largeImportAdmin, child, true); err != nil {
		t.Fatal(err)
	}
	event.Phone = "+12025550124"
	event.NormalizedPhone = store.NormalizePhone(event.Phone)
	event.DedupKey = "disable-in-flight"
	event.ReceivedAt = time.Now()
	if _, err = repo.AcceptOTP(ctx, event); err != nil {
		t.Fatal(err)
	}
	job, err := repo.ClaimIngestJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetChildOTPSharing(ctx, largeImportAdmin, child, false); err != nil {
		t.Fatal(err)
	}
	if r, err := repo.AcceptOTP(ctx, job.Event); err != nil || !r.Duplicate {
		t.Fatal("disabled claimed copy was accepted", r, err)
	}
	_, _, total, err := repo.UserBalance(ctx, 22)
	if err != nil || total != 1 {
		t.Fatal("child credit repeated", total, err)
	}
}

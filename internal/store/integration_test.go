package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/db"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/secure"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRewardConsumptionAndRecyclingScenario(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer adminPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cipher, _ := secure.NewCipher(bytes.Repeat([]byte{1}, 32))
	repo := store.New(pool, time.FixedZone("PKT", 5*60*60), cipher)
	t.Run("premium tenant features", func(t *testing.T) {
		if err := repo.EnsureUser(ctx, 303, "owner", "Owner", ""); err != nil {
			t.Fatal(err)
		}
		const testChildToken = "integration-test-child-bot-token"
		childID, err := repo.CreateChildBotRequest(ctx, 303, "Tenant Bot", testChildToken)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.ApproveChildBot(ctx, childID, 303, "enterprise"); err != nil {
			t.Fatal(err)
		}
		if token, err := repo.ChildBotToken(ctx, childID); err != nil || token != testChildToken {
			t.Fatalf("decrypted child token mismatch err=%v", err)
		}
		if err := repo.EnsureUserForInstance(ctx, childID, 404, "premium", "Premium", ""); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetUserTier(ctx, childID, 404, "enterprise", nil, 303); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetUserTheme(ctx, childID, 404, 3); err != nil {
			t.Fatal(err)
		}
		groupTheme := 5
		if err := repo.UpsertOTPGroupForInstance(ctx, childID, domain.OTPGroupDestination{
			ChatID: -100404, Title: "Private Log", ButtonsEnabled: true, Enabled: true,
			OTPVisibility: "hidden", ThemeID: &groupTheme,
		}, 303); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.AddNumbers(ctx, "TenantService", "TenantCountry", "TC", 2, 0, 1, []string{"923119999999"}); err != nil {
			t.Fatal(err)
		}
		assignment, err := repo.AssignNumbersForInstance(ctx, childID, 404, "TenantService", "TenantCountry", 1, 20*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		webhookID, webhookSecret, err := repo.CreateWebhook(ctx, childID, 404, "https://example.com/otp", []string{"otp.received"})
		if err != nil || webhookID == 0 || webhookSecret == "" {
			t.Fatalf("webhook id=%d secret=%q err=%v", webhookID, webhookSecret, err)
		}
		message := "Tenant code is 654321"
		accepted, err := repo.AcceptOTP(ctx, domain.OTPEvent{BotInstanceID: childID, PanelName: "tenant-panel",
			Phone: assignment.Numbers[0].Phone, NormalizedPhone: assignment.Numbers[0].NormalizedPhone,
			Service: "TenantService", Message: message, Code: "654321",
			DedupKey: store.DedupKey(fmt.Sprint(childID), assignment.Numbers[0].NormalizedPhone, message)})
		if err != nil || !accepted.Counted {
			t.Fatalf("tenant OTP accepted=%+v err=%v", accepted, err)
		}
		firstJob, err := repo.ClaimDeliveryJob(ctx)
		if err != nil || firstJob.BotInstanceID != childID || firstJob.TargetKind != "user" || firstJob.ThemeID != 3 || firstJob.OTPVisibility != "visible" {
			t.Fatalf("tenant user delivery=%+v err=%v", firstJob, err)
		}
		if err := repo.CompleteDeliveryJob(ctx, firstJob, nil, 0, false); err != nil {
			t.Fatal(err)
		}
		secondJob, err := repo.ClaimDeliveryJob(ctx)
		if err != nil || secondJob.BotInstanceID != childID || secondJob.TargetKind != "group" || secondJob.ThemeID != 5 || secondJob.OTPVisibility != "hidden" {
			t.Fatalf("tenant group delivery=%+v err=%v", secondJob, err)
		}
		if err := repo.CompleteDeliveryJob(ctx, secondJob, nil, 0, false); err != nil {
			t.Fatal(err)
		}
		webhookDelivery, err := repo.ClaimWebhookDelivery(ctx)
		if err != nil || webhookDelivery.Endpoint.Secret != webhookSecret || webhookDelivery.Event.BotInstanceID != childID {
			t.Fatalf("webhook delivery=%+v err=%v", webhookDelivery, err)
		}
		if err := repo.CompleteWebhookDelivery(ctx, webhookDelivery, 204, nil); err != nil {
			t.Fatal(err)
		}
		apiKey, rawKey, err := repo.CreateAPIKey(ctx, childID, 404, "test", nil, nil)
		if err != nil || apiKey.ID == 0 || rawKey == "" {
			t.Fatalf("api key=%+v err=%v", apiKey, err)
		}
		authenticated, err := repo.AuthenticateAPIKey(ctx, rawKey)
		if err != nil || authenticated.UserID != 404 {
			t.Fatalf("authenticated key=%+v err=%v", authenticated, err)
		}
		scheduledID, err := repo.CreateScheduledMessage(ctx, domain.ScheduledMessage{BotInstanceID: childID,
			CreatorUserID: 404, TargetKind: "user", TargetID: ptrInt64(404), Body: "Scheduled", DeliverAt: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE scheduled_messages SET deliver_at=now()-interval '1 second' WHERE id=$1`, scheduledID); err != nil {
			t.Fatal(err)
		}
		scheduled, err := repo.ClaimScheduledMessage(ctx)
		if err != nil || scheduled.ID != scheduledID {
			t.Fatalf("scheduled=%+v err=%v", scheduled, err)
		}
		if err := repo.CompleteScheduledMessage(ctx, scheduled, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.AddCustomOTPPattern(ctx, childID, 303, "tenant", `ticket=([0-9]{6})`); err != nil {
			t.Fatal(err)
		}
		patterns, err := repo.ListCustomOTPPatterns(ctx, childID)
		if err != nil || len(patterns) != 1 {
			t.Fatalf("patterns=%v err=%v", patterns, err)
		}
		admins, err := repo.ListInstanceAdmins(ctx, childID)
		if err != nil || len(admins) != 1 || admins[0].UserID != 303 {
			t.Fatalf("tenant admins=%+v err=%v", admins, err)
		}
		users, err := repo.ListUsersForInstance(ctx, childID, 25)
		if err != nil || len(users) != 1 || users[0].UserID != 404 || users[0].Tier != "enterprise" {
			t.Fatalf("tenant users=%+v err=%v", users, err)
		}
		panelID, err := repo.UpsertPanelForInstance(ctx, childID, domain.Panel{
			Name: "inline-panel", Kind: "token_api", PollInterval: time.Second, Enabled: true,
			Config: map[string]any{"url": "https://example.invalid/sms"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.RemovePanelForInstance(ctx, childID, panelID); err != nil {
			t.Fatal(err)
		}
		if report, err := repo.PanelHealthReportForInstance(ctx, childID); err != nil || len(report) != 0 {
			t.Fatalf("removed panel report=%+v err=%v", report, err)
		}
		withdrawalID, err := repo.CreateWithdrawalForInstance(ctx, childID, 404, "PKR", 1, 0, "integration")
		if err != nil {
			t.Fatal(err)
		}
		withdrawals, err := repo.ListPendingWithdrawals(ctx, childID, 25)
		if err != nil || len(withdrawals) != 1 || withdrawals[0].ID != withdrawalID {
			t.Fatalf("pending withdrawals=%+v err=%v", withdrawals, err)
		}
		if err := repo.ResolveWithdrawalForInstance(ctx, childID, withdrawalID, 303, false); err != nil {
			t.Fatal(err)
		}
		if err := repo.RejectChildBot(ctx, childID, 303, "integration reversal"); err != nil {
			t.Fatal(err)
		}
		if err := repo.ApproveChildBot(ctx, childID, 303, "enterprise"); err != nil {
			t.Fatalf("reapprove rejected child bot: %v", err)
		}
	})
	t.Run("durable panel ingestion and replay dedup", func(t *testing.T) {
		panelID, err := repo.UpsertPanel(ctx, domain.Panel{
			Name: "queue-test", Kind: "token_api", PollInterval: time.Second, Enabled: true,
			Config: map[string]any{"url": "https://example.invalid/sms"},
		})
		if err != nil {
			t.Fatal(err)
		}
		event := domain.OTPEvent{
			PanelID: panelID, PanelName: "queue-test", Phone: "923009999999",
			NormalizedPhone: "923009999999", Service: "Test", Message: "Your code is 123456",
			Code: "123456", DedupKey: store.DedupKey("queue-test", "923009999999", "record-1|Your code is 123456"),
			ReceivedAt: time.Now(),
		}
		if err := repo.EnqueuePanelEvents(ctx, panelID, []domain.OTPEvent{event}, "cursor-1"); err != nil {
			t.Fatal(err)
		}
		job, err := repo.ClaimIngestJob(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.AcceptOTP(ctx, job.Event); err != nil {
			t.Fatal(err)
		}
		if err := repo.CompleteIngestJob(ctx, job, nil); err != nil {
			t.Fatal(err)
		}
		if err := repo.EnqueuePanelEvents(ctx, panelID, []domain.OTPEvent{event}, "cursor-2"); err != nil {
			t.Fatal(err)
		}
		pending, err := repo.PendingIngestCount(ctx)
		if err != nil || pending != 0 {
			t.Fatalf("pending ingest=%d err=%v, want 0", pending, err)
		}
		var cursor string
		if err := pool.QueryRow(ctx, `SELECT last_cursor FROM panels WHERE id=$1`, panelID).Scan(&cursor); err != nil || cursor != "cursor-2" {
			t.Fatalf("cursor=%q err=%v, want cursor-2", cursor, err)
		}
	})

	if err := repo.EnsureUser(ctx, 101, "worker", "Worker", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReplaceRewardSchedule(ctx, "global", nil, []domain.RewardRule{
		{Threshold: 30, AmountPKR: 30}, {Threshold: 100, AmountPKR: 50},
	}, 101); err != nil {
		t.Fatal(err)
	}
	phones := make([]string, 300)
	for i := range phones {
		phones[i] = fmt.Sprintf("92300%07d", i)
	}
	if added, err := repo.AddNumbers(ctx, "WhatsApp", "Pakistan", "PK", 1, 0, 300, phones); err != nil || added != 300 {
		t.Fatalf("AddNumbers added=%d err=%v", added, err)
	}
	assignment, err := repo.AssignNumbers(ctx, 101, "WhatsApp", "Pakistan", 300, 20*time.Minute)
	if err != nil || len(assignment.Numbers) != 300 {
		t.Fatalf("assignment numbers=%d err=%v", len(assignment.Numbers), err)
	}

	for i := 0; i < 100; i++ {
		number := assignment.Numbers[i]
		message := fmt.Sprintf("Your WhatsApp code is %06d", 100000+i)
		result, err := repo.AcceptOTP(ctx, domain.OTPEvent{
			PanelName: "integration", Phone: number.Phone, NormalizedPhone: number.NormalizedPhone,
			Service: "WhatsApp", Message: message, Code: fmt.Sprintf("%06d", 100000+i),
			DedupKey: store.DedupKey("integration", number.NormalizedPhone, message), ReceivedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("AcceptOTP %d: %v", i, err)
		}
		if !result.Counted {
			t.Fatalf("OTP %d was not counted", i)
		}
	}
	pkr, _, total, err := repo.UserBalance(ctx, 101)
	if err != nil {
		t.Fatal(err)
	}
	if total != 100 || pkr != 180 {
		t.Fatalf("balance=%v total=%d, want 180/100", pkr, total)
	}

	duplicateMessage := "Your WhatsApp code is 100000"
	duplicate, err := repo.AcceptOTP(ctx, domain.OTPEvent{PanelName: "integration", Phone: phones[0],
		NormalizedPhone: store.NormalizePhone(phones[0]), Message: duplicateMessage,
		DedupKey: store.DedupKey("integration", store.NormalizePhone(phones[0]), duplicateMessage)})
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE assignments SET expires_at=now()-interval '1 second' WHERE id=$1`, assignment.ID); err != nil {
		t.Fatal(err)
	}
	recycled, err := repo.RecycleExpiredAssignments(ctx, 24*time.Hour, 500)
	if err != nil {
		t.Fatal(err)
	}
	if recycled.Returned != 200 || recycled.Consumed != 100 {
		t.Fatalf("recycled=%+v, want returned=200 consumed=100", recycled)
	}
	if _, err := repo.AssignNumbers(ctx, 101, "WhatsApp", "Pakistan", 1, 20*time.Minute); !errors.Is(err, store.ErrNoNumbers) {
		t.Fatalf("previous user unexpectedly received a recycled number: %v", err)
	}
	if err := repo.EnsureUser(ctx, 202, "other", "Other", ""); err != nil {
		t.Fatal(err)
	}
	other, err := repo.AssignNumbers(ctx, 202, "WhatsApp", "Pakistan", 1, 20*time.Minute)
	if err != nil || len(other.Numbers) != 1 {
		t.Fatalf("other assignment=%+v err=%v", other, err)
	}

	var available, consumed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='available'),count(*) FILTER (WHERE state='consumed')
		FROM numbers WHERE service='WhatsApp'`).Scan(&available, &consumed); err != nil {
		t.Fatal(err)
	}
	if available != 199 || consumed != 100 {
		t.Fatalf("available=%d consumed=%d", available, consumed)
	}
	if strings.Contains(schema, "-") {
		t.Fatal("invalid schema")
	}
}

func ptrInt64(value int64) *int64 { return &value }

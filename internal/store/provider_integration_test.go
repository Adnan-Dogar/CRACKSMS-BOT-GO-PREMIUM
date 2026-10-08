package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/db"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/secure"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func providerStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("provider_store_%d", time.Now().UnixNano())
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = root.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		root.Close()
	})
	if e = db.Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	cipher, _ := secure.NewCipher(bytes.Repeat([]byte{3}, 32))
	return store.New(pool, time.UTC, cipher), pool
}

func TestSamePhoneIndependentApplicationsAndConcurrentConsumption(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	phone := "+12025550123"
	for i, app := range []string{"WhatsApp", "Telegram"} {
		if n, e := repo.AddNumbers(ctx, app, "United States", "US", float64(i+1), 0.01, 1, []string{phone}); e != nil || n != 1 {
			t.Fatal(n, e)
		}
	}
	if n, e := repo.AddNumbers(ctx, " whatsapp ", "United States", "US", 1, 0.01, 1, []string{phone}); e != nil || n != 0 {
		t.Fatal("case alias duplicated stock", n, e)
	}
	wa, e := repo.AssignNumbers(ctx, 101, "WhatsApp", "United States", 1, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	tg, e := repo.AssignNumbers(ctx, 202, "Telegram", "United States", 1, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if wa.Numbers[0].ID == tg.Numbers[0].ID {
		t.Fatal("shared inventory row across services")
	}
	old := time.Now().Add(-time.Hour)
	base := domain.OTPEvent{Phone: phone, NormalizedPhone: store.NormalizePhone(phone), Service: "WhatsApp", Message: "Code 123456", Code: "123456", ReceivedAt: time.Now()}
	for i, test := range []struct {
		service, code, status string
		timestamp             *time.Time
	}{{"", "123456", "", nil}, {"Unknown", "123456", "", nil}, {"WhatsApp", "", "", nil}, {"WhatsApp", "letters", "", nil}, {"WhatsApp", "123456", "failed", nil}, {"WhatsApp", "123456", "", &old}} {
		event := base
		event.Service = test.service
		event.Code = test.code
		event.DeliveryStatus = test.status
		event.ProviderTimestamp = test.timestamp
		event.DedupKey = fmt.Sprintf("rejected-%d", i)
		r, e := repo.AcceptOTP(ctx, event)
		if e != nil || r.Counted {
			t.Fatal("invalid/old/failed record consumed stock", r, e)
		}
	}
	base.DedupKey = "concurrent-wa"
	var counted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := repo.AcceptOTP(ctx, base)
			if e != nil {
				t.Error(e)
			}
			if r.Counted {
				counted.Add(1)
			}
		}()
	}
	wg.Wait()
	if counted.Load() != 1 {
		t.Fatal("credited concurrent replay", counted.Load())
	}
	var waState, tgState string
	if e = pool.QueryRow(ctx, `SELECT (SELECT state::text FROM numbers WHERE id=$1),(SELECT state::text FROM numbers WHERE id=$2)`, wa.Numbers[0].ID, tg.Numbers[0].ID).Scan(&waState, &tgState); e != nil || waState != "consumed" || tgState != "assigned" {
		t.Fatal("consumed another application's number", waState, tgState, e)
	}
	base.Service = "Telegram"
	base.DedupKey = "separate-tg"
	r, e := repo.AcceptOTP(ctx, base)
	if e != nil || !r.Counted || r.AssignedUserID != 202 {
		t.Fatal(r, e)
	}
	pkr, usd, total, e := repo.UserBalance(ctx, 101)
	if e != nil || pkr != 1 || usd != 0.01 || total != 1 {
		t.Fatal(pkr, usd, total, e)
	}
	pkr, _, total, e = repo.UserBalance(ctx, 202)
	if e != nil || pkr != 2 || total != 1 {
		t.Fatal(pkr, total, e)
	}
	history, e := repo.OTPHistory(ctx, 1, 101, 20, 0)
	if e != nil || len(history) != 1 || history[0].Service != "WhatsApp" {
		t.Fatal("phone-only history join duplicated/leaked rows", history, e)
	}
}

func TestServiceReleaseOwnershipAndCooldown(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	phone := "+12025550123"
	for _, app := range []string{"WhatsApp", "Telegram"} {
		_, e := repo.AddNumbers(ctx, app, "United States", "US", 1, 0, 1, []string{phone})
		if e != nil {
			t.Fatal(e)
		}
	}
	wa, e := repo.AssignNumbers(ctx, 101, "WhatsApp", "United States", 1, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	tg, e := repo.AssignNumbers(ctx, 101, "Telegram", "United States", 1, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.ReleaseNumber(ctx, 1, 202, wa.ID, wa.Numbers[0].ID, time.Hour); e == nil {
		t.Fatal("another user released number")
	}
	if e = repo.ReleaseNumber(ctx, 1, 101, tg.ID, wa.Numbers[0].ID, time.Hour); e == nil {
		t.Fatal("wrong assignment released number")
	}
	for i := 0; i < 2; i++ {
		if e = repo.ReleaseNumber(ctx, 1, 101, wa.ID, wa.Numbers[0].ID, time.Hour); e != nil {
			t.Fatal(e)
		}
	}
	var state string
	if e = pool.QueryRow(ctx, `SELECT state::text FROM numbers WHERE id=$1`, tg.Numbers[0].ID).Scan(&state); e != nil || state != "assigned" {
		t.Fatal("release touched another app", state, e)
	}
	if _, e = repo.AssignNumbers(ctx, 101, "WhatsApp", "United States", 1, time.Hour); !errors.Is(e, store.ErrNoNumbers) {
		t.Fatal("cooldown ignored", e)
	}
	if _, e = repo.AssignNumbers(ctx, 202, "WhatsApp", "United States", 1, time.Hour); e != nil {
		t.Fatal("release did not replenish stock", e)
	}
}

func TestUnmappedReplayPreservesTimestampAndSharedDatabaseBudget(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	_, e := repo.AddNumbers(ctx, "WhatsApp", "United States", "US", 1, 0, 1, []string{"12025550123"})
	if e != nil {
		t.Fatal(e)
	}
	panel, e := repo.UpsertPanel(ctx, domain.Panel{Name: "Provider", Kind: "augestel", Config: map[string]any{"token": "fixture"}, PollInterval: 30 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	timestamp := time.Now().Add(-time.Hour)
	event := domain.OTPEvent{PanelID: panel, BotInstanceID: 1, NormalizedPhone: "12025550123", Service: "WA_ALIAS", Sender: "WA_ALIAS", Message: "Your code is 123456", Code: "123456", DedupKey: "held-fixture", ProviderTimestamp: &timestamp}
	if ok, e := repo.ResolveEventService(ctx, &event); e != nil || ok {
		t.Fatal("unknown service guessed", ok, e)
	}
	if e = repo.HoldUnmappedEvent(ctx, event); e != nil {
		t.Fatal(e)
	}
	if e = repo.SaveServiceMapping(ctx, 1, panel, store.ServiceMapping{Kind: "sender", Value: "WA_ALIAS", Service: "WhatsApp"}); e != nil {
		t.Fatal(e)
	}
	if n, e := repo.RetryUnmapped(ctx, 1, panel); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	job, e := repo.ClaimIngestJob(ctx)
	if e != nil || job.Event.Service != "WhatsApp" || job.Event.ProviderTimestamp == nil || !job.Event.ProviderTimestamp.Equal(timestamp) {
		t.Fatal("held replay timestamp changed", job, e)
	}
	if e = repo.EnqueuePanelEvents(ctx, panel, nil, `{"page":1,"last_page":8,"done":true}`); e != nil {
		t.Fatal(e)
	}
	var backlog int
	if e = pool.QueryRow(ctx, `SELECT backlog_pages FROM panels WHERE id=$1`, panel).Scan(&backlog); e != nil || backlog != 0 {
		t.Fatal(backlog, e)
	}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			at, e := repo.TakeProviderRequest(ctx, "fixture-key", 5)
			if e != nil {
				t.Error(e)
			} else if at.IsZero() {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 5 {
		t.Fatal("shared DB budget race", allowed.Load())
	}
	pause := time.Now().Add(2 * time.Minute).Truncate(time.Microsecond)
	if e = repo.UpdateProviderBudget(ctx, "fixture-key", 1, pause); e != nil {
		t.Fatal(e)
	}
	at, e := repo.TakeProviderRequest(ctx, "fixture-key", 5)
	if e != nil || at.Before(pause) {
		t.Fatal("provider lower limit/retry ignored", at, e)
	}
}

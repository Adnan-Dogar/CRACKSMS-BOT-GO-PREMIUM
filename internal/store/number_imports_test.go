package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

const largeImportAdmin int64 = 5000000123

func importOwner(t *testing.T, repo *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := repo.EnsureUserForInstance(ctx, 1, largeImportAdmin, "fixture", "Import Admin", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddInstanceAdmin(ctx, 1, largeImportAdmin, []string{"*"}); err != nil {
		t.Fatal(err)
	}
}
func twoAppSettings() store.ImportSettings {
	return store.ImportSettings{Services: []store.ImportService{{Name: "WhatsApp", EmojiID: "5334998226636390258", PricePKR: 1, PerCycle: 3}, {Name: "Telegram", EmojiID: "5368324170671202286", PricePKR: 1, PerCycle: 3}}}
}
func TestLargeImportPublishesAllAppsAndUses64BitIDs(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	importOwner(t, repo)
	if err := repo.UpsertServiceProfile(ctx, 1, largeImportAdmin, "WhatsApp", "5334998226636390258"); err != nil {
		t.Fatal("large Telegram creator ID rejected", err)
	}
	if err := repo.Audit(ctx, 1, largeImportAdmin, "import.fixture", "import", "fixture", map[string]any{}); err != nil {
		t.Fatal("large audit actor ID rejected", err)
	}
	numbers := make([]store.ImportNumber, 10000)
	for i := range numbers {
		numbers[i] = store.ImportNumber{Phone: fmt.Sprintf("+1202%07d", i), Country: "United States", CountryCode: "US"}
	}
	draft, err := repo.CreateImportDraft(ctx, 1, largeImportAdmin, largeImportAdmin, "large-import", "allocation.txt", numbers, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted string
	if err = pool.QueryRow(ctx, `SELECT input_config::text FROM number_import_jobs WHERE id=$1`, draft.ID).Scan(&encrypted); err != nil || strings.Contains(encrypted, numbers[0].Phone) {
		t.Fatal("input was not encrypted", err)
	}
	loaded, err := repo.ImportNumbers(ctx, draft)
	if err != nil || len(loaded) != 10000 {
		t.Fatal("stored input lost", len(loaded), err)
	}
	queued, err := repo.QueueImport(ctx, 1, largeImportAdmin, draft.ID, twoAppSettings(), store.TelegramFlow{})
	if err != nil {
		t.Fatal(err)
	}
	again, err := repo.QueueImport(ctx, 1, largeImportAdmin, draft.ID, twoAppSettings(), store.TelegramFlow{})
	if err != nil || again.ID != queued.ID {
		t.Fatal("double confirmation duplicated job", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM numbers`).Scan(&count); err != nil || count != 0 {
		t.Fatal("queued stock became available", count, err)
	}
	claimed, err := repo.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := repo.ExecuteImport(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Published 20,000 app entries from 10,000 phones in %s", time.Since(started))
	if len(result) != 2 || result[0].Added != 10000 || result[1].Added != 10000 {
		t.Fatal("incorrect app counts", result)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM numbers WHERE state='available'`).Scan(&count); err != nil || count != 20000 {
		t.Fatal("missing stock", count, err)
	}
	done, err := repo.ImportJob(ctx, 1, largeImportAdmin, queued.ID)
	if err != nil || done.State != "succeeded" {
		t.Fatal(done.State, err)
	}
	if _, err = repo.ExecuteImport(ctx, claimed); err == nil {
		t.Fatal("completed lease executed twice")
	}
	if _, err = repo.ImportJob(ctx, 1, 1, queued.ID); err == nil {
		t.Fatal("another user accessed import")
	}
}

func TestTenThousandPhonesAcrossTwentyApps(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	importOwner(t, repo)
	numbers := make([]store.ImportNumber, 10000)
	for i := range numbers {
		numbers[i] = store.ImportNumber{Phone: fmt.Sprintf("+1203%07d", i), Country: "United States", CountryCode: "US"}
	}
	settings := store.ImportSettings{}
	for i := 0; i < 20; i++ {
		settings.Services = append(settings.Services, store.ImportService{Name: fmt.Sprintf("Capacity App %02d", i), PricePKR: 1, PerCycle: 3})
	}
	draft, err := repo.CreateImportDraft(ctx, 1, largeImportAdmin, largeImportAdmin, "capacity", "capacity.txt", numbers, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.QueueImport(ctx, 1, largeImportAdmin, draft.ID, settings, store.TelegramFlow{}); err != nil {
		t.Fatal(err)
	}
	job, err := repo.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	results, err := repo.ExecuteImport(ctx, job)
	if err != nil || len(results) != 20 {
		t.Fatal("capacity import failed", len(results), err)
	}
	t.Logf("Published 200,000 app entries in %s", time.Since(started))
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM numbers`).Scan(&count); err != nil || count != 200000 {
		t.Fatal("capacity inventory incomplete", count, err)
	}
}

func TestCancellationDuringPublicationRemainsResponsiveAndRollsBack(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	importOwner(t, repo)
	service := "Cancellation Fixture"
	draft, err := repo.CreateImportDraft(ctx, 1, largeImportAdmin, largeImportAdmin, "cancel-running", "phones.txt", []store.ImportNumber{{Phone: "+12025550123", Country: "United States", CountryCode: "US"}}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.ImportSettings{Services: []store.ImportService{{Name: service, PricePKR: 1, PerCycle: 3}}}
	if _, err = repo.QueueImport(ctx, 1, largeImportAdmin, draft.ID, settings, store.TelegramFlow{}); err != nil {
		t.Fatal(err)
	}
	job, err := repo.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	block, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(ctx)
	if _, err = block.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, strings.ToLower(service)); err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	done := make(chan error, 1)
	go func() { _, e := repo.ExecuteImport(runCtx, job); done <- e }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=hashtext($1)::oid AND NOT granted)`, strings.ToLower(service)).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("publication did not reach the blocking fixture")
		}
		time.Sleep(10 * time.Millisecond)
	}
	responsive, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if ok, e := repo.RenewImportLease(responsive, job); e != nil || !ok {
		t.Fatal("publication blocked lease renewal", ok, e)
	}
	if err = repo.DetachImportScreens(responsive, 1, largeImportAdmin); err != nil {
		t.Fatal("publication blocked navigation", err)
	}
	if cancelled, e := repo.ChangeImportState(responsive, 1, largeImportAdmin, job.ID, "cancel"); e != nil || cancelled.State != "cancelled" {
		t.Fatal("publication blocked cancellation", cancelled.State, e)
	}
	if err = block.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("cancelled worker published inventory")
	}
	var stock, catalogs int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM numbers),(SELECT count(*) FROM service_countries)`).Scan(&stock, &catalogs); err != nil || stock != 0 || catalogs != 0 {
		t.Fatal("cancelled publication was not rolled back", stock, catalogs, err)
	}
}

func TestImportFailureRollsBackEveryAppAndRetriesSameInput(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	importOwner(t, repo)
	draft, err := repo.CreateImportDraft(ctx, 1, largeImportAdmin, largeImportAdmin, "retry-fixture", "phones.csv", []store.ImportNumber{{Phone: "+12025550123", Country: "United States", CountryCode: "US"}}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.QueueImport(ctx, 1, largeImportAdmin, draft.ID, twoAppSettings(), store.TelegramFlow{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE FUNCTION fail_second_import_app() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.service='WhatsApp' THEN RAISE EXCEPTION 'fixture database failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_import_app BEFORE INSERT ON numbers FOR EACH ROW EXECUTE FUNCTION fail_second_import_app()`)
	if err != nil {
		t.Fatal(err)
	}
	job, err := repo.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, failure := repo.ExecuteImport(ctx, job)
	if failure == nil {
		t.Fatal("database failure was ignored")
	}
	if err = repo.FailImport(ctx, job, failure); err != nil {
		t.Fatal(err)
	}
	var stock, countries, profiles int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM numbers),(SELECT count(*) FROM service_countries),(SELECT count(*) FROM service_profiles)`).Scan(&stock, &countries, &profiles); err != nil || stock != 0 || countries != 0 || profiles != 0 {
		t.Fatal("partial publication", stock, countries, profiles, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_import_app ON numbers`); err != nil {
		t.Fatal(err)
	}
	retry, err := repo.ChangeImportState(ctx, 1, largeImportAdmin, draft.ID, "retry")
	if err != nil || retry.State != "queued" {
		t.Fatal(retry.State, err)
	}
	job, err = repo.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	results, err := repo.ExecuteImport(ctx, job)
	if err != nil || len(results) != 2 || results[0].Added != 1 || results[1].Added != 1 {
		t.Fatal("retry failed", results, err)
	}
}

func TestImportLeaseRecoveryCancellationAndExistingAssignments(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	importOwner(t, repo)
	phone := "+12025550123"
	if _, err := repo.AddNumbers(ctx, "WhatsApp", "United States", "US", 7, 0.25, 2, []string{phone}); err != nil {
		t.Fatal(err)
	}
	assigned, err := repo.AssignNumbers(ctx, largeImportAdmin, "WhatsApp", "United States", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := repo.CreateImportDraft(ctx, 1, largeImportAdmin, largeImportAdmin, "resume-fixture", "phones.txt", []store.ImportNumber{{Phone: phone, Country: "United States", CountryCode: "US"}}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	settings := twoAppSettings()
	settings.KeepPrices = true
	if _, err = repo.QueueImport(ctx, 1, largeImportAdmin, draft.ID, settings, store.TelegramFlow{}); err != nil {
		t.Fatal(err)
	}
	original, err := repo.ClaimImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE number_import_jobs SET lease_until=now()-interval '1 minute' WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := repo.ClaimImport(ctx, 1)
	if err != nil || resumed.LeaseToken == original.LeaseToken {
		t.Fatal("expired lease was not replaced", err)
	}
	if _, err = repo.ExecuteImport(ctx, original); err == nil {
		t.Fatal("old worker published stock")
	}
	results, err := repo.ExecuteImport(ctx, resumed)
	if err != nil {
		t.Fatal(err)
	}
	var state string
	var price float64
	if err = pool.QueryRow(ctx, `SELECT n.state::text,c.price_pkr FROM numbers n JOIN service_countries c USING(service,country) WHERE n.id=$1`, assigned.Numbers[0].ID).Scan(&state, &price); err != nil || state != "assigned" || price != 7 {
		t.Fatal("existing assignment or pricing changed", state, price, err)
	}
	if len(results) != 2 || results[0].Added != 1 || results[1].Existing != 1 {
		t.Fatal(results)
	}
	cancelled, err := repo.CreateImportDraft(ctx, 1, largeImportAdmin, largeImportAdmin, "cancel-fixture", "cancel.txt", []store.ImportNumber{{Phone: "+12025550124", Country: "United States", CountryCode: "US"}}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.QueueImport(ctx, 1, largeImportAdmin, cancelled.ID, settings, store.TelegramFlow{}); err != nil {
		t.Fatal(err)
	}
	cancelled, err = repo.ChangeImportState(ctx, 1, largeImportAdmin, cancelled.ID, "cancel")
	if err != nil || cancelled.State != "cancelled" {
		t.Fatal(cancelled.State, err)
	}
	if _, err = repo.ClaimImport(ctx, 1); err == nil {
		t.Fatal("cancelled job claimed")
	}
}

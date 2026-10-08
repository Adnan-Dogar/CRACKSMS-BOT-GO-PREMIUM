package db

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestPanelMigrationPreservesAccountsAndQueue(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer root.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	for _, file := range []string{"001_initial.sql", "002_premium_platform.sql", "003_tenant_withdrawals.sql", "004_interactive_accounts_and_imports.sql"} {
		raw, e := migrationFiles.ReadFile("migrations/" + file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(raw)); e != nil {
			t.Fatal(file, e)
		}
	}
	_, e = pool.Exec(ctx, `INSERT INTO panels(id,name,kind,config,last_cursor) VALUES(77,'Legacy provider','token_api','{"encrypted":"fixture-ciphertext"}','cursor-99');INSERT INTO panel_ingest_jobs(panel_id,dedup_key,payload) VALUES(77,'queued-fixture','{}')`)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := migrationFiles.ReadFile("migrations/005_panel_sources_and_broadcasts.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(raw)); e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `INSERT INTO users(id,balance_pkr,balance_usd,total_otps) VALUES(77,125.50,1.25,9);INSERT INTO user_preferences(bot_instance_id,user_id,theme_id,compact_menu,timezone) VALUES(1,77,4,false,'Asia/Karachi')`)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = migrationFiles.ReadFile("migrations/006_activity_and_user_tools.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(raw)); e != nil {
		t.Fatal(e)
	}
	var pkr, usd float64
	_, e = pool.Exec(ctx, `INSERT INTO service_countries(service,country,country_code,price_pkr) VALUES('WhatsApp','United States','US',1),('Telegram','United States','US',2);INSERT INTO numbers(id,phone,normalized_phone,service,country,state) VALUES(901,'+12025550123','12025550123','WhatsApp','United States','assigned');INSERT INTO assignments(id,user_id,service,country,expires_at) VALUES('00000000-0000-4000-8000-000000000001',77,'WhatsApp','United States',now()+interval '1 hour');INSERT INTO assignment_numbers(assignment_id,number_id) VALUES('00000000-0000-4000-8000-000000000001',901);INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id) VALUES(77,'PKR',125.5,'fixture','fixture','migration-fixture')`)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = migrationFiles.ReadFile("migrations/007_service_inventory_and_providers.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(raw)); e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `INSERT INTO numbers(phone,normalized_phone,service,country) VALUES('+12025550123','12025550123','Telegram','United States')`)
	if e != nil {
		t.Fatal("same phone second app rejected", e)
	}
	raw, e = migrationFiles.ReadFile("migrations/008_durable_number_imports.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(raw)); e != nil {
		t.Fatal("durable import migration failed", e)
	}
	raw, e = migrationFiles.ReadFile("migrations/009_connections_backups_and_child_feeds.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(raw)); e != nil {
		t.Fatal("connection/backup migration failed", e)
	}
	raw, e = migrationFiles.ReadFile("migrations/010_limited_usd_rewards.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(raw)); e != nil {
		t.Fatal("limited rewards migration failed", e)
	}
	var imports, stock int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM number_import_jobs),(SELECT count(*) FROM numbers)`).Scan(&imports, &stock); e != nil || imports != 0 || stock != 2 {
		t.Fatal("import migration changed inventory or created jobs", imports, stock, e)
	}
	var number int64
	var state string
	var ledger float64
	e = pool.QueryRow(ctx, `SELECT an.number_id,n.state::text,(SELECT sum(amount) FROM balance_ledger WHERE user_id=77) FROM assignment_numbers an JOIN numbers n ON n.id=an.number_id WHERE an.assignment_id='00000000-0000-4000-8000-000000000001'`).Scan(&number, &state, &ledger)
	if e != nil || number != 901 || state != "assigned" || ledger != 125.5 {
		t.Fatal("migration changed assignment or ledger", e)
	}
	var total, theme int
	var compact bool
	var format, zone string
	e = pool.QueryRow(ctx, `SELECT u.balance_pkr,u.balance_usd,u.total_otps,p.theme_id,p.compact_menu,p.timezone,p.display_format FROM users u JOIN user_preferences p ON p.user_id=u.id AND p.bot_instance_id=1 WHERE u.id=77`).Scan(&pkr, &usd, &total, &theme, &compact, &zone, &format)
	if e != nil || pkr != 125.5 || usd != 1.25 || total != 9 || theme != 4 || compact || zone != "Asia/Karachi" || format != "auto" {
		t.Fatal("activity migration changed balances or preferences", e)
	}
	var id, queued int64
	var cursor, encrypted, name, label string
	e = pool.QueryRow(ctx, `SELECT p.id,p.last_cursor,p.config->>'encrypted',s.name,p.account_label,j.panel_id FROM panels p JOIN panel_sources s ON s.id=p.source_id JOIN panel_ingest_jobs j ON j.panel_id=p.id WHERE p.id=77`).Scan(&id, &cursor, &encrypted, &name, &label, &queued)
	if e != nil || id != 77 || queued != 77 || cursor != "cursor-99" || encrypted != "fixture-ciphertext" || name != "Legacy provider" || label != "Default account" {
		t.Fatalf("legacy connection changed: %d %s %s %s %s %d %v", id, cursor, encrypted, name, label, queued, e)
	}
}

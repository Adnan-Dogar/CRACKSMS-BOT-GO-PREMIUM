package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/panels"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

func TestProviderNumberPreviewAndMultiAppImport(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	for _, service := range []string{"WhatsApp", "Telegram"} {
		if _, e := app.store.AddNumbers(ctx, service, "United States", "US", 7, 0.25, 2, nil); e != nil {
			t.Fatal(e)
		}
	}
	source, e := app.store.SavePanelSource(ctx, 1, 0, "Auges fixture", "augestel", "https://augestel.com")
	if e != nil {
		t.Fatal(e)
	}
	id, e := app.store.SavePanelAccount(ctx, 1, source, 0, "Account", map[string]any{"token": "fixture-secret", "url": "https://augestel.com"}, false)
	if e != nil {
		t.Fatal(e)
	}
	page := panels.NumberPage{Data: []panels.ProviderNumber{{Number: "+12025550123", Range: "US Premium", Rate: json.Number("0.0150")}, {Number: "+12025550124", Range: "US Premium", Rate: json.Number("0.0150")}}, Page: 1, LastPage: 1, Total: 2}
	raw, _ := json.Marshal(page)
	if e = app.store.SaveProviderResponse(ctx, 1, id, providerNumberCacheKey(1, ""), raw); e != nil {
		t.Fatal(e)
	}
	testCallback(app, 1, fmt.Sprintf("admin:panel:numbers:%d:1", id))
	screen := lastScreen(t, f)
	if !strings.Contains(screen.Params.Get("reply_markup"), "Filter Range") || strings.Contains(screen.Params.Get("text"), "fixture-secret") {
		t.Fatal("missing range tool or exposed credential")
	}
	testCallback(app, 1, fmt.Sprintf("admin:panel:importpage:%d:1", id))
	testText(app, 1, "WhatsApp,Telegram|United States|US")
	flow, e := app.store.TelegramFlow(ctx, 1, 1)
	if e != nil || flow.Step != "confirm" || !strings.Contains(lastScreen(t, f).Params.Get("text"), "2 additions") {
		t.Fatal("import preview missing", flow, e)
	}
	testCallback(app, 1, "admin:panel:importconfirm:"+flow.Data["nonce"])
	testCallback(app, 1, "admin:panel:importconfirm:"+flow.Data["nonce"])
	job, e := app.store.ClaimImport(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.ExecuteImport(ctx, job); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM numbers WHERE normalized_phone IN ('12025550123','12025550124')`).Scan(&count); e != nil || count != 4 {
		t.Fatal("multi-service import or idempotency failed", count, e)
	}
	catalog, e := app.store.Catalog(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, service := range []string{"WhatsApp", "Telegram"} {
		c := catalog[service][0]
		if c.PricePKR != 7 || c.PriceUSD != 0.25 || c.PerCycle != 2 {
			t.Fatal("provider import changed bot pricing", c)
		}
	}
	// Scoped panel staff can view inventory but cannot import it.
	testCallback(app, 3, fmt.Sprintf("admin:panel:importpage:%d:1", id))
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "Inventory permission") {
		t.Fatal("panel-only staff imported numbers")
	}
}

func TestMappingWorkflowUnmappedInboxAndRotationQueue(t *testing.T) {
	app, f, pool := newIntegrationApp(t)
	ctx := context.Background()
	if _, e := app.store.AddNumbers(ctx, "WhatsApp", "United States", "US", 1, 0, 1, nil); e != nil {
		t.Fatal(e)
	}
	source, e := app.store.SavePanelSource(ctx, 1, 0, "ASP fixture", "axon_asp", "https://axonsms.xyz")
	if e != nil {
		t.Fatal(e)
	}
	id, e := app.store.SavePanelAccount(ctx, 1, source, 0, "Account", map[string]any{"token": "fixture-old"}, false)
	if e != nil {
		t.Fatal(e)
	}
	testCallback(app, 1, fmt.Sprintf("admin:panel:mapadd:%d", id))
	flow, e := app.store.TelegramFlow(ctx, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	testCallback(app, 1, "admin:panel:mapkind:sender:"+flow.Data["nonce"])
	testText(app, 1, "WA_ALIAS")
	testCallback(app, 1, "admin:panel:mapservice:"+selectionKey("WhatsApp"))
	flow, e = app.store.TelegramFlow(ctx, 1, 1)
	if e != nil || flow.Step != "confirm" {
		t.Fatal(e)
	}
	testCallback(app, 1, "admin:panel:mapsave:"+flow.Data["nonce"])
	mappings, e := app.store.ServiceMappings(ctx, 1, id)
	if e != nil || len(mappings) != 1 || mappings[0].Service != "WhatsApp" {
		t.Fatal(mappings, e)
	}
	testCallback(app, 1, fmt.Sprintf("admin:panel:inbox:%d", id))
	if !strings.Contains(lastScreen(t, f).Params.Get("text"), "No unmapped") {
		t.Fatal("empty inbox unhandled")
	}
	if e = app.store.SchedulePanelTest(ctx, 1, id, time.Now(), true); e != nil {
		t.Fatal(e)
	}
	job, e := app.store.ClaimPanelTest(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = app.store.SaveProviderResponse(ctx, 1, id, "fixture-cache", []byte(`{"old":true}`)); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE panels SET last_cursor='fixture-cursor' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.SavePanelAccount(ctx, 1, source, id, "Account", map[string]any{"token": "fixture-new"}, false); e != nil {
		t.Fatal(e)
	}
	if e = app.store.FinishPanelTest(ctx, job, true, "stale success", time.Time{}); e != nil {
		t.Fatal(e)
	}
	panel, e := app.store.PanelForInstance(ctx, 1, id)
	if e != nil || panel.Enabled || panel.Healthy || panel.Config["token"] != "fixture-new" || panel.LastCursor != "fixture-cursor" {
		t.Fatal("stale test enabled rotated credentials", panel.Enabled, panel.Healthy, e)
	}
	var tests, cache int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM panel_connection_tests WHERE panel_id=$1),(SELECT count(*) FROM provider_response_cache WHERE panel_id=$1)`, id).Scan(&tests, &cache); e != nil || tests != 0 || cache != 0 {
		t.Fatal("rotation retained stale jobs or responses", tests, cache, e)
	}
	var config string
	if e = pool.QueryRow(ctx, `SELECT config::text FROM panels WHERE id=$1`, id).Scan(&config); e != nil || strings.Contains(config, "fixture-new") {
		t.Fatal("credential saved unencrypted", e)
	}
	// Range filter persists through navigation and uses a separate cache key.
	testCallback(app, 1, fmt.Sprintf("admin:panel:rangefilter:%d", id))
	flow, e = app.store.TelegramFlow(ctx, 1, 1)
	if e != nil || flow.Kind != "provider_range" {
		t.Fatal(flow, e)
	}
}

func TestImportParserAndNotificationQuietHours(t *testing.T) {
	phones, invalid, repeated := parseImportPhones([]byte("+12025550123,+12025550123\nnot-a-phone\n12025550124;12025550125"))
	if len(phones) != 3 || invalid != 1 || repeated != 1 {
		t.Fatal(phones, invalid, repeated)
	}
	start, end := 22, 8
	pref := store.NotificationPreferences{Enabled: true, Start: &start, End: &end, Zone: "Asia/Karachi"}
	for _, test := range []struct {
		hour    int
		allowed bool
	}{{21, true}, {22, false}, {2, false}, {7, false}, {8, true}} {
		loc, _ := time.LoadLocation(pref.Zone)
		now := time.Date(2026, 9, 28, test.hour, 0, 0, 0, loc)
		if pref.Allowed(now) != test.allowed {
			t.Fatal("quiet hours incorrect", test)
		}
	}
}

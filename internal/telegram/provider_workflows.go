package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/country"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/panels"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (a *App) providerAllowed(ctx context.Context, user int64) bool {
	ok, e := a.store.HasAdminPermission(ctx, a.botInstanceID, user, "manage_panels")
	return e == nil && ok
}
func providerNumberCacheKey(page int, rangeName string) string {
	return fmt.Sprintf("numbers:%d:%s", page, rangeName)
}
func (a *App) providerNumberRange(ctx context.Context, user, panel int64) string {
	flow, e := a.store.TelegramFlow(ctx, a.botInstanceID, user)
	if e == nil && (flow.Kind == "provider_numbers" || flow.Kind == "provider_import") && flow.Data["panel_id"] == fmt.Sprint(panel) {
		return flow.Data["range"]
	}
	return ""
}
func truncateProviderMessage(value string) string {
	r := []rune(value)
	if len(r) > 180 {
		return string(r[:180]) + "…"
	}
	return value
}
func (a *App) providerMenu(id int64, kind string) premium.InlineKeyboard {
	rows := [][]premium.InlineButton{{premium.Button("Reconnect", fmt.Sprintf("admin:panel:reconnect:%d", id), "primary", "refresh"), premium.Button("Edit Credentials", fmt.Sprintf("admin:source:editcreds:%d", id), "primary", "key")}, {premium.Button("Service Mappings", fmt.Sprintf("admin:panel:mappings:%d", id), "primary", "app"), premium.Button("Retry Unmapped", fmt.Sprintf("admin:panel:unmapped:%d", id), "primary", "refresh")}}
	rows = append(rows, []premium.InlineButton{premium.Button("Unmapped Inbox", fmt.Sprintf("admin:panel:inbox:%d", id), "primary", "mail")})
	if domain.Profile(kind).Numbers {
		rows = append(rows, []premium.InlineButton{premium.Button("Import Provider Numbers", fmt.Sprintf("admin:panel:numbers:%d:1", id), "success", "phone")})
	}
	if domain.Profile(kind).Statistics {
		rows = append(rows, []premium.InlineButton{premium.Button("Provider Statistics", fmt.Sprintf("admin:panel:providerstats:%d:30d:day", id), "primary", "chart")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Test Now", fmt.Sprintf("admin:panel:test:%d", id), "primary", "live"), premium.Button("Panels", "admin:panels", "primary", "plug")})
	return premium.InlineKeyboard{InlineKeyboard: rows}
}
func (a *App) showProviderDiagnostics(ctx context.Context, chat, id int64) {
	panel, e := a.store.PanelForInstance(ctx, a.botInstanceID, id)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	text, e := a.store.PanelDiagnostics(ctx, a.botInstanceID, id)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	a.sendHTML(chat, "📡 <b>Provider Account</b>\n\n"+html.EscapeString(panel.Name)+"\n"+html.EscapeString(text)+"\n\nCredentials: encrypted / hidden", a.providerMenu(id, panel.Kind))
}
func (a *App) showMappings(ctx context.Context, chat, id int64) {
	items, e := a.store.ServiceMappings(ctx, a.botInstanceID, id)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	text := "📱 <b>Provider Service Mappings</b>\n\nUnrecognized or conflicting services wait for a mapping. Retried messages retain their original timestamps.\n"
	rows := [][]premium.InlineButton{}
	for _, m := range items {
		if len(rows) >= 20 {
			break
		}
		text += fmt.Sprintf("\n%s: <code>%s</code> → <b>%s</b>", m.Kind, html.EscapeString(m.Value), html.EscapeString(m.Service))
		rows = append(rows, []premium.InlineButton{premium.Button("Remove "+m.Service, fmt.Sprintf("admin:panel:mapremove:%d:%d", id, m.ID), "danger", "trash")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Add Mapping", fmt.Sprintf("admin:panel:mapadd:%d", id), "success", "add"), premium.Button("Retry Unmapped", fmt.Sprintf("admin:panel:unmapped:%d", id), "primary", "refresh")}, []premium.InlineButton{premium.Button("Account", fmt.Sprintf("admin:panel:diagnostics:%d", id), "primary", "user")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) startMapping(ctx context.Context, chat, user, panel int64) {
	flow := store.TelegramFlow{Kind: "provider_mapping", Step: "kind", Data: map[string]string{"panel_id": fmt.Sprint(panel), "nonce": broadcastKey()}}
	_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, user, flow)
	rows := [][]premium.InlineButton{}
	for _, kind := range []string{"service", "sender", "message", "range"} {
		rows = append(rows, []premium.InlineButton{premium.Button("Match "+kind, "admin:panel:mapkind:"+kind+":"+flow.Data["nonce"], "primary", "app")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Cancel", "flow:cancel", "danger", "cancel")})
	a.sendHTML(chat, "Choose what identifies the application in this provider's messages.", premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) handleProviderText(ctx context.Context, m *tgbotapi.Message, flow store.TelegramFlow) bool {
	if !a.providerAllowed(ctx, m.From.ID) {
		a.sendHTML(m.Chat.ID, "Panel permission is required.", userBackMenu())
		return true
	}
	if flow.Kind == "provider_range" && flow.Step == "value" {
		rangeName := strings.TrimSpace(m.Text)
		if len(rangeName) > 200 {
			a.sendHTML(m.Chat.ID, "Use a range name of at most 200 characters.", flowCancelMenu("admin:panels"))
			return true
		}
		if rangeName == "all" {
			rangeName = ""
		}
		flow.Kind = "provider_numbers"
		flow.Step = "browse"
		flow.Data["range"] = rangeName
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, m.From.ID, flow)
		id, _ := strconv.ParseInt(flow.Data["panel_id"], 10, 64)
		a.showProviderNumbers(ctx, m.Chat.ID, id, 1)
		return true
	}
	if flow.Kind == "provider_mapping" && flow.Step == "value" {
		value := strings.TrimSpace(m.Text)
		if value == "" || len(value) > 256 {
			a.sendHTML(m.Chat.ID, "Use a match value of 1–256 characters.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["value"] = value
		flow.Step = "service"
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, m.From.ID, flow)
		catalog, e := a.store.CatalogForInstance(ctx, a.botInstanceID)
		if e != nil {
			a.sendError(m.Chat.ID, e)
			return true
		}
		rows := [][]premium.InlineButton{}
		for _, name := range store.SortedServices(catalog) {
			rows = append(rows, []premium.InlineButton{premium.Button(name, "admin:panel:mapservice:"+selectionKey(name), "primary", "app")})
		}
		rows = append(rows, []premium.InlineButton{premium.Button("Cancel", "flow:cancel", "danger", "cancel")})
		a.sendHTML(m.Chat.ID, "Choose the configured application for this match.", premium.InlineKeyboard{InlineKeyboard: rows})
		return true
	}
	if flow.Kind == "provider_import" && flow.Step == "mapping" {
		parts := splitExact(m.Text, "|", 3)
		if len(parts) != 3 {
			a.sendHTML(m.Chat.ID, "Use <code>App1,App2|Country name|Country code</code>, for example <code>WhatsApp,Telegram|Pakistan|PK</code>.", flowCancelMenu("admin:panels"))
			return true
		}
		catalog, e := a.store.CatalogForInstance(ctx, a.botInstanceID)
		if e != nil {
			a.sendError(m.Chat.ID, e)
			return true
		}
		services := []string{}
		for _, raw := range strings.Split(parts[0], ",") {
			name := ""
			for _, candidate := range store.SortedServices(catalog) {
				if strings.EqualFold(strings.TrimSpace(raw), candidate) {
					name = candidate
					break
				}
			}
			if name == "" {
				a.sendHTML(m.Chat.ID, "Choose configured app names. Add new apps through Upload Numbers first.", flowCancelMenu("admin:panels"))
				return true
			}
			services = append(services, name)
		}
		code := strings.ToUpper(parts[2])
		if len(services) > 20 || parts[1] == "" || len(parts[1]) > 80 || len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
			a.sendHTML(m.Chat.ID, "Use up to 20 apps and a two-letter country code.", flowCancelMenu("admin:panels"))
			return true
		}
		var page panels.NumberPage
		_ = json.Unmarshal([]byte(flow.Data["numbers"]), &page)
		for _, v := range page.Data {
			info := country.Detect(v.Number)
			ambiguous := (info.Code == "US" && code == "CA") || (info.Code == "RU" && code == "KZ")
			if info.Code != "" && info.Code != code && !ambiguous {
				a.sendHTML(m.Chat.ID, "This page contains a number outside the selected country. Apply a provider range filter before importing.", flowCancelMenu("admin:panels"))
				return true
			}
		}
		preview := fmt.Sprintf("\nNumbers on this page: %d", len(page.Data))
		phones := []string{}
		for _, v := range page.Data {
			phones = append(phones, v.Number)
		}
		for _, service := range services {
			existing, e := a.store.PreviewNumbers(ctx, service, phones)
			if e != nil {
				a.sendError(m.Chat.ID, e)
				return true
			}
			preview += fmt.Sprintf("\n%s: %d additions · %d already present", service, len(phones)-existing, existing)
		}
		raw, _ := json.Marshal(services)
		flow.Data["services"] = string(raw)
		flow.Data["country"], flow.Data["country_code"] = parts[1], code
		flow.Step = "confirm"
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, m.From.ID, flow)
		a.sendHTML(m.Chat.ID, "Review import:\nApps: <b>"+html.EscapeString(strings.Join(services, ", "))+"</b>\nCountry: <b>"+html.EscapeString(parts[1])+"</b>\n"+html.EscapeString(preview)+"\n\nExisting bot prices remain in use. New country entries start at 1 PKR / 0 USD and 3 numbers per request; adjust them in Prices. Provider rates are shown separately.", confirmationMenu("admin:panel:importconfirm:"+flow.Data["nonce"], "flow:cancel"))
		return true
	}
	return true
}
func (a *App) showProviderNumbers(ctx context.Context, chat, id int64, page int) {
	panel, e := a.store.PanelForInstance(ctx, a.botInstanceID, id)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	if panel.Kind != "augestel" {
		a.sendHTML(chat, "This provider publishes SMS retrieval only.", a.providerMenu(id, panel.Kind))
		return
	}
	rangeName := a.providerNumberRange(ctx, chat, id)
	key := providerNumberCacheKey(page, rangeName)
	payload, e := a.store.CachedProviderResponse(ctx, a.botInstanceID, id, key)
	var result panels.NumberPage
	if e == nil {
		e = json.Unmarshal(payload, &result)
	} else {
		adapter, err := panels.NewAdapterWithGate(panel, a.store)
		e = err
		if e == nil {
			defer adapter.Close()
			provider := adapter.(panels.NumberProvider)
			callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			result, e = provider.Numbers(callCtx, page, rangeName)
			cancel()
			if e == nil {
				raw, _ := json.Marshal(result)
				_ = a.store.SaveProviderResponse(ctx, a.botInstanceID, id, key, raw)
			}
		}
	}
	if e != nil {
		a.sendHTML(chat, panels.SafeError(e), a.providerMenu(id, panel.Kind))
		return
	}
	text := fmt.Sprintf("📱 <b>Assigned Provider Numbers</b>\n\nPage %d/%d · Total %d\n\n", page, max(result.LastPage, 1), result.Total)
	if rangeName != "" {
		text += "Range: " + html.EscapeString(rangeName) + "\n\n"
	}
	for i, v := range result.Data {
		if i >= 15 {
			text += "\nMore numbers are included in this page's import.\n"
			break
		}
		text += fmt.Sprintf("<code>%s</code> · %s · %s\n", html.EscapeString(v.Number), html.EscapeString(v.Range), html.EscapeString(v.Rate.String()))
	}
	rows := [][]premium.InlineButton{{premium.Button("Import This Page", fmt.Sprintf("admin:panel:importpage:%d:%d", id, page), "success", "add")}}
	rows = append(rows, []premium.InlineButton{premium.Button("Filter Range", fmt.Sprintf("admin:panel:rangefilter:%d", id), "primary", "filter")})
	pager := []premium.InlineButton{}
	if page > 1 {
		pager = append(pager, premium.Button("Previous", fmt.Sprintf("admin:panel:numbers:%d:%d", id, page-1), "primary", "back"))
	}
	if page < result.LastPage {
		pager = append(pager, premium.Button("Next", fmt.Sprintf("admin:panel:numbers:%d:%d", id, page+1), "primary", "play"))
	}
	if len(pager) > 0 {
		rows = append(rows, pager)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Account", fmt.Sprintf("admin:panel:diagnostics:%d", id), "primary", "user")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) providerStatistics(ctx context.Context, chat, id int64, period, group string) {
	panel, e := a.store.PanelForInstance(ctx, a.botInstanceID, id)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	days := 30
	if period == "7d" {
		days = 7
	} else if period == "24h" {
		days = 1
	} else {
		period = "30d"
	}
	from := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	to := time.Now().UTC().Format("2006-01-02")
	key := "stats:" + from + ":" + to + ":" + group
	cached, e := a.store.CachedProviderResponse(ctx, a.botInstanceID, id, key)
	var result map[string]any
	if e == nil {
		decoder := json.NewDecoder(bytes.NewReader(cached))
		decoder.UseNumber()
		e = decoder.Decode(&result)
	} else {
		adapter, err := panels.NewAdapterWithGate(panel, a.store)
		e = err
		if e == nil {
			defer adapter.Close()
			provider, ok := adapter.(panels.StatisticsProvider)
			if !ok {
				e = errors.New("provider statistics are not available")
			} else {
				callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				result, e = provider.Statistics(callCtx, from, to, group)
				cancel()
				if e == nil {
					raw, _ := json.Marshal(result)
					_ = a.store.SaveProviderResponse(ctx, a.botInstanceID, id, key, raw)
				}
			}
		}
	}
	if e != nil {
		a.sendHTML(chat, panels.SafeError(e), a.providerMenu(id, panel.Kind))
		return
	}
	summary, ok := result["summary"].(map[string]any)
	if !ok {
		summary = map[string]any{}
		if pagination, ok := result["pagination"].(map[string]any); ok {
			summary["messages"] = pagination["total"]
		}
	}
	keys := []string{}
	for k := range summary {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	classic := fmt.Sprintf("📊 <b>Provider Statistics</b>\n\n%s → %s UTC\nProvider payouts are separate from bot rewards.\n", from, to)
	rich := "<h1>Provider Statistics</h1><p>" + from + " → " + to + " UTC</p><p>Provider payouts are separate from bot rewards.</p><table striped compact><tr><th>Metric</th><th>Value</th></tr>"
	for _, k := range keys {
		label := html.EscapeString(strings.ReplaceAll(k, "_", " "))
		raw, _ := json.Marshal(summary[k])
		value := html.EscapeString(string(raw))
		classic += "\n" + label + ": <b>" + value + "</b>"
		rich += "<tr><td>" + label + "</td><td>" + value + "</td></tr>"
	}
	rich += "</table>"
	for _, section := range []struct {
		key, title string
		columns    []string
	}{{"daily_breakdown", "Period breakdown", []string{"period", "messages", "earnings"}}, {"top_numbers", "Top numbers", []string{"number", "messages", "earnings"}}} {
		entries, ok := result[section.key].([]any)
		if !ok || len(entries) == 0 {
			continue
		}
		classic += "\n\n<b>" + section.title + "</b>"
		rich += "<h2>" + section.title + "</h2><table striped compact><tr>"
		for _, c := range section.columns {
			rich += "<th>" + c + "</th>"
		}
		rich += "</tr>"
		for i, item := range entries {
			if i >= 5 {
				break
			}
			fields, ok := item.(map[string]any)
			if !ok {
				continue
			}
			values := []string{}
			rich += "<tr>"
			for _, c := range section.columns {
				value := html.EscapeString(fmt.Sprint(fields[c]))
				values = append(values, value)
				rich += "<td>" + value + "</td>"
			}
			rich += "</tr>"
			classic += "\n" + strings.Join(values, " · ")
		}
		rich += "</table>"
	}
	rows := [][]premium.InlineButton{}
	row := []premium.InlineButton{}
	for _, p := range []string{"24h", "7d", "30d"} {
		row = append(row, premium.Button(p, fmt.Sprintf("admin:panel:providerstats:%d:%s:%s", id, p, group), activeStyle(p == period), "calendar"))
	}
	rows = append(rows, row)
	if panel.Kind == "augestel" {
		row = nil
		for _, g := range []string{"day", "week", "month"} {
			row = append(row, premium.Button(g, fmt.Sprintf("admin:panel:providerstats:%d:%s:%s", id, period, g), activeStyle(g == group), "chart"))
		}
		rows = append(rows, row)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Account", fmt.Sprintf("admin:panel:diagnostics:%d", id), "primary", "user")})
	a.sendDocument(chat, screenDocument{RichHTML: rich, ClassicHTML: classic, Keyboard: premium.InlineKeyboard{InlineKeyboard: rows}})
}
func (a *App) handleProviderCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) bool {
	p := strings.Split(cb.Data, ":")
	if len(p) < 4 || p[0] != "admin" || p[1] != "panel" {
		return false
	}
	action := p[2]
	switch action {
	case "diagnostics", "reconnect", "mappings", "mapadd", "mapkind", "mapservice", "mapsave", "mapremove", "unmapped", "inbox", "rangefilter", "numbers", "importpage", "importconfirm", "providerstats":
	default:
		return false
	}
	if !a.providerAllowed(ctx, cb.From.ID) {
		a.sendHTML(cb.Message.Chat.ID, "Panel permission is required.", userBackMenu())
		return true
	}
	chat, user := cb.Message.Chat.ID, cb.From.ID
	id, _ := strconv.ParseInt(p[3], 10, 64)
	switch action {
	case "diagnostics":
		a.showProviderDiagnostics(ctx, chat, id)
	case "rangefilter":
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, user, store.TelegramFlow{Kind: "provider_range", Step: "value", Data: map[string]string{"panel_id": fmt.Sprint(id)}})
		a.sendHTML(chat, "Send the exact provider range name, or <code>all</code> to remove the filter.", flowCancelMenu("admin:panels"))
	case "inbox":
		events, e := a.store.UnmappedEvents(ctx, a.botInstanceID, id)
		if e != nil {
			a.sendError(chat, e)
			return true
		}
		text := "📨 <b>Unmapped SMS Inbox</b>\n\nLatest 10 messages awaiting an application mapping.\n"
		for _, event := range events {
			text += "\n<b>" + html.EscapeString(event.Sender) + "</b> · " + html.EscapeString(event.Service) + " · " + html.EscapeString(event.ProviderRange) + "\n<code>" + html.EscapeString(event.NormalizedPhone) + "</code>\n" + html.EscapeString(truncateProviderMessage(event.Message)) + "\n"
		}
		if len(events) == 0 {
			text += "\nNo unmapped messages."
		}
		a.sendHTML(chat, text, a.providerMenu(id, ""))
	case "reconnect":
		if e := a.store.SchedulePanelTest(ctx, a.botInstanceID, id, time.Now(), true); e != nil {
			a.sendError(chat, e)
		} else {
			a.sendHTML(chat, "Connection test queued. This account will reconnect after a successful test.", a.providerMenu(id, ""))
		}
	case "mappings":
		a.showMappings(ctx, chat, id)
	case "mapadd":
		a.startMapping(ctx, chat, user, id)
	case "mapremove":
		if len(p) != 5 {
			return true
		}
		mapping, _ := strconv.ParseInt(p[4], 10, 64)
		_ = a.store.DeleteServiceMapping(ctx, a.botInstanceID, id, mapping)
		a.showMappings(ctx, chat, id)
	case "unmapped":
		n, e := a.store.RetryUnmapped(ctx, a.botInstanceID, id)
		if e != nil {
			a.sendError(chat, e)
		} else {
			a.sendHTML(chat, fmt.Sprintf("%d mapped messages queued for processing. Original assignment times still apply.", n), a.providerMenu(id, ""))
		}
	case "numbers":
		page := 1
		if len(p) == 5 {
			page, _ = strconv.Atoi(p[4])
		}
		a.showProviderNumbers(ctx, chat, id, max(page, 1))
	case "providerstats":
		if len(p) != 6 {
			return true
		}
		a.providerStatistics(ctx, chat, id, p[4], p[5])
	case "importpage":
		allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, user, "manage_settings")
		if !allowed {
			a.sendHTML(chat, "Inventory permission is required.", userBackMenu())
			return true
		}
		if len(p) != 5 {
			return true
		}
		page, _ := strconv.Atoi(p[4])
		rangeName := a.providerNumberRange(ctx, user, id)
		raw, e := a.store.CachedProviderResponse(ctx, a.botInstanceID, id, providerNumberCacheKey(page, rangeName))
		if e != nil {
			a.showProviderNumbers(ctx, chat, id, page)
			return true
		}
		flow := store.TelegramFlow{Kind: "provider_import", Step: "mapping", Data: map[string]string{"panel_id": p[3], "page": p[4], "numbers": string(raw), "range": rangeName, "nonce": broadcastKey()}}
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, user, flow)
		a.sendHTML(chat, "Map this page to configured applications and a country:\n<code>App1,App2|Country name|Country code</code>\n\nExample: <code>WhatsApp,Telegram|Pakistan|PK</code>. Import pages containing a single country; use a provider range filter for mixed ranges.", flowCancelMenu("admin:panels"))
	case "importconfirm":
		flow, e := a.store.TelegramFlow(ctx, a.botInstanceID, user)
		allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, user, "manage_settings")
		if e != nil || !allowed || flow.Kind != "provider_import" || flow.Step != "confirm" || flow.Data["nonce"] != p[3] {
			return true
		}
		var data panels.NumberPage
		if json.Unmarshal([]byte(flow.Data["numbers"]), &data) != nil {
			return true
		}
		phones := []string{}
		seen := map[string]bool{}
		duplicates := 0
		for _, v := range data.Data {
			phone := store.NormalizePhone(v.Number)
			if len(phone) < 5 || len(phone) > 20 {
				continue
			}
			if seen[phone] {
				duplicates++
				continue
			}
			seen[phone] = true
			phones = append(phones, phone)
		}
		numbers := make([]store.ImportNumber, 0, len(phones))
		for _, phone := range phones {
			numbers = append(numbers, store.ImportNumber{Phone: phone, Country: flow.Data["country"], CountryCode: flow.Data["country_code"]})
		}
		job, e := a.store.CreateImportDraft(ctx, a.botInstanceID, user, chat, flow.Data["nonce"], "Provider page "+flow.Data["page"], numbers, len(data.Data)-len(phones)-duplicates, duplicates)
		if e != nil {
			a.sendError(chat, e)
			return true
		}
		settings := store.ImportSettings{KeepPrices: true}
		for _, service := range importServices(flow) {
			emoji, _ := a.store.ServiceEmojiID(ctx, a.botInstanceID, service)
			if emoji == "" {
				emoji = premium.AppEmojiID(service)
			}
			settings.Services = append(settings.Services, store.ImportService{Name: service, EmojiID: emoji, PricePKR: 1, PerCycle: 3})
		}
		job, e = a.store.QueueImport(ctx, a.botInstanceID, user, job.ID, settings, flow)
		if e != nil {
			a.sendError(chat, e)
			return true
		}
		job.MessageID = cb.Message.MessageID
		if e = a.showImportJob(ctx, chat, user, job); e != nil {
			a.sendError(chat, e)
		}
	case "mapkind", "mapservice", "mapsave":
		flow, e := a.store.TelegramFlow(ctx, a.botInstanceID, user)
		if e != nil || flow.Kind != "provider_mapping" {
			return true
		}
		panel, _ := strconv.ParseInt(flow.Data["panel_id"], 10, 64)
		if action == "mapkind" && flow.Step == "kind" && len(p) == 5 && flow.Data["nonce"] == p[4] {
			flow.Data["kind"] = p[3]
			flow.Step = "value"
			_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, user, flow)
			a.sendHTML(chat, "Send the exact match value. For message rules, use a Go regular expression such as <code>(?i)\\bWhatsApp\\b</code>.", flowCancelMenu("admin:panels"))
		} else if action == "mapservice" && flow.Step == "service" {
			catalog, _ := a.store.CatalogForInstance(ctx, a.botInstanceID)
			for _, name := range store.SortedServices(catalog) {
				if selectionKey(name) == p[3] {
					flow.Data["service"] = name
				}
			}
			if flow.Data["service"] == "" {
				return true
			}
			flow.Step = "confirm"
			_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, user, flow)
			a.sendHTML(chat, "Save mapping to <b>"+html.EscapeString(flow.Data["service"])+"</b>?", confirmationMenu("admin:panel:mapsave:"+flow.Data["nonce"], "flow:cancel"))
		} else if action == "mapsave" && flow.Step == "confirm" && flow.Data["nonce"] == p[3] {
			e = a.store.SaveServiceMapping(ctx, a.botInstanceID, panel, store.ServiceMapping{Kind: flow.Data["kind"], Value: flow.Data["value"], Service: flow.Data["service"]})
			if e != nil {
				a.sendHTML(chat, html.EscapeString(e.Error()), flowCancelMenu("admin:panels"))
			} else {
				_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, user)
				a.showMappings(ctx, chat, panel)
			}
		}
	}
	return true
}
func (a *App) handleProviderCommand(ctx context.Context, m *tgbotapi.Message, command, args string) bool {
	if command != "paneltest" && command != "syncnumbers" && command != "servicemappings" {
		return false
	}
	if !a.providerAllowed(ctx, m.From.ID) {
		a.sendHTML(m.Chat.ID, "Panel permission is required.", userBackMenu())
		return true
	}
	id, e := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
	if e != nil {
		a.listPanelSources(ctx, m.Chat.ID)
		return true
	}
	switch command {
	case "paneltest":
		a.testPanelNow(ctx, m.Chat.ID, id)
	case "syncnumbers":
		a.showProviderNumbers(ctx, m.Chat.ID, id, 1)
	case "servicemappings":
		a.showMappings(ctx, m.Chat.ID, id)
	}
	return true
}

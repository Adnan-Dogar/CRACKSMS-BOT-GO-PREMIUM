package telegram

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var importPhonePattern = regexp.MustCompile(`^\+?[0-9][0-9 ()-]*$`)

func parseImportPhones(body []byte) ([]string, int, int) {
	text := strings.TrimPrefix(string(body), "\ufeff")
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' || r == '\t' })
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	firstLine, _, _ := strings.Cut(text, "\n")
	if strings.Contains(firstLine, ";") && !strings.Contains(firstLine, ",") {
		reader.Comma = ';'
	}
	if header, err := reader.Read(); err == nil {
		column := -1
		for i, value := range header {
			key := strings.ToLower(strings.TrimSpace(value))
			key = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(key)
			switch key {
			case "number", "phone", "phonenumber", "msisdn", "mobile", "destination":
				column = i
			}
		}
		if column >= 0 {
			parts = nil
			for {
				record, e := reader.Read()
				if e == io.EOF {
					break
				}
				if e != nil || column >= len(record) {
					parts = append(parts, "invalid CSV row")
					continue
				}
				parts = append(parts, record[column])
			}
		}
	}
	phones := []string{}
	seen := map[string]bool{}
	invalid, duplicates := 0, 0
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		items := []string{part}
		fields := strings.Fields(part)
		multiple := len(fields) > 1
		for _, f := range fields {
			if len(store.NormalizePhone(f)) < 5 {
				multiple = false
			}
		}
		if multiple {
			items = fields
		}
		for _, item := range items {
			item = strings.TrimSpace(strings.Trim(item, "\"'"))
			normal := store.NormalizePhone(item)
			if !importPhonePattern.MatchString(item) || len(normal) < 5 || len(normal) > 20 {
				invalid++
				continue
			}
			if seen[normal] {
				duplicates++
				continue
			}
			seen[normal] = true
			phones = append(phones, normal)
		}
	}
	return phones, invalid, duplicates
}
func importServices(flow store.TelegramFlow) []string {
	if raw, ok := flow.Data["services"]; ok {
		var names []string
		if json.Unmarshal([]byte(raw), &names) == nil {
			return names
		}
		return nil
	}
	if name := strings.TrimSpace(flow.Data["service"]); name != "" {
		return []string{name}
	}
	return nil
}
func (a *App) importChoices(ctx context.Context, flow store.TelegramFlow) ([]string, map[string][]store.CatalogCountry) {
	catalog, _ := a.store.CatalogForInstance(ctx, a.botInstanceID)
	names := store.SortedServices(catalog)
	known := map[string]bool{}
	for _, v := range names {
		known[strings.ToLower(v)] = true
	}
	for _, v := range premium.DefaultApps() {
		if !known[strings.ToLower(v.Name)] {
			names = append(names, v.Name)
			known[strings.ToLower(v.Name)] = true
		}
	}
	for name := range importCustomApps(flow) {
		if !known[strings.ToLower(name)] {
			names = append(names, name)
			known[strings.ToLower(name)] = true
		}
	}
	filtered := []string{}
	query := strings.ToLower(flow.Data["search_query"])
	for _, name := range names {
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			filtered = append(filtered, name)
		}
	}
	sort.Strings(filtered)
	return filtered, catalog
}
func importPage(flow store.TelegramFlow, count int) (int, int, int) {
	page, _ := strconv.Atoi(flow.Data["page"])
	pages := max(1, (count+11)/12)
	page = max(0, min(page, pages-1))
	start := page * 12
	return page, start, min(start+12, count)
}
func (a *App) multiImportMenu(ctx context.Context, flow store.TelegramFlow) premium.InlineKeyboard {
	names, catalog := a.importChoices(ctx, flow)
	picked := map[string]bool{}
	for _, v := range importServices(flow) {
		picked[v] = true
	}
	page, start, end := importPage(flow, len(names))
	rows := [][]premium.InlineButton{}
	row := []premium.InlineButton{}
	for _, name := range names[start:end] {
		label := name
		if picked[name] {
			label = "Selected · " + name
		}
		row = append(row, premium.InlineButton{Text: label, CallbackData: "admin:upload:mp:" + selectionKey(name), Style: activeStyle(picked[name]), IconCustomEmojiID: importAppEmoji(name, flow, catalog[name])})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	nav := []premium.InlineButton{}
	if page > 0 {
		nav = append(nav, premium.Button("Previous", fmt.Sprintf("admin:upload:page:%d", page-1), "primary", "back"))
	}
	if end < len(names) {
		nav = append(nav, premium.Button("Next", fmt.Sprintf("admin:upload:page:%d", page+1), "primary", "list"))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Search Apps", "admin:upload:search", "primary", "search"), premium.Button("Select Page", "admin:upload:pickpage", "success", "check")}, []premium.InlineButton{premium.Button("Clear Selection", "admin:upload:clear", "danger", "trash"), premium.Button("Add App", "admin:upload:other", "primary", "add")}, []premium.InlineButton{premium.Button("Continue", "admin:upload:mdone", "success", "check"), premium.Button("Cancel", "flow:cancel", "danger", "cancel")})
	return premium.InlineKeyboard{InlineKeyboard: rows}
}
func (a *App) showMultiImport(ctx context.Context, chat, user int64, flow store.TelegramFlow, note string) {
	names, _ := a.importChoices(ctx, flow)
	page, _, _ := importPage(flow, len(names))
	selected := importServices(flow)
	text := fmt.Sprintf("📱 <b>Select Import Apps</b>\n\nSelected apps: %d / 20\nPage: %d / %d\n", len(selected), page+1, max(1, (len(names)+11)/12))
	if len(selected) > 0 {
		text += "Apps: " + html.EscapeString(strings.Join(selected, ", ")) + "\n"
	}
	if flow.Data["search_query"] != "" {
		text += "Search: " + html.EscapeString(flow.Data["search_query"]) + "\n"
	}
	text += "\nChoose apps, then Continue. Each app receives independent inventory."
	if note != "" {
		text += "\n\n" + note
	}
	a.importScreen(ctx, chat, user, flow, text, a.multiImportMenu(ctx, flow))
}
func (a *App) handleMultiImport(ctx context.Context, cb *tgbotapi.CallbackQuery, flow store.TelegramFlow) bool {
	if cb.Data == "admin:upload:multi" && flow.Step == "service" {
		flow.Step = "multi_service"
		flow.Data["services"] = "[]"
		a.showMultiImport(ctx, cb.Message.Chat.ID, cb.From.ID, flow, "")
		return true
	}
	if flow.Step != "multi_service" {
		return false
	}
	names, _ := a.importChoices(ctx, flow)
	selected := importServices(flow)
	note := ""
	switch {
	case strings.HasPrefix(cb.Data, "admin:upload:mp:"):
		key := strings.TrimPrefix(cb.Data, "admin:upload:mp:")
		name := ""
		for _, v := range names {
			if selectionKey(v) == key {
				name = v
				break
			}
		}
		if name == "" {
			return true
		}
		filtered := []string{}
		found := false
		for _, v := range selected {
			if v == name {
				found = true
			} else {
				filtered = append(filtered, v)
			}
		}
		if !found {
			if len(filtered) < 20 {
				filtered = append(filtered, name)
			} else {
				note = "Select at most 20 apps. Deselect an app to add another."
			}
		}
		flow.Data["services"] = importServiceJSON(filtered)
	case strings.HasPrefix(cb.Data, "admin:upload:page:"):
		page, e := strconv.Atoi(strings.TrimPrefix(cb.Data, "admin:upload:page:"))
		if e != nil || page < 0 {
			return true
		}
		flow.Data["page"] = strconv.Itoa(page)
	case cb.Data == "admin:upload:clear":
		flow.Data["services"] = "[]"
	case cb.Data == "admin:upload:pickpage":
		_, start, end := importPage(flow, len(names))
		picked := map[string]bool{}
		for _, v := range selected {
			picked[v] = true
		}
		for _, v := range names[start:end] {
			if len(selected) == 20 {
				break
			}
			if !picked[v] {
				selected = append(selected, v)
				picked[v] = true
			}
		}
		flow.Data["services"] = importServiceJSON(selected)
	case cb.Data == "admin:upload:search":
		flow.Step = "import_search"
		a.importScreen(ctx, cb.Message.Chat.ID, cb.From.ID, flow, "🔎 <b>Search Import Apps</b>\n\nSend an app name, or <code>/all</code> to clear the filter. Your selections are kept.", flowCancelMenu("admin:numbers"))
		return true
	case cb.Data == "admin:upload:mdone":
		if len(selected) == 0 {
			note = "Choose at least one app before continuing."
		} else {
			flow.Data["service"] = selected[0]
			flow.Data["emoji_id"], _ = a.store.ServiceEmojiID(ctx, a.botInstanceID, selected[0])
			if flow.Data["emoji_id"] == "" {
				flow.Data["emoji_id"] = premium.AppEmojiID(selected[0])
			}
			a.promptImportPricing(ctx, cb.Message.Chat.ID, cb.From.ID, flow)
			return true
		}
	default:
		return true
	}
	a.showMultiImport(ctx, cb.Message.Chat.ID, cb.From.ID, flow, note)
	return true
}

func importCustomApps(flow store.TelegramFlow) map[string]string {
	apps := map[string]string{}
	_ = json.Unmarshal([]byte(flow.Data["custom_apps"]), &apps)
	if apps == nil {
		apps = map[string]string{}
	}
	return apps
}
func importAppEmoji(name string, flow store.TelegramFlow, countries []store.CatalogCountry) string {
	if id := importCustomApps(flow)[name]; id != "" {
		return id
	}
	return catalogServiceEmojiID(name, countries)
}

func importAppFormMenu(flow store.TelegramFlow) premium.InlineKeyboard {
	menu := flowCancelMenu("admin:numbers")
	if _, ok := flow.Data["services"]; ok {
		menu.InlineKeyboard = append([][]premium.InlineButton{{premium.Button("Back to Apps", "admin:upload:apps", "primary", "back")}}, menu.InlineKeyboard...)
	}
	return menu
}

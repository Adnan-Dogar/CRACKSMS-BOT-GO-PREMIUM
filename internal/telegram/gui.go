package telegram

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (a *App) homeMenu(ctx context.Context, user int64, admin bool) any {
	pref, e := a.store.Preference(ctx, a.botInstanceID, user)
	if e != nil {
		return compactMenu(admin, a.isMain)
	}
	links, _ := a.store.EffectiveLinks(ctx, a.botInstanceID, a.links)
	if !pref.CompactMenu {
		return dailyMenu(fullMenu(admin, a.isMain, links))
	}
	return dailyMenu(compactMenu(admin, a.isMain))
}
func dailyMenu(menu premium.InlineKeyboard) premium.InlineKeyboard {
	menu.InlineKeyboard = append(menu.InlineKeyboard, []premium.InlineButton{premium.Button("My Numbers", "tools:numbers:0", "primary", "phone"), premium.Button("Search Apps & Countries", "tools:search", "primary", "search")})
	return menu
}
func (a *App) showUserSettings(ctx context.Context, chat, user int64) {
	pref, e := a.store.Preference(ctx, a.botInstanceID, user)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	theme, e := a.store.EffectiveTheme(ctx, a.botInstanceID, user)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	mode := "Compact"
	if !pref.CompactMenu {
		mode = "Full"
	}
	text := fmt.Sprintf("⚙️ <b>Your Settings</b>\n\n🎨 OTP theme: <b>T%d</b>\n🕓 Timezone: <b>%s</b>\n📋 Main menu: <b>%s</b>\n\nTap a preference to change it. Changes are saved for this bot.", theme, html.EscapeString(pref.Timezone), mode)
	text += "\nDisplay: <b>" + html.EscapeString(pref.DisplayFormat) + "</b>"
	menu := settingsMenu()
	menu.InlineKeyboard = append([][]premium.InlineButton{{premium.Button("Compact Menu", "prefs:mode:compact", activeStyle(pref.CompactMenu), "list"), premium.Button("Full Menu", "prefs:mode:full", activeStyle(!pref.CompactMenu), "list")}, {premium.Button("Change Timezone", "prefs:timezone", "primary", "clock")}}, menu.InlineKeyboard...)
	rows := [][]premium.InlineButton{}
	for _, formats := range [][]string{{"auto", "rich"}, {"classic"}} {
		row := []premium.InlineButton{}
		for _, format := range formats {
			label := strings.ToUpper(format[:1]) + format[1:]
			if format == pref.DisplayFormat {
				label = "Selected · " + label
			}
			row = append(row, premium.Button(label, "prefs:display:"+format, activeStyle(format == pref.DisplayFormat), "document"))
		}
		rows = append(rows, row)
	}
	menu.InlineKeyboard = append(rows, menu.InlineKeyboard...)
	a.sendHTML(chat, text, menu)
}
func (a *App) showAdminSettings(ctx context.Context, chat int64) {
	values, e := a.store.InstanceSettings(ctx, a.botInstanceID)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	limit, e := a.store.AssignmentLimit(ctx, a.botInstanceID)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	label := "Service default"
	if limit > 0 {
		label = fmt.Sprint(limit)
	}
	text := "⚙️ <b>Bot Settings</b>\n\nAssignment limit: <b>" + label + "</b>\n\nConfigure this bot's appearance and delivery defaults. Masked and hidden OTPs remain copyable when group buttons are enabled."
	for _, key := range []string{"group_url", "channel_url", "number_bot_url", "developer_url", "support_url"} {
		if v, ok := values[key].(string); ok {
			text += "\n" + html.EscapeString(strings.TrimSuffix(key, "_url")) + ": " + html.EscapeString(v)
		}
	}
	menu := adminSettingsMenu()
	menu.InlineKeyboard = append([][]premium.InlineButton{{premium.Button("Assignment Limit", "admin:setting:assignment_limit", "primary", "number"), premium.Button("Group Privacy", "admin:setting:privacy", "primary", "lock")}, {premium.Button("OTP Group Link", "admin:setting:group_url", "primary", "people"), premium.Button("Channel Link", "admin:setting:channel_url", "primary", "channel")}, {premium.Button("Number Bot Link", "admin:setting:number_bot_url", "primary", "bot")}, {premium.Button("Developer Link", "admin:setting:developer_url", "primary", "developer"), premium.Button("Support Link", "admin:setting:support_url", "primary", "support")}}, menu.InlineKeyboard...)
	a.sendHTML(chat, text, menu)
}
func (a *App) handleGUISettingsCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) bool {
	d := cb.Data
	if strings.HasPrefix(d, "prefs:display:") {
		format := strings.TrimPrefix(d, "prefs:display:")
		if err := a.store.SetDisplayFormat(ctx, a.botInstanceID, cb.From.ID, format); err != nil {
			a.sendError(cb.Message.Chat.ID, err)
		} else {
			a.displayFormat = format
			a.showUserSettings(ctx, cb.Message.Chat.ID, cb.From.ID)
		}
		return true
	}
	if strings.HasPrefix(d, "help:") {
		a.showHelpTopic(cb.Message.Chat.ID, strings.TrimPrefix(d, "help:"))
		return true
	}
	if strings.HasPrefix(d, "stats:") || strings.HasPrefix(d, "admin:stats:") {
		admin := strings.HasPrefix(d, "admin:")
		if admin {
			ok, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, cb.From.ID, "view_analytics")
			if !ok {
				a.sendHTML(cb.Message.Chat.ID, "Statistics permission is required.", userBackMenu())
				return true
			}
		}
		parts := strings.Split(d, ":")
		a.showPeriodStats(ctx, cb.Message.Chat.ID, cb.From.ID, parts[len(parts)-1], admin)
		return true
	}
	if strings.HasPrefix(d, "admin:bots:filter:") {
		ok, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, cb.From.ID, "manage_bots")
		if !ok || !a.isMain {
			a.sendHTML(cb.Message.Chat.ID, "Child-bot management permission is required in the main bot.", userBackMenu())
			return true
		}
		p := strings.Split(d, ":")
		page, _ := strconv.Atoi(p[len(p)-1])
		a.showBots(ctx, cb.Message.Chat.ID, p[3], page)
		return true
	}
	if !strings.HasPrefix(d, "prefs:") && !strings.HasPrefix(d, "admin:setting:") {
		return false
	}
	if strings.HasPrefix(d, "prefs:mode:") {
		mode := strings.TrimPrefix(d, "prefs:mode:")
		if mode != "compact" && mode != "full" {
			return true
		}
		compact := mode == "compact"
		if e := a.store.SetMenuMode(ctx, a.botInstanceID, cb.From.ID, compact); e != nil {
			a.sendError(cb.Message.Chat.ID, e)
		} else {
			a.showUserSettings(ctx, cb.Message.Chat.ID, cb.From.ID)
		}
		return true
	}
	flow := store.TelegramFlow{Kind: "ui_setting", Step: "value", Data: map[string]string{}}
	prompt := "Send an IANA timezone, for example Asia/Karachi, UTC, or Europe/London."
	if strings.HasPrefix(d, "admin:setting:") {
		allowed, e := a.store.HasAdminPermission(ctx, a.botInstanceID, cb.From.ID, "manage_settings")
		if e != nil || !allowed {
			a.sendHTML(cb.Message.Chat.ID, "Settings permission is required.", userBackMenu())
			return true
		}
		key := strings.TrimPrefix(d, "admin:setting:")
		flow.Data["key"] = key
		switch key {
		case "assignment_limit":
			prompt = "Send a number from 1 to 1000, or 0 to use service defaults."
		case "privacy":
			prompt = "Send visible, masked, or hidden for new OTP groups. Copy buttons still copy the full OTP when enabled. Minimal requires buttons to access the code."
		case "group_url", "channel_url", "number_bot_url", "developer_url", "support_url":
			prompt = "Send the HTTPS link, or none to remove it."
		default:
			a.sendHTML(cb.Message.Chat.ID, "Unknown setting.", userBackMenu())
			return true
		}
	} else {
		flow.Data["key"] = "timezone"
	}
	if e := a.store.SetTelegramFlow(ctx, a.botInstanceID, cb.From.ID, flow); e != nil {
		a.sendError(cb.Message.Chat.ID, e)
	} else {
		a.sendHTML(cb.Message.Chat.ID, "⚙️ <b>Change Setting</b>\n\n"+prompt, flowCancelMenu("menu:settings"))
	}
	return true
}
func (a *App) handleSettingText(ctx context.Context, m *tgbotapi.Message, flow store.TelegramFlow) bool {
	key := flow.Data["key"]
	v := strings.TrimSpace(m.Text)
	var e error
	if key == "timezone" {
		e = a.store.SetTimezone(ctx, a.botInstanceID, m.From.ID, v)
	} else {
		allowed, err := a.store.HasAdminPermission(ctx, a.botInstanceID, m.From.ID, "manage_settings")
		if err != nil || !allowed {
			a.sendHTML(m.Chat.ID, "You no longer have settings permission.", userBackMenu())
			return true
		}
		switch key {
		case "assignment_limit":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 1000 {
				e = fmt.Errorf("use 0–1000")
			} else {
				e = a.store.SetInstanceSetting(ctx, a.botInstanceID, key, n)
			}
		case "privacy":
			e = a.store.SetBotInstancePrivacy(ctx, a.botInstanceID, v)
		default:
			if v == "none" {
				v = ""
			}
			parsed, err := url.ParseRequestURI(v)
			if v != "" && (err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil) {
				e = fmt.Errorf("use an HTTPS link or none")
			} else {
				e = a.store.SetInstanceSetting(ctx, a.botInstanceID, key, v)
			}
		}
	}
	if e != nil {
		a.sendHTML(m.Chat.ID, "Invalid setting: "+html.EscapeString(e.Error()), flowCancelMenu("menu:settings"))
		return true
	}
	_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, m.From.ID)
	if key == "timezone" {
		a.showUserSettings(ctx, m.Chat.ID, m.From.ID)
	} else {
		a.showAdminSettings(ctx, m.Chat.ID)
	}
	return true
}
func (a *App) showPeriodStats(ctx context.Context, chat, user int64, period string, admin bool) {
	location := a.location
	if !admin {
		pref, e := a.store.Preference(ctx, a.botInstanceID, user)
		if e != nil {
			a.sendError(chat, e)
			return
		}
		if l, e := time.LoadLocation(pref.Timezone); e == nil {
			location = l
		}
	}
	now := time.Now().In(location)
	var since *time.Time
	switch period {
	case "today", "7", "30":
		n := 1
		if period != "today" {
			n, _ = strconv.Atoi(period)
		}
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -(n - 1))
		since = &start
	case "all":
	default:
		period = "today"
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		since = &start
	}
	scope := user
	prefix := "stats:"
	title := "My Statistics"
	if admin {
		scope = 0
		prefix = "admin:stats:"
		title = "Bot Statistics"
	}
	stats, e := a.store.PeriodStatistics(ctx, a.botInstanceID, scope, since)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	label := map[string]string{"today": "Today", "7": "Last 7 days", "30": "Last 30 days", "all": "All time"}[period]
	text := fmt.Sprintf("📊 <b>%s</b>\n\n🗓 %s · %s\n\n📩 SMS events: <b>%d</b>\n🔑 Counted OTPs: <b>%d</b>\n💰 OTP earnings: <b>%.2f PKR · %.4f USD</b>", title, label, html.EscapeString(location.String()), stats.Events, stats.Counted, stats.BasePKR, stats.BaseUSD)
	if admin {
		text += fmt.Sprintf("\n👤 Users receiving OTPs: <b>%d</b>", stats.Users)
		health, e := a.store.Analytics(ctx, a.botInstanceID)
		if e != nil {
			a.sendError(chat, e)
			return
		}
		text += fmt.Sprintf("\n\n📱 Shared inventory: <b>%d available</b>\n🔌 Account connections: <b>%d/%d online</b>\n📨 Delivery queue: <b>%d pending · %d failed</b>", health.AvailableNumbers, health.ActivePanels, health.TotalPanels, health.DeliveryPending, health.DeliveryFailed)
	} else {
		text += fmt.Sprintf("\n🎁 Account-wide rewards: <b>%.2f PKR · %.4f USD</b>", stats.RewardsPKR, stats.RewardsUSD)
	}
	if stats.Events == 0 {
		text += "\n\nNo activity in this period."
	}
	menu := premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button("Today", prefix+"today", activeStyle(period == "today"), "calendar"), premium.Button("7 Days", prefix+"7", activeStyle(period == "7"), "calendar")}, {premium.Button("30 Days", prefix+"30", activeStyle(period == "30"), "calendar"), premium.Button("All Time", prefix+"all", activeStyle(period == "all"), "history")}, {premium.Button("Refresh", prefix+period, "primary", "refresh"), premium.Button("Main Menu", "menu:home", "primary", "home")}}}
	if admin {
		menu.InlineKeyboard = append(menu.InlineKeyboard, []premium.InlineButton{premium.Button("Admin Home", "menu:admin", "primary", "home")})
	}
	a.sendHTML(chat, text, menu)
}

var helpTopics = map[string]struct{ Title, Body, Action string }{
	"start":    {"Getting Started", "Choose Get Number, select a service and country, then wait for SMS. Use More Options for your account and tools.", "menu:services"},
	"otp":      {"Numbers & OTPs", "Assigned numbers expire at the time shown on your assignment screen. The first valid OTP consumes the number. Numbers without OTP return automatically, with a personal cooldown. Your history contains previous messages.", "menu:liveotp"},
	"account":  {"Account & Withdrawals", "Open your profile to inspect balances and referral rewards. Add a payout account, enter an amount, and review before confirming a withdrawal.", "menu:profile"},
	"bots":     {"Child Bots", "Create a token in BotFather, then use Create My Bot. An administrator approves the request. Main panel templates are available in child bots, but each child adds its own account. Main OTP sharing starts off; a main administrator may enable new OTPs for a child's active numbers and apps.", "menu:createbot"},
	"premium":  {"Premium Features", "Your plan determines available integrations. Webhooks and scheduling require Pro; API access requires Enterprise.", "menu:premium"},
	"admin":    {"Admin Guide", "Add a panel's name and link first, then Add Account to configure credentials. Upload numbers with Add App for custom services, configure OTP groups, and use Statistics to monitor the bot. User Backups can export and review a restore without changing current balances.", "menu:admin"},
	"activity": {"Live Activity", "Live OTP keeps your assignments and codes private. All Countries includes configured and historical countries, even with zero OTPs. Top Apps and Top Countries count unique received OTPs for this bot. Use period filters, Refresh, and Pause/Resume; automatic refresh pauses after ten minutes.", "menu:liveotp"},
	"tools":    {"Favorites & Alerts", "Save an application and country to Favorites, or repeat your last successful selection. Number Alerts notify you when eligible inventory becomes available. Alerts do not reserve numbers. Remove a watch to unsubscribe.", "tools:favorites:0"},
	"trouble":  {"Troubleshooting", "Refresh old menus after an update. Check available inventory and required membership. A provider challenge requires supported access or allowlisting. Expired forms can be restarted from the relevant menu.", "menu:help"},
}

func (a *App) helpHome(chat int64, admin bool) {
	rows := [][]premium.InlineButton{}
	for _, key := range []string{"start", "otp", "activity", "tools", "account", "bots", "premium", "admin", "trouble"} {
		if key == "admin" && !admin {
			continue
		}
		if key == "bots" && !a.isMain {
			continue
		}
		topic := helpTopics[key]
		rows = append(rows, []premium.InlineButton{premium.Button(topic.Title, "help:"+key, "primary", helpTopicIcon(key))})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Main Menu", "menu:home", "primary", "home")})
	rich := "<h2>" + premium.Emoji("book", "📖") + " CrackSMS Help</h2><p>Choose a topic below. Each guide includes a shortcut to its feature.</p><p>Get Number → App → Country → Live OTP</p>"
	rich += "<details><summary>How do I receive an OTP?</summary><p>Choose Get Number, select an app and country, then open Live OTP. Your codes are visible only in your private chat.</p><p><tg-button type=\"callback_data\" style=\"link\" data=\"menu:services\">Get Number</tg-button></p></details>"
	rich += "<details><summary>What do activity counts mean?</summary><p>Counts show unique received OTPs for this bot, including unassigned messages. Credited usage appears separately in your Statistics.</p></details>"
	a.sendDocument(chat, screenDocument{RichHTML: rich, ClassicHTML: "📖 <b>CrackSMS Help</b>\n\nChoose a guide below. Each guide includes a shortcut to its feature.\n\nGet Number → App → Country → Live OTP. Activity counts received OTPs; credited usage appears separately in Statistics.", Keyboard: premium.InlineKeyboard{InlineKeyboard: rows}})
}
func (a *App) showHelpTopic(chat int64, key string) {
	topic, ok := helpTopics[key]
	if !ok {
		a.helpHome(chat, false)
		return
	}
	keyboard := premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button("Open Feature", topic.Action, "success", helpTopicIcon(key))}, {premium.Button("Help", "menu:help", "primary", "help"), premium.Button("Home", "menu:home", "", "home")}}}
	rich := "<h2>" + premium.Emoji(helpTopicIcon(key), "📖") + " " + html.EscapeString(topic.Title) + "</h2><p>" + strings.ReplaceAll(html.EscapeString(topic.Body), "\n", "<br>") + "</p><p><tg-button type=\"callback_data\" style=\"link\" data=\"" + html.EscapeString(topic.Action) + "\">Open Feature</tg-button></p>"
	a.sendDocument(chat, screenDocument{RichHTML: rich, ClassicHTML: premium.Emoji(helpTopicIcon(key), "📖") + " <b>" + html.EscapeString(topic.Title) + "</b>\n\n" + html.EscapeString(topic.Body), Keyboard: keyboard})
}
func (a *App) showBots(ctx context.Context, chat int64, status string, page int) {
	items, e := a.store.ListBotInstances(ctx, false)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	filtered := []domain.BotInstance{}
	for _, item := range items {
		if item.IsMain {
			continue
		}
		if status == "all" || item.Status == status || (status == "pending" && item.Status == "approved") {
			filtered = append(filtered, item)
		}
	}
	if page < 0 {
		page = 0
	}
	pages := max(1, (len(filtered)+7)/8)
	if page >= pages {
		page = pages - 1
	}
	rows := [][]premium.InlineButton{}
	for _, item := range filtered[page*8 : min(len(filtered), page*8+8)] {
		rows = append(rows, []premium.InlineButton{premium.Button(item.Name+" · "+item.Status, fmt.Sprintf("admin:bot:view:%d", item.ID), "primary", "bot")})
	}
	for _, pair := range [][]string{{"all", "pending"}, {"running", "stopped"}, {"error"}} {
		row := []premium.InlineButton{}
		for _, filter := range pair {
			row = append(row, premium.Button(strings.Title(filter), fmt.Sprintf("admin:bots:filter:%s:0", filter), activeStyle(status == filter), "bot"))
		}
		rows = append(rows, row)
	}
	if page > 0 {
		rows = append(rows, []premium.InlineButton{premium.Button("Previous", fmt.Sprintf("admin:bots:filter:%s:%d", status, page-1), "primary", "back")})
	}
	if page+1 < pages {
		rows = append(rows, []premium.InlineButton{premium.Button("Next", fmt.Sprintf("admin:bots:filter:%s:%d", status, page+1), "primary", "play")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Create Bot", "menu:createbot", "success", "add"), premium.Button("Admin Home", "menu:admin", "primary", "home")})
	text := fmt.Sprintf("🤖 <b>Child Bots</b>\n\nFilter: %s · Page %d/%d\nSelect a bot to inspect its state and manage it.", html.EscapeString(status), page+1, pages)
	if len(filtered) == 0 {
		text += "\n\nNo bots in this category."
	}
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}

func (a *App) currentLinks(ctx context.Context) themes.Links {
	links, e := a.store.EffectiveLinks(ctx, a.botInstanceID, a.links)
	if e != nil {
		return a.links
	}
	return links
}
func (a *App) instancePrivacy(ctx context.Context) string {
	var privacy string
	e := a.store.Pool().QueryRow(ctx, `SELECT default_group_privacy FROM bot_instances WHERE id=$1`, a.botInstanceID).Scan(&privacy)
	if e != nil {
		return "visible"
	}
	return privacy
}

func helpTopicIcon(key string) string {
	switch key {
	case "start", "otp":
		return "phone"
	case "activity":
		return "live"
	case "tools":
		return "favorite"
	case "account":
		return "user"
	case "bots":
		return "bot"
	case "premium":
		return "premium"
	case "admin":
		return "admin"
	case "trouble":
		return "help"
	}
	return "book"
}

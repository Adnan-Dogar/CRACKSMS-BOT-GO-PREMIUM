package telegram

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	webhookservice "github.com/adnan-dogar/cracksms-vnext/internal/webhook"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func compactMenu(admin, isMain bool) premium.InlineKeyboard {
	rows := [][]premium.InlineButton{
		{premium.Button("Get Number", "menu:services", "success", "phone")},
		{premium.Button("Live OTP", "menu:liveotp", "primary", "live"), premium.Button("My Account", "menu:profile", "primary", "user")},
		{premium.Button("Top Apps", "activity:apps:24h:0", "primary", "ranking"), premium.Button("Top Countries", "activity:topcountries:24h:0", "primary", "globe")},
		{premium.Button("Favorites", "tools:favorites:0", "primary", "favorite"), premium.Button("My Statistics", "menu:stats", "primary", "chart")},
		{premium.Button("History", "menu:history:0", "primary", "history"), premium.Button("Settings", "menu:settings", "primary", "settings")},
		{premium.Button("Help", "menu:help", "primary", "help"), premium.Button("More Options", "menu:full", "primary", "list")},
	}
	if isMain {
		rows = append(rows, []premium.InlineButton{premium.Button("Create My Bot", "menu:createbot", "success", "bot")})
	}
	if admin {
		rows = append(rows, []premium.InlineButton{premium.Button("Admin Dashboard", "menu:admin", "primary", "admin")})
	}

	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func fullMenu(admin, isMain bool, links themes.Links) premium.InlineKeyboard {
	rows := [][]premium.InlineButton{}
	for _, row := range compactMenu(false, isMain).InlineKeyboard {
		containsMore := false
		for _, button := range row {
			if button.CallbackData == "menu:full" {
				containsMore = true
			}
		}
		if !containsMore {
			rows = append(rows, row)
		}
	}
	// Keep main entry points and add the less frequently used tools.
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Repeat Selection", "tools:last", "primary", "refresh"), premium.Button("Withdraw", "menu:withdraw", "success", "withdraw")},
		[]premium.InlineButton{premium.Button("Tutorials", "menu:tutorials", "primary", "book"), premium.Button("OTP Themes", "menu:themes", "primary", "palette")},
		[]premium.InlineButton{premium.Button("Webhooks", "menu:webhooks", "primary", "link"), premium.Button("Scheduling", "menu:schedule", "primary", "settings")},
		[]premium.InlineButton{premium.Button("API Access", "menu:api", "primary", "developer"), premium.Button("Number Alerts", "tools:alerts:0", "primary", "bell")},
		[]premium.InlineButton{premium.Button("Premium", "menu:premium", "primary", "premium"), premium.Button("Analytics", "menu:analytics", "primary", "chart")},
	)
	if isMain {
		rows = append(rows, []premium.InlineButton{premium.Button("My Bots", "menu:mybots", "primary", "bot")})
	}
	var community []premium.InlineButton
	if isMain && links.Channel != "" {
		community = append(community, premium.InlineButton{Text: "Channel", URL: links.Channel, Style: "success", IconCustomEmojiID: premium.ID("channel")})
	}
	if isMain && links.NumberBot != "" {
		community = append(community, premium.InlineButton{Text: "Get Numbers", URL: links.NumberBot, Style: "primary", IconCustomEmojiID: premium.ID("number")})
	}
	if len(community) > 0 {
		rows = append(rows, community)
	}
	var contacts []premium.InlineButton
	if links.Developer != "" && isMain {
		contacts = append(contacts, premium.InlineButton{Text: "Developer", URL: links.Developer, Style: "primary", IconCustomEmojiID: premium.ID("developer")})
	}
	if links.Support != "" {
		contacts = append(contacts, premium.InlineButton{Text: "Support", URL: links.Support, Style: "primary", IconCustomEmojiID: premium.ID("support")})
	}
	if len(contacts) > 0 {
		rows = append(rows, contacts)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Help", "menu:help", "primary", "support"), premium.Button("Compact Menu", "menu:compact", "primary", "phone")})
	if admin {
		rows = append(rows, []premium.InlineButton{premium.Button("Admin Dashboard", "menu:admin", "danger", "admin")})
	}
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func adminDashboard() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Numbers", "admin:numbers", "primary", "phone"), premium.Button("Broadcast", "admin:broadcast", "danger", "channel")},
		{premium.Button("Statistics", "admin:analytics", "success", "chart"), premium.Button("Users & Tiers", "admin:users", "primary", "premium")},
		{premium.Button("Panels", "admin:panels", "primary", "chart"), premium.Button("OTP Groups", "admin:groups", "success", "channel")},
		{premium.Button("Rewards", "admin:rewards", "success", "money"), premium.Button("Withdrawals", "admin:withdrawals", "danger", "money")},
		{premium.Button("Child Bots", "admin:bots", "primary", "bot"), premium.Button("Admins", "admin:admins", "danger", "admin")},
		{premium.Button("User Backups", "admin:backups", "primary", "download")},
		{premium.Button("Required Chats", "admin:required", "primary", "lock"), premium.Button("OTP Patterns", "admin:patterns", "primary", "otp")},
		{premium.Button("Tutorials", "admin:tutorials", "primary", "message"), premium.Button("Settings", "admin:settings", "primary", "settings")},
		{premium.Button("Help", "menu:help", "danger", "support"), premium.Button("Main Menu", "menu:home", "primary", "phone")},
	}}
}

func (a *App) rateLimited(userID int64) bool {
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	a.rateMu.Lock()
	defer a.rateMu.Unlock()
	if len(a.rateWindow) >= 4096 {
		for id, window := range a.rateWindow {
			if len(window) == 0 || !window[len(window)-1].After(cutoff) {
				delete(a.rateWindow, id)
			}
		}
	}
	items := a.rateWindow[userID][:0]
	for _, item := range a.rateWindow[userID] {
		if item.After(cutoff) {
			items = append(items, item)
		}
	}
	if len(items) >= 60 {
		a.rateWindow[userID] = items
		return true
	}
	a.rateWindow[userID] = append(items, now)
	return false
}

func (a *App) handleMyStats(ctx context.Context, chatID, userID int64) {
	a.showPeriodStats(ctx, chatID, userID, "today", false)
}

func (a *App) handleHistory(ctx context.Context, chatID, userID int64, offset int) {
	items, err := a.store.OTPHistory(ctx, a.botInstanceID, userID, 5, offset)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("📜 <b>My OTP History</b>\n")
	if len(items) == 0 {
		text.WriteString("\n📭 No OTP history on this page.")
	}
	for _, item := range items {
		fmt.Fprintf(&text, "\n\n📱 <code>+%s</code> · %s\n🔑 <code>%s</code> · %s\n📡 %s",
			html.EscapeString(item.Phone), html.EscapeString(item.Service), html.EscapeString(item.Code),
			item.ReceivedAt.In(a.location).Format("02 Jan 03:04 PM"), html.EscapeString(item.PanelName))
	}
	var nav []premium.InlineButton
	if offset > 0 {
		nav = append(nav, premium.Button("Previous", fmt.Sprintf("menu:history:%d", max(0, offset-5)), "primary", "history"))
	}
	if len(items) == 5 {
		nav = append(nav, premium.Button("Next", fmt.Sprintf("menu:history:%d", offset+5), "primary", "history"))
	}
	rows := [][]premium.InlineButton{}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Main Menu", "menu:home", "primary", "phone")})
	a.sendHTML(chatID, text.String(), premium.InlineKeyboard{InlineKeyboard: rows})
}

func (a *App) handleTheme(ctx context.Context, message *tgbotapi.Message, command, args string) {
	if command == "otpguipreview" {
		event := domain.OTPEvent{PanelName: "Preview Panel", NormalizedPhone: "923001234567", Service: "WhatsApp", Code: "123456", Message: "Your WhatsApp verification code is 123456"}
		links, err := a.store.EffectiveLinks(ctx, a.botInstanceID, a.links)
		if err != nil {
			a.sendError(message.Chat.ID, err)
			return
		}
		for _, theme := range themes.Catalog() {
			a.sendLegacyHTML(message.Chat.ID, fmt.Sprintf("<b>T%d · %s</b>\n\n%s", theme.ID, theme.Name,
				themes.Format(event, theme.ID, true, "visible")), themes.Keyboard(event, theme.ID, links, true, true))
		}
		return
	}
	if args != "" {
		themeID, err := strconv.Atoi(args)
		if err != nil || themeID < 0 || themeID > 9 {
			a.sendHTML(message.Chat.ID, "Theme must be a number from 0 to 9.", nil)
			return
		}
		if err := a.store.SetUserTheme(ctx, a.botInstanceID, message.From.ID, themeID); err != nil {
			a.sendError(message.Chat.ID, err)
			return
		}
		a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Theme changed to <b>T%d · %s</b>.", themeID, themes.Get(themeID).Name), nil)
		return
	}
	effective, _ := a.store.EffectiveTheme(ctx, a.botInstanceID, message.From.ID)
	var rows [][]premium.InlineButton
	for i := 0; i < 10; i += 2 {
		left, right := themes.Get(i), themes.Get(i+1)
		rows = append(rows, []premium.InlineButton{
			premium.Button(fmt.Sprintf("%s T%d %s", activeMark(i == effective), i, left.Name), fmt.Sprintf("theme:set:%d", i), "primary", "celebrate"),
			premium.Button(fmt.Sprintf("%s T%d %s", activeMark(i+1 == effective), i+1, right.Name), fmt.Sprintf("theme:set:%d", i+1), "primary", "celebrate"),
		})
	}
	a.sendHTML(message.Chat.ID, "🎨 <b>Choose an OTP theme</b>\n\nUse /otpguipreview to preview all ten complete layouts.", premium.InlineKeyboard{InlineKeyboard: rows})
}

func (a *App) handlePremium(ctx context.Context, chatID, userID int64) {
	tier, err := a.store.UserTier(ctx, a.botInstanceID, userID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	features := []string{"10 premium OTP themes", "OTP delivery and admin panel"}
	if store.TierAllows(tier, "analytics") {
		features = append(features, "Advanced analytics", "Signed webhooks", "Message scheduling", "Priority support")
	}
	if store.TierAllows(tier, "api_access") {
		features = append(features, "Enterprise API keys", "50 panels", "Child bots", "Custom OTP patterns")
	}
	text := fmt.Sprintf("💎 <b>%s Plan</b>\n\n🔌 Panel limit: <b>%d</b>\n\n✅ %s\n\nFree: 2 panels · Pro: 10 panels · Enterprise: 50 panels + API/child bots",
		strings.ToUpper(tier), store.TierPanelLimit(tier), strings.Join(features, "\n✅ "))
	a.sendHTML(chatID, text, premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{
		premium.Button("Analytics", "menu:analytics", "primary", "chart"), premium.Button("Themes", "menu:themes", "success", "celebrate"),
	}, {
		premium.Button("Main Menu", "menu:home", "primary", "phone"),
	}}})
}

func (a *App) handleAnalytics(ctx context.Context, chatID, userID int64) {
	tier, _ := a.store.UserTier(ctx, a.botInstanceID, userID)
	if !store.TierAllows(tier, "analytics") {
		a.sendHTML(chatID, "📊 Advanced analytics require the <b>Pro</b> or <b>Enterprise</b> tier.", nil)
		return
	}
	a.showPeriodStats(ctx, chatID, userID, "today", false)
}

func (a *App) handleAnalyticsAdmin(ctx context.Context, chatID int64) {
	a.showPeriodStats(ctx, chatID, 0, "today", true)
}

func (a *App) handleWebhook(ctx context.Context, message *tgbotapi.Message, args string) {
	tier, _ := a.store.UserTier(ctx, a.botInstanceID, message.From.ID)
	if !store.TierAllows(tier, "webhooks") {
		a.sendHTML(message.Chat.ID, "🔗 Webhooks require the <b>Pro</b> or <b>Enterprise</b> tier.", nil)
		return
	}
	parts := splitExact(args, "|", 3)
	action := strings.ToLower(parts[0])
	switch action {
	case "add":
		if len(parts) < 2 || webhookservice.ValidateURL(parts[1]) != nil {
			a.sendHTML(message.Chat.ID, "Usage: <code>/webhook add|https://example.com/otp|otp.received</code>. Public HTTPS endpoints only.", nil)
			return
		}
		events := []string{"otp.received"}
		if len(parts) == 3 && parts[2] != "" {
			events = strings.Split(strings.ReplaceAll(parts[2], " ", ""), ",")
		}
		id, secret, err := a.store.CreateWebhook(ctx, a.botInstanceID, message.From.ID, parts[1], events)
		if err != nil {
			a.sendError(message.Chat.ID, err)
			return
		}
		a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Webhook <b>#%d</b> created. Save this signing secret now; it will not be shown again:\n<code>%s</code>\n\nVerify <code>X-CrackSMS-Signature</code> using HMAC-SHA256.", id, html.EscapeString(secret)), nil)
	case "remove":
		if len(parts) < 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/webhook remove|id</code>", nil)
			return
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil {
			err = a.store.RemoveWebhook(ctx, a.botInstanceID, message.From.ID, id)
		}
		a.respondAdminResult(message.Chat.ID, "Webhook removed.", err)
	default:
		items, err := a.store.ListWebhooks(ctx, a.botInstanceID, message.From.ID)
		if err != nil {
			a.sendError(message.Chat.ID, err)
			return
		}
		var text strings.Builder
		text.WriteString("🔗 <b>My Webhooks</b>\n")
		if len(items) == 0 {
			text.WriteString("\nNo webhooks configured yet.\n")
		}
		for _, item := range items {
			fmt.Fprintf(&text, "\n#%d · %s\nEnabled: %s · failures: %d\n", item.ID, html.EscapeString(item.URL), onOff(item.Enabled), item.ConsecutiveFailures)
		}
		text.WriteString("\nUse the buttons below to manage your webhooks.")
		a.sendHTML(message.Chat.ID, text.String(), integrationMenu("webhook", "unhook"))
	}
}

func (a *App) handleSchedule(ctx context.Context, message *tgbotapi.Message, args string) {
	tier, _ := a.store.UserTier(ctx, a.botInstanceID, message.From.ID)
	if !store.TierAllows(tier, "scheduling") {
		a.sendHTML(message.Chat.ID, "⏰ Scheduling requires the <b>Pro</b> or <b>Enterprise</b> tier.", nil)
		return
	}
	parts := splitExact(args, "|", 4)
	action := strings.ToLower(parts[0])
	switch action {
	case "add":
		if len(parts) != 4 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/schedule add|2026-08-20 15:30|user:123/group:-100/all|message</code> (Asia/Karachi)", nil)
			return
		}
		deliverAt, err := time.ParseInLocation("2006-01-02 15:04", parts[1], a.location)
		if err != nil {
			a.sendHTML(message.Chat.ID, "Invalid time. Use YYYY-MM-DD HH:MM in Asia/Karachi.", nil)
			return
		}
		targetKind := "all_users"
		var targetID *int64
		if parts[2] != "all" {
			target := splitExact(parts[2], ":", 2)
			if len(target) != 2 || (target[0] != "user" && target[0] != "group") {
				a.sendHTML(message.Chat.ID, "Target must be user:ID, group:ID, or all.", nil)
				return
			}
			id, parseErr := strconv.ParseInt(target[1], 10, 64)
			if parseErr != nil {
				a.sendHTML(message.Chat.ID, "Invalid target ID.", nil)
				return
			}
			targetKind, targetID = target[0], &id
		}
		id, err := a.store.CreateScheduledMessage(ctx, domain.ScheduledMessage{BotInstanceID: a.botInstanceID,
			CreatorUserID: message.From.ID, TargetKind: targetKind, TargetID: targetID, Body: parts[3], DeliverAt: deliverAt})
		if err != nil {
			a.sendError(message.Chat.ID, err)
			return
		}
		a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Scheduled message <b>#%d</b> for <b>%s</b>.", id, deliverAt.Format("02 Jan 2006 03:04 PM MST")), nil)
	case "cancel":
		if len(parts) < 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/schedule cancel|id</code>", nil)
			return
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil {
			err = a.store.CancelScheduledMessage(ctx, a.botInstanceID, message.From.ID, id)
		}
		a.respondAdminResult(message.Chat.ID, "Scheduled message cancelled.", err)
	default:
		items, err := a.store.ListScheduledMessages(ctx, a.botInstanceID, message.From.ID)
		if err != nil {
			a.sendError(message.Chat.ID, err)
			return
		}
		var text strings.Builder
		text.WriteString("⏰ <b>Scheduled Messages</b>\n")
		if len(items) == 0 {
			text.WriteString("\nNo scheduled messages yet.\n")
		}
		for _, item := range items {
			fmt.Fprintf(&text, "\n#%d · %s · %s\n%s\n", item.ID, item.DeliverAt.In(a.location).Format("02 Jan 03:04 PM"), item.TargetKind, html.EscapeString(short(item.Body, 80)))
		}
		text.WriteString("\nUse the buttons below to manage scheduled messages.")
		a.sendHTML(message.Chat.ID, text.String(), integrationMenu("schedule", "unschedule"))
	}
}

func (a *App) handleAPIKey(ctx context.Context, message *tgbotapi.Message, args string) {
	tier, _ := a.store.UserTier(ctx, a.botInstanceID, message.From.ID)
	if !store.TierAllows(tier, "api_access") {
		a.sendHTML(message.Chat.ID, "⚡ API access requires the <b>Enterprise</b> tier.", nil)
		return
	}
	parts := splitExact(args, "|", 2)
	switch strings.ToLower(parts[0]) {
	case "create":
		name := "Telegram"
		if len(parts) == 2 && parts[1] != "" {
			name = parts[1]
		}
		key, secret, err := a.store.CreateAPIKey(ctx, a.botInstanceID, message.From.ID, name, nil, nil)
		if err != nil {
			a.sendError(message.Chat.ID, err)
			return
		}
		a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ API key <b>#%d</b> created. Copy it now; it cannot be shown again:\n<code>%s</code>", key.ID, html.EscapeString(secret)), nil)
	case "revoke":
		if len(parts) < 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/apikey revoke|id</code>", nil)
			return
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil {
			err = a.store.RevokeAPIKey(ctx, a.botInstanceID, message.From.ID, id)
		}
		a.respondAdminResult(message.Chat.ID, "API key revoked.", err)
	default:
		keys, err := a.store.ListAPIKeys(ctx, a.botInstanceID, message.From.ID)
		if err != nil {
			a.sendError(message.Chat.ID, err)
			return
		}
		var text strings.Builder
		text.WriteString("⚡ <b>Enterprise API Keys</b>\n")
		if len(keys) == 0 {
			text.WriteString("\nNo API keys created yet.\n")
		}
		for _, key := range keys {
			fmt.Fprintf(&text, "\n#%d · %s · <code>%s…</code> · %s\n", key.ID, html.EscapeString(key.Name), key.Prefix, onOff(key.Enabled))
		}
		text.WriteString("\nEndpoints: <code>/api/v1/me</code>, <code>/api/v1/analytics</code>, <code>/api/v1/otp/history</code>")
		a.sendHTML(message.Chat.ID, text.String(), integrationMenu("apikey", "revoke"))
	}
}

func (a *App) handleTutorials(ctx context.Context, chatID int64) {
	items, err := a.store.ListTutorials(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	if len(items) == 0 {
		a.sendHTML(chatID, "📚 No tutorials are available yet.", nil)
		return
	}
	var rows [][]premium.InlineButton
	for _, item := range items {
		rows = append(rows, []premium.InlineButton{premium.Button(item.Title, fmt.Sprintf("tutorial:%d", item.ID), "primary", "message")})
	}
	a.sendHTML(chatID, "📚 <b>Tutorials</b>\n\nChoose a guide:", premium.InlineKeyboard{InlineKeyboard: rows})
}

func (a *App) sendTutorial(ctx context.Context, chatID, id int64) {
	item, err := a.store.Tutorial(ctx, a.botInstanceID, id)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	caption := premium.AnimateHTML(fmt.Sprintf("📚 <b>%s</b>\n\n%s\n\n%s", html.EscapeString(item.Title), html.EscapeString(item.Description), item.Body))
	switch item.ContentType {
	case "photo":
		media := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(item.MediaFileID))
		media.Caption, media.ParseMode = caption, tgbotapi.ModeHTML
		_, err = a.bot.Send(media)
	case "video":
		media := tgbotapi.NewVideo(chatID, tgbotapi.FileID(item.MediaFileID))
		media.Caption, media.ParseMode = caption, tgbotapi.ModeHTML
		_, err = a.bot.Send(media)
	default:
		a.sendDocument(chatID, screenDocument{RichHTML: "<h2>" + premium.Emoji("book", "📚") + " " + html.EscapeString(item.Title) + "</h2><p>" + html.EscapeString(item.Description) + "</p><p>" + strings.ReplaceAll(item.Body, "\n", "<br>") + "</p>", ClassicHTML: caption, Keyboard: premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button("Tutorials", "menu:tutorials", "primary", "book"), premium.Button("Home", "menu:home", "", "home")}}}})
	}
	if err != nil {
		a.sendError(chatID, err)
	}
}

func (a *App) handleSettings(ctx context.Context, chatID, userID int64) {
	a.showUserSettings(ctx, chatID, userID)
}

var childTokenPattern = regexp.MustCompile(`^[0-9]{6,14}:[A-Za-z0-9_-]{30,}$`)

func (a *App) handleCreateBot(ctx context.Context, message *tgbotapi.Message, args string) {
	if !a.isMain {
		a.sendHTML(message.Chat.ID, "Child bots cannot create sub-bots.", nil)
		return
	}
	// Tokens are credentials: remove the command before any further processing.
	if message.MessageID != 0 {
		_, _ = a.bot.Request(tgbotapi.NewDeleteMessage(message.Chat.ID, message.MessageID))
	}
	parts := splitExact(args, "|", 2)
	if len(parts) != 2 || !childTokenPattern.MatchString(parts[1]) {
		a.sendHTML(message.Chat.ID, "Usage: <code>/createbot Bot Name|123456:BotFatherToken</code>\nThe credential message is deleted automatically.", nil)
		return
	}
	id, err := a.store.CreateChildBotRequest(ctx, message.From.ID, parts[0], parts[1])
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Child bot request <b>#%d</b> submitted for admin approval. The token is encrypted at rest.", id), nil)
}

func (a *App) handleMyBots(ctx context.Context, chatID, ownerID int64) {
	items, err := a.store.ListBotInstancesForOwner(ctx, ownerID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("🤖 <b>My Child Bots</b>\n")
	if len(items) == 0 {
		text.WriteString("\nNo child bots requested yet.")
	}
	for _, item := range items {
		fmt.Fprintf(&text, "\n#%d · %s · %s\nTier: %s · @%s\n", item.ID, html.EscapeString(item.Name), item.Status, item.Tier, html.EscapeString(item.Username))
	}
	a.sendHTML(chatID, text.String(), userBackMenu())
}

func (a *App) adminSetTier(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 3)
	if len(parts) < 2 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/settier user_id|free/pro/enterprise|2026-12-31</code> (expiry optional)", nil)
		return
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	var expires *time.Time
	if err == nil && len(parts) == 3 && parts[2] != "" {
		value, parseErr := time.ParseInLocation("2006-01-02", parts[2], a.location)
		if parseErr != nil {
			err = parseErr
		} else {
			value = value.Add(24*time.Hour - time.Second)
			expires = &value
		}
	}
	if err == nil {
		err = a.store.SetUserTier(ctx, a.botInstanceID, userID, parts[1], expires, message.From.ID)
	}
	a.respondAdminResult(message.Chat.ID, "User tier updated.", err)
}

func (a *App) adminAddTutorial(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 3)
	if len(parts) != 3 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/tutorialadd title|description|body</code>", nil)
		return
	}
	id, err := a.store.AddTutorial(ctx, domain.Tutorial{BotInstanceID: a.botInstanceID, Title: parts[0], Description: parts[1], Body: parts[2]}, message.From.ID)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Tutorial <b>#%d</b> added.", id), nil)
}

func (a *App) adminListBots(ctx context.Context, chatID int64) {
	a.showBots(ctx, chatID, "all", 0)
}

func (a *App) adminApproveBot(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 2)
	if len(parts) != 2 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/botapprove id|free/pro/enterprise</code>", nil)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err == nil {
		err = a.store.ApproveChildBot(ctx, id, message.From.ID, parts[1])
	}
	a.respondAdminResult(message.Chat.ID, "Child bot approved; the supervisor will start it shortly.", err)
}

func (a *App) adminRejectBot(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 2)
	if len(parts) == 0 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/botreject id|reason</code>", nil)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	reason := "Rejected by administrator"
	if len(parts) == 2 && parts[1] != "" {
		reason = parts[1]
	}
	if err == nil {
		err = a.store.RejectChildBot(ctx, id, message.From.ID, reason)
	}
	a.respondAdminResult(message.Chat.ID, "Child bot rejected.", err)
}

func (a *App) adminToggleBot(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 2)
	if len(parts) != 2 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/bottoggle id|on/off</code>", nil)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err == nil {
		err = a.store.SetChildBotEnabled(ctx, id, strings.EqualFold(parts[1], "on"))
	}
	a.respondAdminResult(message.Chat.ID, "Child bot state updated.", err)
}

func (a *App) adminAddPattern(ctx context.Context, message *tgbotapi.Message, args string) {
	tier, _ := a.store.BotInstanceTier(ctx, a.botInstanceID)
	if !store.TierAllows(tier, "custom_patterns") {
		a.sendHTML(message.Chat.ID, "Custom OTP patterns require an Enterprise bot instance.", nil)
		return
	}
	parts := splitExact(args, "|", 2)
	if len(parts) != 2 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/patternadd name|(?i)code: ([0-9]{6})</code>. The first capture group must contain the OTP.", nil)
		return
	}
	compiled, err := regexp.Compile(parts[1])
	if err != nil || compiled.NumSubexp() < 1 {
		a.sendHTML(message.Chat.ID, "The regex is invalid or has no capture group.", nil)
		return
	}
	id, err := a.store.AddCustomOTPPattern(ctx, a.botInstanceID, message.From.ID, parts[0], parts[1])
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Custom OTP pattern <b>#%d</b> saved.", id), nil)
}

func (a *App) adminListPatterns(ctx context.Context, chatID int64) {
	patterns, err := a.store.ListCustomOTPPatternRows(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("🧩 <b>Custom OTP Patterns</b>\n")
	for _, pattern := range patterns {
		fmt.Fprintf(&text, "\n#%d · <b>%s</b>\n<code>%s</code>\n", pattern.ID, html.EscapeString(pattern.Name), html.EscapeString(pattern.Pattern))
	}
	a.sendHTML(chatID, text.String(), adminPatternsMenu(patterns))
}

func activeMark(active bool) string {
	if active {
		return "ACTIVE"
	}
	return ""
}

func short(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "…"
}

package telegram

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// handleStyledCallback owns the complete premium inline navigation layer. It
// returns false only for callbacks handled by the smaller legacy switch in
// app.go, which keeps command and callback compatibility during migration.
func (a *App) handleStyledCallback(ctx context.Context, callback *tgbotapi.CallbackQuery, admin bool) bool {
	data := callback.Data
	chatID := callback.Message.Chat.ID
	userID := callback.From.ID
	if data == "ignore" {
		return true
	}

	switch data {
	case "menu:top":
		a.handleTop(ctx, chatID)
		return true
	case "menu:referral":
		a.handleReferral(ctx, chatID, userID)
		return true
	case "menu:withdraw":
		a.sendHTML(chatID, "💸 <b>Request a withdrawal</b>\n\nSend <code>/withdraw amount|payment details</code>. The amount is held immediately and refunded automatically if an admin rejects the request.", commandTemplateMenu("Copy withdrawal template", "/withdraw 100|JazzCash 03001234567", "menu:profile"))
		return true
	case "menu:mybots":
		a.handleMyBots(ctx, chatID, userID)
		return true
	}

	if strings.HasPrefix(data, "buy:") {
		a.handleBuyCallback(ctx, callback)
		return true
	}
	if !strings.HasPrefix(data, "admin:") {
		return false
	}
	permission := adminCallbackPermission(data)
	if !admin || permission == "" {
		a.sendHTML(chatID, "🚫 You do not have permission for this admin action.", userBackMenu())
		return true
	}
	allowed, err := a.store.HasAdminPermission(ctx, a.botInstanceID, userID, permission)
	if err != nil || !allowed {
		a.sendHTML(chatID, "🚫 You do not have permission for this admin action.", userBackMenu())
		return true
	}

	parts := strings.Split(data, ":")
	switch {
	case data == "admin:numbers":
		a.sendHTML(chatID, "📂 <b>Number Management</b>\n\nUpload inventory, inspect availability, and monitor assignment totals.", adminNumbersMenu())
	case data == "admin:inventory":
		a.sendAdminInventory(ctx, chatID)
	case data == "admin:numbers:upload":
		a.sendHTML(chatID, "📤 <b>Upload Numbers</b>\n\nAttach a UTF-8 text file and use this command as its caption. Each line or whitespace-separated value is treated as one number.", commandTemplateMenu("Copy upload caption", "/addnumbers WhatsApp|Pakistan|PK|1.00|0|3", "admin:numbers"))
	case data == "admin:broadcast":
		a.sendHTML(chatID, "📢 <b>Broadcast</b>\n\nCopy the template, replace the text, and send it. Delivery is rate-limited and reports sent/failed totals.", commandTemplateMenu("Copy broadcast command", "/broadcast Your announcement", "menu:admin"))
	case data == "admin:users":
		a.sendAdminUsers(ctx, chatID)
	case data == "admin:user:tier-help":
		a.sendHTML(chatID, "💎 <b>Set a user tier</b>\n\nSelect a user from the list or use the command template for an optional expiry date.", commandTemplateMenu("Copy tier command", "/settier 123456789|pro|2026-12-31", "admin:users"))
	case len(parts) == 4 && parts[1] == "user" && parts[2] == "view":
		a.sendAdminUser(ctx, chatID, parts[3])
	case len(parts) == 5 && parts[1] == "user" && parts[2] == "tier":
		target, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid user ID.", adminUsersMenu(nil))
			break
		}
		err := a.store.SetUserTier(ctx, a.botInstanceID, target, parts[4], nil, userID)
		if err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendHTML(chatID, "✅ User tier updated.", userTierMenu(target, parts[4]))
		}
	case data == "admin:withdrawals":
		a.sendAdminWithdrawals(ctx, chatID)
	case len(parts) == 4 && parts[1] == "withdraw" && parts[2] == "view":
		a.sendAdminWithdrawal(ctx, chatID, parts[3])
	case len(parts) == 5 && parts[1] == "withdraw" && parts[2] == "confirm":
		id, ok := parseCallbackInt(parts[3])
		if !ok || (parts[4] != "approve" && parts[4] != "reject") {
			a.sendHTML(chatID, "Invalid withdrawal action.", adminWithdrawalsMenu(nil))
			break
		}
		a.sendHTML(chatID, "⚠️ <b>Confirm withdrawal decision</b>\n\nThis ledger operation is atomic. Rejection refunds the held balance.", confirmationMenu(fmt.Sprintf("admin:withdraw:do:%d:%s", id, parts[4]), fmt.Sprintf("admin:withdraw:view:%d", id)))
	case len(parts) == 5 && parts[1] == "withdraw" && parts[2] == "do":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid withdrawal ID.", adminWithdrawalsMenu(nil))
			break
		}
		err := a.store.ResolveWithdrawalForInstance(ctx, a.botInstanceID, id, userID, parts[4] == "approve")
		if err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendAdminWithdrawals(ctx, chatID)
		}
	case data == "admin:admins":
		a.sendAdminList(ctx, chatID)
	case data == "admin:admin:add":
		a.sendHTML(chatID, "👤 <b>Add an administrator</b>\n\nUse <code>*</code> for full access or a comma-separated permission list.", commandTemplateMenu("Copy add-admin command", "/addadmin 123456789|manage_panels,manage_groups", "admin:admins"))
	case len(parts) == 4 && parts[1] == "admin" && parts[2] == "delete":
		target, ok := parseCallbackInt(parts[3])
		if !ok || target == userID {
			a.sendHTML(chatID, "You cannot remove your own active admin role from this inline action.", adminDashboard())
			break
		}
		a.sendHTML(chatID, "⚠️ <b>Remove administrator?</b>", confirmationMenu(fmt.Sprintf("admin:admin:deleteyes:%d", target), "admin:admins"))
	case len(parts) == 4 && parts[1] == "admin" && parts[2] == "deleteyes":
		target, ok := parseCallbackInt(parts[3])
		if !ok || target == userID {
			a.sendHTML(chatID, "Invalid administrator target.", adminDashboard())
			break
		}
		if err := a.store.RemoveInstanceAdmin(ctx, a.botInstanceID, target); err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendAdminList(ctx, chatID)
		}
	case data == "admin:required":
		a.sendAdminRequired(ctx, chatID)
	case data == "admin:required:add":
		a.sendHTML(chatID, "🔒 <b>Add a required chat</b>\n\nThe bot must be able to inspect membership in the target chat.", commandTemplateMenu("Copy required-chat command", "/addrequired -1001234567890|Updates|https://t.me/yourchannel", "admin:required"))
	case len(parts) == 4 && parts[1] == "required" && parts[2] == "delete":
		id, ok := parseCallbackInt(parts[3])
		if ok {
			a.sendHTML(chatID, "⚠️ <b>Remove this membership requirement?</b>", confirmationMenu(fmt.Sprintf("admin:required:deleteyes:%d", id), "admin:required"))
		}
	case len(parts) == 4 && parts[1] == "required" && parts[2] == "deleteyes":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid chat ID.", adminRequiredMenu(nil))
			break
		}
		if err := a.store.RemoveRequiredChatForInstance(ctx, a.botInstanceID, id); err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendAdminRequired(ctx, chatID)
		}
	case data == "admin:patterns":
		a.adminListPatterns(ctx, chatID)
	case data == "admin:pattern:add":
		a.sendHTML(chatID, "🧩 <b>Add an OTP extraction pattern</b>\n\nThe first RE2 capture group must contain the OTP.", commandTemplateMenu("Copy pattern command", `/patternadd Example|(?i)code[: ]+([0-9]{6})`, "admin:patterns"))
	case len(parts) == 4 && parts[1] == "pattern" && parts[2] == "delete":
		id, ok := parseCallbackInt(parts[3])
		if ok {
			a.sendHTML(chatID, "⚠️ <b>Delete this OTP pattern?</b>", confirmationMenu(fmt.Sprintf("admin:pattern:deleteyes:%d", id), "admin:patterns"))
		}
	case len(parts) == 4 && parts[1] == "pattern" && parts[2] == "deleteyes":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid pattern ID.", adminPatternsMenu(nil))
			break
		}
		if err := a.store.RemoveCustomOTPPattern(ctx, a.botInstanceID, id); err != nil {
			a.sendError(chatID, err)
		} else {
			a.adminListPatterns(ctx, chatID)
		}
	case data == "admin:settings":
		a.sendHTML(chatID, "⚙️ <b>Admin Settings</b>\n\nAll controls are instance-scoped, including child bots.", adminSettingsMenu())
	case data == "admin:themes":
		active, err := a.store.DefaultTheme(ctx, a.botInstanceID)
		if err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendHTML(chatID, "🎨 <b>Default OTP Theme</b>", adminThemesMenu(active))
		}
	case len(parts) == 4 && parts[1] == "theme" && parts[2] == "set":
		themeID, err := strconv.Atoi(parts[3])
		if err == nil {
			err = a.store.SetInstanceTheme(ctx, a.botInstanceID, themeID)
		}
		if err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendHTML(chatID, fmt.Sprintf("✅ Default theme changed to T%d · %s.", themeID, themes.Get(themeID).Name), adminThemesMenu(themeID))
		}
	case data == "admin:panels":
		a.listPanels(ctx, chatID)
	case data == "admin:panel:add":
		a.sendHTML(chatID, "➕ <b>Add an encrypted panel</b>\n\nThe command containing credentials is deleted immediately after processing. The adapter is tested before it is saved.", commandTemplateMenu("Copy panel command", `/addpanel token_api|Provider|2s|{"url":"https://provider/api","token":"replace-me"}`, "admin:panels"))
	case len(parts) == 4 && parts[1] == "panel" && parts[2] == "view":
		a.sendAdminPanel(ctx, chatID, parts[3])
	case len(parts) == 5 && parts[1] == "panel" && parts[2] == "set":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid panel ID.", adminPanelMenu(nil))
			break
		}
		if err := a.store.SetPanelEnabledForInstance(ctx, a.botInstanceID, id, parts[4] == "1"); err != nil {
			a.sendError(chatID, err)
		} else {
			a.listPanels(ctx, chatID)
		}
	case len(parts) == 4 && parts[1] == "panel" && parts[2] == "delete":
		id, ok := parseCallbackInt(parts[3])
		if ok {
			a.sendHTML(chatID, "⚠️ <b>Delete this panel?</b>\n\nIts encrypted configuration and queued raw ingest jobs will be removed. Existing accepted OTP history is retained.", confirmationMenu(fmt.Sprintf("admin:panel:deleteyes:%d", id), fmt.Sprintf("admin:panel:view:%d", id)))
		}
	case len(parts) == 4 && parts[1] == "panel" && parts[2] == "deleteyes":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid panel ID.", adminPanelMenu(nil))
			break
		}
		if err := a.store.RemovePanelForInstance(ctx, a.botInstanceID, id); err != nil {
			a.sendError(chatID, err)
		} else {
			a.listPanels(ctx, chatID)
		}
	case data == "admin:groups":
		a.listGroups(ctx, chatID)
	case data == "admin:group:add":
		a.sendHTML(chatID, "➕ <b>Add an OTP group</b>\n\nAdd the bot to the group first. The bot validates access and sends a test message before saving it.", commandTemplateMenu("Copy add-group command", "/addgroup -1001234567890|buttons|Main OTP Group", "admin:groups"))
	case len(parts) == 4 && parts[1] == "group" && parts[2] == "view":
		a.sendAdminGroup(ctx, chatID, parts[3])
	case len(parts) == 5 && parts[1] == "group" && parts[2] == "enable":
		a.updateAdminGroupBool(ctx, chatID, parts[3], parts[4] == "1", true)
	case len(parts) == 5 && parts[1] == "group" && parts[2] == "buttons":
		a.updateAdminGroupBool(ctx, chatID, parts[3], parts[4] == "1", false)
	case len(parts) == 5 && parts[1] == "group" && parts[2] == "privacy":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid group ID.", adminGroupsMenu(nil))
			break
		}
		if err := a.store.SetOTPGroupPrivacy(ctx, a.botInstanceID, id, parts[4]); err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendAdminGroup(ctx, chatID, parts[3])
		}
	case len(parts) == 4 && parts[1] == "group" && parts[2] == "themes":
		group, ok := a.findGroup(ctx, parts[3])
		if !ok {
			a.sendHTML(chatID, "Group not found.", adminGroupsMenu(nil))
		} else {
			a.sendHTML(chatID, "🎨 <b>Choose this group's OTP theme</b>", groupThemesMenu(group.ChatID, group.ThemeID))
		}
	case len(parts) == 5 && parts[1] == "group" && parts[2] == "theme":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid group ID.", adminGroupsMenu(nil))
			break
		}
		var themeID *int
		if parts[4] != "default" {
			value, parseErr := strconv.Atoi(parts[4])
			if parseErr != nil {
				a.sendHTML(chatID, "Invalid theme.", adminGroupsMenu(nil))
				break
			}
			themeID = &value
		}
		if err := a.store.SetOTPGroupTheme(ctx, a.botInstanceID, id, themeID); err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendAdminGroup(ctx, chatID, parts[3])
		}
	case len(parts) == 4 && parts[1] == "group" && parts[2] == "delete":
		id, ok := parseCallbackInt(parts[3])
		if ok {
			a.sendHTML(chatID, "⚠️ <b>Delete this OTP destination?</b>\n\nNew OTPs will no longer be delivered there.", confirmationMenu(fmt.Sprintf("admin:group:deleteyes:%d", id), fmt.Sprintf("admin:group:view:%d", id)))
		}
	case len(parts) == 4 && parts[1] == "group" && parts[2] == "deleteyes":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid group ID.", adminGroupsMenu(nil))
			break
		}
		if err := a.store.RemoveOTPGroupForInstance(ctx, a.botInstanceID, id); err != nil {
			a.sendError(chatID, err)
		} else {
			a.listGroups(ctx, chatID)
		}
	case data == "admin:rewards":
		a.listRewards(ctx, chatID)
	case data == "admin:reward:set":
		a.sendHTML(chatID, "🎁 <b>Set cumulative daily rewards</b>\n\nA user-specific schedule replaces the global schedule for that user. Progress resets logically at midnight in the configured timezone.", commandTemplateMenu("Copy reward command", "/setrewards global|30=30,100=30,200=50", "admin:rewards"))
	case strings.HasPrefix(data, "admin:reward:"):
		a.sendHTML(chatID, "🎁 Use <code>/setrewards</code> to replace this schedule. Use <code>/clearreward user_id</code> to remove a user override.", commandTemplateMenu("Copy reward command", "/setrewards global|30=30,100=30,200=50", "admin:rewards"))
	case data == "admin:bots":
		a.adminListBots(ctx, chatID)
	case len(parts) == 4 && parts[1] == "bot" && parts[2] == "view":
		a.sendAdminBot(ctx, chatID, parts[3])
	case len(parts) == 5 && parts[1] == "bot" && parts[2] == "approve":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid bot ID.", adminBotsMenu(nil))
			break
		}
		if err := a.store.ApproveChildBot(ctx, id, userID, parts[4]); err != nil {
			a.sendError(chatID, err)
		} else {
			a.adminListBots(ctx, chatID)
		}
	case len(parts) == 5 && parts[1] == "bot" && parts[2] == "set":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid bot ID.", adminBotsMenu(nil))
			break
		}
		if err := a.store.SetChildBotEnabled(ctx, id, parts[4] == "1"); err != nil {
			a.sendError(chatID, err)
		} else {
			a.adminListBots(ctx, chatID)
		}
	case len(parts) == 4 && parts[1] == "bot" && parts[2] == "reject":
		id, ok := parseCallbackInt(parts[3])
		if ok {
			a.sendHTML(chatID, "⚠️ <b>Reject this child-bot request?</b>", confirmationMenu(fmt.Sprintf("admin:bot:rejectyes:%d", id), fmt.Sprintf("admin:bot:view:%d", id)))
		}
	case len(parts) == 4 && parts[1] == "bot" && parts[2] == "rejectyes":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid bot ID.", adminBotsMenu(nil))
			break
		}
		if err := a.store.RejectChildBot(ctx, id, userID, "Rejected from premium admin menu"); err != nil {
			a.sendError(chatID, err)
		} else {
			a.adminListBots(ctx, chatID)
		}
	case data == "admin:tutorials":
		a.sendAdminTutorials(ctx, chatID)
	case data == "admin:tutorial:add":
		a.sendHTML(chatID, "📚 <b>Add a tutorial</b>", commandTemplateMenu("Copy tutorial command", "/tutorialadd Title|Short description|Tutorial body", "admin:tutorials"))
	case len(parts) == 4 && parts[1] == "tutorial" && parts[2] == "delete":
		id, ok := parseCallbackInt(parts[3])
		if ok {
			a.sendHTML(chatID, "⚠️ <b>Delete this tutorial?</b>", confirmationMenu(fmt.Sprintf("admin:tutorial:deleteyes:%d", id), "admin:tutorials"))
		}
	case len(parts) == 4 && parts[1] == "tutorial" && parts[2] == "deleteyes":
		id, ok := parseCallbackInt(parts[3])
		if !ok {
			a.sendHTML(chatID, "Invalid tutorial ID.", adminTutorialsMenu(nil))
			break
		}
		if err := a.store.DeleteTutorial(ctx, a.botInstanceID, id); err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendAdminTutorials(ctx, chatID)
		}
	case data == "admin:analytics":
		a.handleAnalyticsAdmin(ctx, chatID)
	default:
		return false
	}
	return true
}

func (a *App) handleBuyCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	catalog, err := a.store.Catalog(ctx)
	if err != nil {
		a.sendError(callback.Message.Chat.ID, err)
		return
	}
	services := store.SortedServices(catalog)
	parts := strings.Split(callback.Data, ":")
	if len(parts) == 3 && parts[1] == "s" {
		index, err := strconv.Atoi(parts[2])
		if err != nil || index < 0 || index >= len(services) {
			a.sendHTML(callback.Message.Chat.ID, "Inventory changed; please select a service again.", servicesMenu(catalog))
			return
		}
		service := services[index]
		a.sendHTML(callback.Message.Chat.ID, fmt.Sprintf("🌍 <b>%s countries</b>\n\nAvailability is shown on each button.", html.EscapeString(service)), countriesMenu(index, catalog[service]))
		return
	}
	if len(parts) == 4 && parts[1] == "c" {
		serviceIndex, err1 := strconv.Atoi(parts[2])
		countryIndex, err2 := strconv.Atoi(parts[3])
		if err1 != nil || err2 != nil || serviceIndex < 0 || serviceIndex >= len(services) {
			a.sendHTML(callback.Message.Chat.ID, "Inventory changed; please select a service again.", servicesMenu(catalog))
			return
		}
		service := services[serviceIndex]
		countries := catalog[service]
		if countryIndex < 0 || countryIndex >= len(countries) {
			a.sendHTML(callback.Message.Chat.ID, "Inventory changed; please select a country again.", countriesMenu(serviceIndex, countries))
			return
		}
		message := &tgbotapi.Message{Chat: callback.Message.Chat, From: callback.From}
		a.handleGetNumber(ctx, message, service+"|"+countries[countryIndex].Country)
	}
}

func adminCallbackPermission(data string) string {
	switch {
	case strings.HasPrefix(data, "admin:panel"):
		return "manage_panels"
	case strings.HasPrefix(data, "admin:group"):
		return "manage_groups"
	case strings.HasPrefix(data, "admin:reward"):
		return "manage_rewards"
	case strings.HasPrefix(data, "admin:withdraw"):
		return "manage_withdrawals"
	case strings.HasPrefix(data, "admin:bot"):
		return "manage_bots"
	case strings.HasPrefix(data, "admin:tutorial"):
		return "manage_tutorials"
	case strings.HasPrefix(data, "admin:pattern"):
		return "manage_patterns"
	case strings.HasPrefix(data, "admin:admin"):
		return "manage_admins"
	case strings.HasPrefix(data, "admin:user"):
		return "manage_tiers"
	case data == "admin:broadcast":
		return "broadcast"
	case data == "admin:analytics":
		return "view_analytics"
	case strings.HasPrefix(data, "admin:numbers"), data == "admin:inventory", strings.HasPrefix(data, "admin:required"), strings.HasPrefix(data, "admin:setting"), strings.HasPrefix(data, "admin:theme"):
		return "manage_settings"
	default:
		return ""
	}
}

func (a *App) sendAdminUsers(ctx context.Context, chatID int64) {
	items, err := a.store.ListUsersForInstance(ctx, a.botInstanceID, 25)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendHTML(chatID, fmt.Sprintf("👥 <b>Users & Tiers</b>\n\nShowing the top %d users by counted OTPs. Select a user to change their tier.", len(items)), adminUsersMenu(items))
}

func (a *App) sendAdminInventory(ctx context.Context, chatID int64) {
	catalog, err := a.store.Catalog(ctx)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("📂 <b>Inventory</b>\n")
	for _, service := range store.SortedServices(catalog) {
		fmt.Fprintf(&text, "\n<b>%s</b>\n", html.EscapeString(service))
		for _, country := range catalog[service] {
			fmt.Fprintf(&text, "• %s · %d available · %.2f PKR · %d/cycle\n", html.EscapeString(country.Country), country.Available, country.PricePKR, country.PerCycle)
		}
	}
	if len(catalog) == 0 {
		text.WriteString("\nNo inventory has been imported.")
	}
	a.sendHTML(chatID, text.String(), adminNumbersMenu())
}

func (a *App) sendAdminUser(ctx context.Context, chatID int64, rawID string) {
	target, ok := parseCallbackInt(rawID)
	if !ok {
		a.sendHTML(chatID, "Invalid user ID.", adminUsersMenu(nil))
		return
	}
	items, err := a.store.ListUsersForInstance(ctx, a.botInstanceID, 100)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	for _, item := range items {
		if item.UserID == target {
			a.sendHTML(chatID, fmt.Sprintf("👤 <b>User %d</b>\n\nName: %s\nUsername: @%s\nTier: <b>%s</b>\nOTPs: <b>%d</b>\nBalance: <b>%.2f PKR</b>", item.UserID, html.EscapeString(item.FirstName), html.EscapeString(item.Username), html.EscapeString(item.Tier), item.TotalOTPs, item.BalancePKR), userTierMenu(item.UserID, item.Tier))
			return
		}
	}
	a.sendHTML(chatID, "User not found in this bot instance.", adminUsersMenu(items))
}

func (a *App) sendAdminWithdrawals(ctx context.Context, chatID int64) {
	items, err := a.store.ListPendingWithdrawals(ctx, a.botInstanceID, 25)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	text := "💸 <b>Pending Withdrawals</b>"
	if len(items) == 0 {
		text += "\n\nNo pending requests."
	}
	a.sendHTML(chatID, text, adminWithdrawalsMenu(items))
}

func (a *App) sendAdminWithdrawal(ctx context.Context, chatID int64, rawID string) {
	id, ok := parseCallbackInt(rawID)
	if !ok {
		a.sendHTML(chatID, "Invalid withdrawal ID.", adminWithdrawalsMenu(nil))
		return
	}
	items, err := a.store.ListPendingWithdrawals(ctx, a.botInstanceID, 100)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	for _, item := range items {
		if item.ID == id {
			a.sendHTML(chatID, fmt.Sprintf("💸 <b>Withdrawal #%d</b>\n\nUser: <code>%d</code>\nAmount: <b>%.2f PKR / %.4f USD</b>\nMethod: %s\nDetails: %s", item.ID, item.UserID, item.AmountPKR, item.AmountUSD, html.EscapeString(item.Method), html.EscapeString(item.Details)), withdrawalActionsMenu(item.ID))
			return
		}
	}
	a.sendHTML(chatID, "Withdrawal is no longer pending.", adminWithdrawalsMenu(items))
}

func (a *App) sendAdminList(ctx context.Context, chatID int64) {
	items, err := a.store.ListInstanceAdmins(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendHTML(chatID, "👮 <b>Administrators</b>\n\nRoles and permissions are isolated per bot instance.", adminAdminsMenu(items))
}

func (a *App) sendAdminRequired(ctx context.Context, chatID int64) {
	items, err := a.store.ListRequiredChatsForInstance(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendHTML(chatID, "🔒 <b>Required Membership Chats</b>", adminRequiredMenu(items))
}

func (a *App) sendAdminPanel(ctx context.Context, chatID int64, rawID string) {
	id, ok := parseCallbackInt(rawID)
	if !ok {
		a.sendHTML(chatID, "Invalid panel ID.", adminPanelMenu(nil))
		return
	}
	report, err := a.store.PanelHealthReportForInstance(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	for _, panel := range report {
		if panel.ID == id {
			text := fmt.Sprintf("📡 <b>#%d %s</b>\n\nType: <b>%s</b>\nEnabled: <b>%s</b>\nHealthy: <b>%s</b>\nFailures: <b>%d</b>\nOTPs: <b>%d</b>", panel.ID, html.EscapeString(panel.Name), html.EscapeString(panel.Kind), onOff(panel.Enabled), onOff(panel.Healthy), panel.Failures, panel.OTPCount)
			if panel.LastError != "" {
				text += "\nLast error: " + html.EscapeString(panel.LastError)
			}
			a.sendHTML(chatID, text, panelActionsMenu(panel))
			return
		}
	}
	a.sendHTML(chatID, "Panel not found.", adminPanelMenu(report))
}

func (a *App) findGroup(ctx context.Context, rawID string) (domain.OTPGroupDestination, bool) {
	id, ok := parseCallbackInt(rawID)
	if !ok {
		return domain.OTPGroupDestination{}, false
	}
	groups, err := a.store.ListOTPGroupsForInstance(ctx, a.botInstanceID)
	if err != nil {
		return domain.OTPGroupDestination{}, false
	}
	for _, group := range groups {
		if group.ChatID == id {
			return group, true
		}
	}
	return domain.OTPGroupDestination{}, false
}

func (a *App) sendAdminGroup(ctx context.Context, chatID int64, rawID string) {
	group, ok := a.findGroup(ctx, rawID)
	if !ok {
		a.sendHTML(chatID, "Group not found.", adminGroupsMenu(nil))
		return
	}
	theme := "default"
	if group.ThemeID != nil {
		theme = fmt.Sprintf("T%d · %s", *group.ThemeID, themes.Get(*group.ThemeID).Name)
	}
	text := fmt.Sprintf("📨 <b>%s</b>\n\nChat: <code>%d</code>\nEnabled: <b>%s</b>\nButtons: <b>%s</b>\nPrivacy: <b>%s</b>\nTheme: <b>%s</b>\nHealthy: <b>%s</b>", html.EscapeString(group.Title), group.ChatID, onOff(group.Enabled), onOff(group.ButtonsEnabled), html.EscapeString(group.OTPVisibility), html.EscapeString(theme), onOff(group.Healthy))
	if group.LastError != "" {
		text += "\nLast error: " + html.EscapeString(group.LastError)
	}
	a.sendHTML(chatID, text, groupActionsMenu(group))
}

func (a *App) updateAdminGroupBool(ctx context.Context, chatID int64, rawID string, enabled, groupEnabled bool) {
	id, ok := parseCallbackInt(rawID)
	if !ok {
		a.sendHTML(chatID, "Invalid group ID.", adminGroupsMenu(nil))
		return
	}
	var err error
	if groupEnabled {
		err = a.store.SetOTPGroupEnabledForInstance(ctx, a.botInstanceID, id, enabled)
	} else {
		err = a.store.SetOTPGroupButtonsForInstance(ctx, a.botInstanceID, id, enabled)
	}
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendAdminGroup(ctx, chatID, rawID)
}

func (a *App) sendAdminBot(ctx context.Context, chatID int64, rawID string) {
	id, ok := parseCallbackInt(rawID)
	if !ok {
		a.sendHTML(chatID, "Invalid bot ID.", adminBotsMenu(nil))
		return
	}
	items, err := a.store.ListBotInstances(ctx, false)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	for _, item := range items {
		if item.ID != id || item.IsMain {
			continue
		}
		owner := int64(0)
		if item.OwnerUserID != nil {
			owner = *item.OwnerUserID
		}
		text := fmt.Sprintf("🤖 <b>#%d %s</b>\n\nStatus: <b>%s</b>\nTier: <b>%s</b>\nEnabled: <b>%s</b>\nOwner: <code>%d</code>\nUsername: @%s", item.ID, html.EscapeString(item.Name), html.EscapeString(item.Status), html.EscapeString(item.Tier), onOff(item.Enabled), owner, html.EscapeString(item.Username))
		if item.LastError != "" {
			text += "\nLast error: " + html.EscapeString(item.LastError)
		}
		a.sendHTML(chatID, text, botActionsMenu(item))
		return
	}
	a.sendHTML(chatID, "Child bot not found.", adminBotsMenu(items))
}

func (a *App) sendAdminTutorials(ctx context.Context, chatID int64) {
	items, err := a.store.ListTutorials(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendHTML(chatID, "📚 <b>Tutorial Manager</b>", adminTutorialsMenu(items))
}

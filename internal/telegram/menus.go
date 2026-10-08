package telegram

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
)

func userBackMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{
		premium.Button("Main Menu", "menu:home", "primary", "phone"),
	}}}
}

func profileMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("My Numbers", "tools:numbers:0", "primary", "phone"), premium.Button("Filter History", "tools:history:24h:all:0", "primary", "history")},
		{premium.Button("My Stats", "menu:stats", "primary", "chart"), premium.Button("OTP History", "menu:history:0", "success", "history")},
		{premium.Button("Withdraw", "menu:withdraw", "danger", "money"), premium.Button("Referral", "menu:referral", "success", "link")},
		{premium.Button("Leaderboard", "menu:top", "primary", "gold"), premium.Button("Main Menu", "menu:home", "primary", "phone")},
	}}
}

func settingsMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Notification Preferences", "tools:notifications", "primary", "bell")},
		{premium.Button("Choose OTP Theme", "menu:themes", "success", "celebrate")},
		{premium.Button("Webhooks", "menu:webhooks", "primary", "link"), premium.Button("Scheduling", "menu:schedule", "primary", "settings")},
		{premium.Button("API Access", "menu:api", "danger", "developer"), premium.Button("Premium Plan", "menu:premium", "success", "premium")},
		{premium.Button("Main Menu", "menu:home", "primary", "phone")},
	}}
}

func servicesMenu(catalog map[string][]store.CatalogCountry) premium.InlineKeyboard {
	services := store.SortedServices(catalog)
	rows := make([][]premium.InlineButton, 0, (len(services)+1)/2+1)
	for i := 0; i < len(services); i += 2 {
		row := []premium.InlineButton{{Text: services[i], CallbackData: "buy:service:" + selectionKey(services[i]), Style: "primary", IconCustomEmojiID: catalogServiceEmojiID(services[i], catalog[services[i]])}}
		if i+1 < len(services) {
			row = append(row, premium.InlineButton{Text: services[i+1], CallbackData: "buy:service:" + selectionKey(services[i+1]), Style: "primary", IconCustomEmojiID: catalogServiceEmojiID(services[i+1], catalog[services[i+1]])})
		}
		rows = append(rows, row)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Main Menu", "menu:home", "primary", "phone")})
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func catalogServiceEmojiID(service string, countries []store.CatalogCountry) string {
	for _, country := range countries {
		if country.CustomEmojiID != "" {
			return country.CustomEmojiID
		}
	}
	return premium.AppEmojiID(service)
}

func withdrawalMenu(accounts []store.WithdrawalAccount) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(accounts)+3)
	for _, account := range accounts {
		rows = append(rows, []premium.InlineButton{
			premium.Button(withdrawalMethodLabel(account.Method)+" · "+account.DisplayHint, fmt.Sprintf("wd:use:%d", account.ID), "success", withdrawalMethodIcon(account.Method)),
			premium.Button("Delete", fmt.Sprintf("wd:delete:%d", account.ID), "danger", "admin"),
		})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Add Withdrawal Account", "wd:add", "success", "money")},
		[]premium.InlineButton{premium.Button("Refresh Balance", "menu:withdraw", "primary", "chart"), premium.Button("Back", "menu:profile", "primary", "phone")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func withdrawalMethodMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("JazzCash", "wd:addmethod:jazzcash", "success", "money"), premium.Button("Easypaisa", "wd:addmethod:easypaisa", "success", "money")},
		{premium.Button("Binance", "wd:addmethod:binance", "primary", "diamond"), premium.Button("USDT BEP20", "wd:addmethod:usdt_bep20", "primary", "diamond")},
		{premium.Button("Cancel", "flow:cancel", "danger", "admin"), premium.Button("Accounts", "menu:withdraw", "primary", "money")},
	}}
}

func withdrawalConfirmMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Confirm Request", "wd:confirm", "success", "check"), premium.Button("Cancel", "flow:cancel", "danger", "admin")},
	}}
}

func uploadServicesMenu() premium.InlineKeyboard {
	apps := premium.DefaultApps()
	rows := make([][]premium.InlineButton, 0, (len(apps)+1)/2+2)
	for i := 0; i < len(apps); i += 2 {
		row := []premium.InlineButton{{Text: apps[i].Name, CallbackData: fmt.Sprintf("admin:upload:service:%d", i), Style: "primary", IconCustomEmojiID: apps[i].CustomEmojiID}}
		if i+1 < len(apps) {
			row = append(row, premium.InlineButton{Text: apps[i+1].Name, CallbackData: fmt.Sprintf("admin:upload:service:%d", i+1), Style: "primary", IconCustomEmojiID: apps[i+1].CustomEmojiID})
		}
		rows = append(rows, row)
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Select Multiple Apps", "admin:upload:multi", "primary", "app")},
		[]premium.InlineButton{premium.Button("Other App", "admin:upload:service:other", "success", "developer")},
		[]premium.InlineButton{premium.Button("Cancel Import", "flow:cancel", "danger", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func uploadPricingMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Use Defaults", "admin:upload:pricing:default", "success", "check")},
		{premium.Button("Cancel Import", "flow:cancel", "danger", "admin")},
	}}
}

func uploadConfirmMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Import Numbers", "admin:upload:confirm", "success", "number"), premium.Button("Cancel", "flow:cancel", "danger", "admin")},
	}}
}

func panelKindMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Login Panel", "admin:panel:kind:login", "success", "key"), premium.Button("CR API", "admin:panel:kind:token", "primary", "link")},
		{premium.Button("Reseller API", "admin:panel:kind:legacy", "primary", "developer"), premium.Button("Live SMS Stream", "admin:panel:kind:stream", "success", "live")},
		{premium.Button("ASP SMS API", "admin:panel:kind:axon_asp", "primary", "plug"), premium.Button("IPRN REST API", "admin:panel:kind:augestel", "primary", "developer")},
		{premium.Button("Cancel", "flow:cancel", "danger", "admin")},
	}}
}

func streamMethodsMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("IVAS Account", "admin:panel:kind:ivas", "primary", "key"), premium.Button("Stream URL", "admin:panel:kind:socketio", "success", "live")},
		{premium.Button("Plain WebSocket · Advanced", "admin:panel:kind:ws", "primary", "satellite")},
		{premium.Button("Back", "admin:panel:kind:types", "primary", "back"), premium.Button("Cancel", "flow:cancel", "danger", "cancel")},
	}}
}

func flowCancelMenu(backCallback string) premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Cancel", "flow:cancel", "danger", "admin"), premium.Button("Back", backCallback, "primary", "phone")},
	}}
}

func withdrawalMethodLabel(method string) string {
	switch method {
	case "jazzcash":
		return "JazzCash"
	case "easypaisa":
		return "Easypaisa"
	case "binance":
		return "Binance"
	case "usdt_bep20":
		return "USDT BEP20"
	default:
		return method
	}
}

func withdrawalMethodIcon(method string) string {
	if method == "binance" || method == "usdt_bep20" {
		return "diamond"
	}
	return "money"
}

func countriesMenu(service string, countries []store.CatalogCountry) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, (len(countries)+1)/2+1)
	for i := 0; i < len(countries); i += 2 {
		label := fmt.Sprintf("%s · %d", countries[i].Country, countries[i].Available)
		style := "primary"
		if countries[i].Available > 0 {
			style = "success"
		}
		row := []premium.InlineButton{{Text: label, CallbackData: "buy:country:" + selectionKey(service) + ":" + selectionKey(countries[i].Country), Style: style, IconCustomEmojiID: premium.CountryEmojiID(countries[i].CountryCode)}}
		if i+1 < len(countries) {
			label = fmt.Sprintf("%s · %d", countries[i+1].Country, countries[i+1].Available)
			style = "primary"
			if countries[i+1].Available > 0 {
				style = "success"
			}
			row = append(row, premium.InlineButton{Text: label, CallbackData: "buy:country:" + selectionKey(service) + ":" + selectionKey(countries[i+1].Country), Style: style, IconCustomEmojiID: premium.CountryEmojiID(countries[i+1].CountryCode)})
		}
		rows = append(rows, row)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Change Service", "menu:services", "primary", "phone"), premium.Button("Main Menu", "menu:home", "primary", "phone")})
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func assignmentMenu(service, country string) premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Get More Numbers", "buy:country:"+selectionKey(service)+":"+selectionKey(country), "success", "number")},
		{premium.Button("Save Favorite", "tools:add:"+selectionKey(service)+":"+selectionKey(country), "primary", "favorite"), premium.Button("Watch Availability", "tools:watch:"+selectionKey(service)+":"+selectionKey(country), "primary", "bell")},
		{premium.Button("Change Country", "buy:service:"+selectionKey(service), "primary", "globe"), premium.Button("Change App", "menu:services", "primary", "app")},
		{premium.Button("Live OTP", "menu:liveotp", "primary", "live"), premium.Button("Main Menu", "menu:home", "", "home")},
	}}
}

func adminNumbersMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Inventory", "admin:inventory", "primary", "phone"), premium.Button("Upload Numbers", "admin:numbers:upload", "success", "number")},
		{premium.Button("Import History", "admin:imports:list:0", "primary", "history")},
		{premium.Button("Statistics", "admin:analytics", "success", "chart"), premium.Button("Back", "menu:admin", "primary", "admin")},
	}}
}

func adminPanelMenu(report []store.PanelHealth) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(report)+2)
	for _, panel := range report {
		status, style := "OFF", "danger"
		if panel.Enabled {
			status, style = "ON", "success"
		}
		rows = append(rows, []premium.InlineButton{
			premium.Button(fmt.Sprintf("#%d %s", panel.ID, panel.Name), fmt.Sprintf("admin:panel:view:%d", panel.ID), "primary", "chart"),
			premium.Button(status, fmt.Sprintf("admin:panel:set:%d:%d", panel.ID, boolInt(!panel.Enabled)), style, "check"),
		})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Add Panel", "admin:panel:add", "success", "chart"), premium.Button("Refresh", "admin:panels", "primary", "chart")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func panelActionsMenu(panel store.PanelHealth) premium.InlineKeyboard {
	state, next := "Disable", 0
	style := "danger"
	if !panel.Enabled {
		state, next, style = "Enable", 1, "success"
	}
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button(state, fmt.Sprintf("admin:panel:set:%d:%d", panel.ID, next), style, "check"), premium.Button("Test Now", fmt.Sprintf("admin:panel:test:%d", panel.ID), "primary", "bolt")},
		{premium.Button("Diagnostics & Tools", fmt.Sprintf("admin:panel:diagnostics:%d", panel.ID), "primary", "satellite")},
		{premium.Button("Refresh", fmt.Sprintf("admin:panel:view:%d", panel.ID), "primary", "chart")},
		{premium.Button("Delete Panel", fmt.Sprintf("admin:panel:delete:%d", panel.ID), "danger", "admin")},
		{premium.Button("Panel List", "admin:panels", "primary", "chart"), premium.Button("Admin Home", "menu:admin", "primary", "admin")},
	}}
}

func adminGroupsMenu(groups []domain.OTPGroupDestination) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(groups)+2)
	for _, group := range groups {
		style := "danger"
		if group.Enabled && group.Healthy {
			style = "success"
		} else if group.Enabled {
			style = "primary"
		}
		rows = append(rows, []premium.InlineButton{
			premium.Button(group.Title, fmt.Sprintf("admin:group:view:%d", group.ChatID), style, "channel"),
		})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Add OTP Group", "admin:group:add", "success", "channel"), premium.Button("Refresh", "admin:groups", "primary", "chart")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func groupActionsMenu(group domain.OTPGroupDestination) premium.InlineKeyboard {
	enabledLabel, enabledNext, enabledStyle := "Disable Group", 0, "danger"
	if !group.Enabled {
		enabledLabel, enabledNext, enabledStyle = "Enable Group", 1, "success"
	}
	buttonLabel, buttonNext, buttonStyle := "Disable Buttons", 0, "danger"
	if !group.ButtonsEnabled {
		buttonLabel, buttonNext, buttonStyle = "Enable Buttons", 1, "success"
	}
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button(enabledLabel, fmt.Sprintf("admin:group:enable:%d:%d", group.ChatID, enabledNext), enabledStyle, "check"), premium.Button(buttonLabel, fmt.Sprintf("admin:group:buttons:%d:%d", group.ChatID, buttonNext), buttonStyle, "otp")},
		{premium.Button("Visible display", fmt.Sprintf("admin:group:privacy:%d:visible", group.ChatID), activeStyle(group.OTPVisibility == "visible"), "otp"), premium.Button("Masked display", fmt.Sprintf("admin:group:privacy:%d:masked", group.ChatID), activeStyle(group.OTPVisibility == "masked"), "lock")},
		{premium.Button("Hidden display", fmt.Sprintf("admin:group:privacy:%d:hidden", group.ChatID), activeStyle(group.OTPVisibility == "hidden"), "lock")},
		{premium.Button("Choose Theme", fmt.Sprintf("admin:group:themes:%d", group.ChatID), "primary", "celebrate"), premium.Button("Delete Group", fmt.Sprintf("admin:group:delete:%d", group.ChatID), "danger", "admin")},
		{premium.Button("Group List", "admin:groups", "primary", "channel"), premium.Button("Admin Home", "menu:admin", "primary", "admin")},
	}}
}

func groupThemesMenu(chatID int64, active *int) premium.InlineKeyboard {
	rows := [][]premium.InlineButton{{premium.Button("Use Default", fmt.Sprintf("admin:group:theme:%d:default", chatID), activeStyle(active == nil), "settings")}}
	for i := 0; i < 10; i += 2 {
		left, right := themes.Get(i), themes.Get(i+1)
		rows = append(rows, []premium.InlineButton{
			premium.Button(fmt.Sprintf("T%d %s", i, left.Name), fmt.Sprintf("admin:group:theme:%d:%d", chatID, i), activeStyle(active != nil && *active == i), "celebrate"),
			premium.Button(fmt.Sprintf("T%d %s", i+1, right.Name), fmt.Sprintf("admin:group:theme:%d:%d", chatID, i+1), activeStyle(active != nil && *active == i+1), "celebrate"),
		})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Back", fmt.Sprintf("admin:group:view:%d", chatID), "primary", "channel")})
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func adminRewardsMenu(schedules []domain.RewardSchedule) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(schedules)+2)
	for _, schedule := range schedules {
		label := "Global Rewards"
		callback := "admin:reward:global"
		if schedule.UserID != nil {
			label = fmt.Sprintf("User %d", *schedule.UserID)
			callback = fmt.Sprintf("admin:reward:user:%d", *schedule.UserID)
		}
		rows = append(rows, []premium.InlineButton{premium.Button(label, callback, "success", "money")})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Set Rewards", "admin:reward:set", "success", "money"), premium.Button("Refresh", "admin:rewards", "primary", "chart")},
		[]premium.InlineButton{premium.Button("Limited Reward", "admin:reward:limited", "success", "gift")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func adminUsersMenu(items []store.InstanceUser) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(items)+2)
	for _, item := range items {
		name := item.FirstName
		if item.Username != "" {
			name = "@" + item.Username
		}
		if name == "" {
			name = strconv.FormatInt(item.UserID, 10)
		}
		rows = append(rows, []premium.InlineButton{premium.Button(
			fmt.Sprintf("%s · %s · %d OTP", name, item.Tier, item.TotalOTPs),
			fmt.Sprintf("admin:user:view:%d", item.UserID), "primary", "premium",
		)})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Set User Tier", "admin:user:tier-help", "success", "premium"), premium.Button("Refresh", "admin:users", "primary", "chart")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func userTierMenu(userID int64, active string) premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Free", fmt.Sprintf("admin:user:tier:%d:free", userID), activeStyle(active == "free"), "phone"), premium.Button("Pro", fmt.Sprintf("admin:user:tier:%d:pro", userID), activeStyle(active == "pro"), "premium")},
		{premium.Button("Enterprise", fmt.Sprintf("admin:user:tier:%d:enterprise", userID), activeStyle(active == "enterprise"), "gold")},
		{premium.Button("Users", "admin:users", "primary", "premium"), premium.Button("Admin Home", "menu:admin", "primary", "admin")},
	}}
}

func adminWithdrawalsMenu(items []store.Withdrawal) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(items)+2)
	for _, item := range items {
		amount := fmt.Sprintf("%.2f PKR", item.AmountPKR)
		if item.AmountUSD > 0 {
			amount = fmt.Sprintf("%.4f USD", item.AmountUSD)
		}
		rows = append(rows, []premium.InlineButton{
			premium.Button(fmt.Sprintf("#%d · %s · user %d", item.ID, amount, item.UserID), fmt.Sprintf("admin:withdraw:view:%d", item.ID), "primary", "money"),
		})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Refresh", "admin:withdrawals", "primary", "chart"), premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func withdrawalActionsMenu(id int64) premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Approve", fmt.Sprintf("admin:withdraw:confirm:%d:approve", id), "success", "check"), premium.Button("Reject", fmt.Sprintf("admin:withdraw:confirm:%d:reject", id), "danger", "admin")},
		{premium.Button("Pending List", "admin:withdrawals", "primary", "money"), premium.Button("Admin Home", "menu:admin", "primary", "admin")},
	}}
}

func adminAdminsMenu(items []store.InstanceAdmin) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(items)+2)
	for _, item := range items {
		name := item.FirstName
		if item.Username != "" {
			name = "@" + item.Username
		}
		if name == "" {
			name = strconv.FormatInt(item.UserID, 10)
		}
		rows = append(rows, []premium.InlineButton{
			premium.Button(name, "ignore", "primary", "admin"),
			premium.Button("Remove", fmt.Sprintf("admin:admin:delete:%d", item.UserID), "danger", "admin"),
		})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Add Admin", "admin:admin:add", "success", "admin"), premium.Button("Refresh", "admin:admins", "primary", "chart")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func adminBotsMenu(items []domain.BotInstance) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(items)+2)
	for _, item := range items {
		if item.IsMain {
			continue
		}
		style := "danger"
		if item.Status == "running" {
			style = "success"
		} else if item.Status == "approved" || item.Status == "pending" {
			style = "primary"
		}
		rows = append(rows, []premium.InlineButton{premium.Button(fmt.Sprintf("#%d %s · %s", item.ID, item.Name, item.Status), fmt.Sprintf("admin:bot:view:%d", item.ID), style, "bot")})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Refresh", "admin:bots", "primary", "chart"), premium.Button("Create Bot Guide", "menu:createbot", "success", "bot")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func botActionsMenu(item domain.BotInstance) premium.InlineKeyboard {
	rows := [][]premium.InlineButton{}
	if item.Status == "pending" || item.Status == "rejected" {
		rows = append(rows,
			[]premium.InlineButton{premium.Button("Approve Free", fmt.Sprintf("admin:bot:approve:%d:free", item.ID), "primary", "bot"), premium.Button("Approve Pro", fmt.Sprintf("admin:bot:approve:%d:pro", item.ID), "success", "premium")},
			[]premium.InlineButton{premium.Button("Approve Enterprise", fmt.Sprintf("admin:bot:approve:%d:enterprise", item.ID), "success", "gold"), premium.Button("Reject", fmt.Sprintf("admin:bot:reject:%d", item.ID), "danger", "admin")},
		)
	} else {
		if item.Status == "error" {
			rows = append(rows, []premium.InlineButton{premium.Button("Retry Connection", fmt.Sprintf("admin:bot:set:%d:1", item.ID), "success", "refresh")})
		}
		label, next, style := "Stop Bot", 0, "danger"
		if !item.Enabled {
			label, next, style = "Start Bot", 1, "success"
		}
		rows = append(rows, []premium.InlineButton{premium.Button(label, fmt.Sprintf("admin:bot:set:%d:%d", item.ID, next), style, "bot")})
	}
	label, next, style := "Enable Main OTP Sharing", 1, "success"
	if item.ShareMainOTPs {
		label, next, style = "Disable Main OTP Sharing", 0, "danger"
	}
	rows = append(rows, []premium.InlineButton{premium.Button(label, fmt.Sprintf("admin:bot:otpshare:%d:%d", item.ID, next), style, "otp")})
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Refresh", fmt.Sprintf("admin:bot:view:%d", item.ID), "primary", "chart"), premium.Button("Bot List", "admin:bots", "primary", "bot")},
		[]premium.InlineButton{premium.Button("Admin Home", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func adminPatternsMenu(items []store.CustomOTPPattern) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(items)+2)
	for _, item := range items {
		rows = append(rows, []premium.InlineButton{
			premium.Button(fmt.Sprintf("#%d %s", item.ID, item.Name), "ignore", "primary", "otp"),
			premium.Button("Delete", fmt.Sprintf("admin:pattern:delete:%d", item.ID), "danger", "admin"),
		})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Add Pattern", "admin:pattern:add", "success", "otp"), premium.Button("Refresh", "admin:patterns", "primary", "chart")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func adminRequiredMenu(items []store.RequiredChat) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(items)+2)
	for _, item := range items {
		rows = append(rows, []premium.InlineButton{
			{Text: item.Title, URL: item.InviteURL, Style: "primary", IconCustomEmojiID: premium.ID("channel")},
			premium.Button("Remove", fmt.Sprintf("admin:required:delete:%d", item.ChatID), "danger", "admin"),
		})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Add Required Chat", "admin:required:add", "success", "channel"), premium.Button("Refresh", "admin:required", "primary", "chart")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func adminTutorialsMenu(items []domain.Tutorial) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, len(items)+2)
	for _, item := range items {
		rows = append(rows, []premium.InlineButton{
			premium.Button(item.Title, fmt.Sprintf("tutorial:%d", item.ID), "primary", "message"),
			premium.Button("Delete", fmt.Sprintf("admin:tutorial:delete:%d", item.ID), "danger", "admin"),
		})
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Add Tutorial", "admin:tutorial:add", "success", "message"), premium.Button("Refresh", "admin:tutorials", "primary", "chart")},
		[]premium.InlineButton{premium.Button("Back", "menu:admin", "primary", "admin")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func adminSettingsMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Default OTP Theme", "admin:themes", "success", "celebrate"), premium.Button("Required Chats", "admin:required", "primary", "lock")},
		{premium.Button("OTP Patterns", "admin:patterns", "primary", "otp"), premium.Button("Panels", "admin:panels", "primary", "chart")},
		{premium.Button("System Analytics", "admin:analytics", "success", "chart"), premium.Button("Broadcast", "admin:broadcast", "danger", "channel")},
		{premium.Button("Back", "menu:admin", "primary", "admin")},
	}}
}

func adminThemesMenu(active int) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, 0, 6)
	for i := 0; i < 10; i += 2 {
		rows = append(rows, []premium.InlineButton{
			premium.Button(fmt.Sprintf("T%d %s", i, themes.Get(i).Name), fmt.Sprintf("admin:theme:set:%d", i), activeStyle(active == i), "celebrate"),
			premium.Button(fmt.Sprintf("T%d %s", i+1, themes.Get(i+1).Name), fmt.Sprintf("admin:theme:set:%d", i+1), activeStyle(active == i+1), "celebrate"),
		})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Back", "admin:settings", "primary", "settings")})
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func confirmationMenu(confirmCallback, cancelCallback string) premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{
		premium.Button("Confirm", confirmCallback, "danger", "check"),
		premium.Button("Cancel", cancelCallback, "success", "support"),
	}}}
}

func commandTemplateMenu(label, command, backCallback string) premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{{Text: label, CopyText: &premium.CopyText{Text: command}, Style: "success", IconCustomEmojiID: premium.ID("copy")}},
		{premium.Button("Back", backCallback, "primary", "admin")},
	}}
}

func activeStyle(active bool) string {
	if active {
		return "success"
	}
	return "primary"
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseCallbackInt(value string) (int64, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil
}

// Stable across catalog insertion, deletion and reordering.
func selectionKey(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:24] }

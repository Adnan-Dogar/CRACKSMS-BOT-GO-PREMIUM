package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type commandDefinition struct {
	Name, Description, Action, Permission string
	MainOnly                              bool
}

var commandRegistry = buildCommandRegistry()

func buildCommandRegistry() []commandDefinition {
	user := []commandDefinition{
		{"mynumbers", "Your active and recent number assignments", "tools:numbers:0", "", false},
		{"search", "Search applications and countries", "tools:search", "", false},
		{"notifications", "Availability alert and quiet hour preferences", "tools:notifications", "", false},
		{"start", "Open the main menu", "menu:home", "", false}, {"help", "Guides and troubleshooting", "menu:help", "", false},
		{"services", "Choose an application and country", "menu:services", "", false}, {"getnumber", "Get a number", "menu:services", "", false},
		{"liveotp", "Your OTPs and live activity", "menu:liveotp", "", false}, {"topapps", "Applications ranked by received OTPs", "activity:apps:24h:0", "", false},
		{"topcountries", "Countries ranked by received OTPs", "activity:topcountries:24h:0", "", false}, {"favorites", "Saved application and country selections", "tools:favorites:0", "", false},
		{"lastselection", "Repeat your last selection", "tools:last", "", false}, {"alerts", "Number availability alerts", "tools:alerts:0", "", false},
		{"balance", "View your account balance", "menu:profile", "", false}, {"account", "View your account", "menu:profile", "", false},
		{"mystats", "Your received OTP statistics", "menu:stats", "", false}, {"myprofile", "Your profile statistics", "menu:stats", "", false},
		{"myhistory", "Your OTP history", "menu:history:0", "", false}, {"top", "User leaderboard", "menu:top", "", false},
		{"referral", "Your referral link and rewards", "menu:referral", "", false}, {"withdraw", "Manage payout accounts and withdrawals", "menu:withdraw", "", false},
		{"theme", "Choose an OTP theme", "menu:themes", "", false}, {"otpguipreview", "Preview all OTP themes", "", "", false},
		{"premium", "View your subscription features", "menu:premium", "", false}, {"analytics", "Your advanced analytics", "menu:analytics", "", false},
		{"webhook", "Manage your webhooks", "menu:webhooks", "", false}, {"schedule", "Manage scheduled messages", "menu:schedule", "", false},
		{"apikey", "Manage your API keys", "menu:api", "", false}, {"tutorials", "Read tutorials", "menu:tutorials", "", false},
		{"settings", "Menu, display and timezone preferences", "menu:settings", "", false}, {"createbot", "Create a child bot", "menu:createbot", "", true},
		{"bots", "Your child bot requests", "menu:mybots", "", true}, {"cancel", "Cancel the current form", "flow:cancel", "", false},
		{"admin", "Open your administration tools", "menu:admin", "@admin", false},
	}
	groups := []struct {
		Names, Permission, Action, Description string
		Main                                   bool
	}{
		{"addgroup", "manage_groups", "admin:group:add", "Add an OTP destination", false},
		{"groups groupbuttons groupenable groupprivacy grouptheme removegroup", "manage_groups", "admin:groups", "Manage OTP destinations", false},
		{"setrewards", "manage_rewards", "admin:reward:set", "Configure daily rewards", false}, {"rewards", "manage_rewards", "admin:rewards", "View reward schedules", false},
		{"limitedreward", "manage_rewards", "guide:start:limited_reward", "Reward the first N users at an OTP target", false},
		{"clearreward", "manage_rewards", "guide:start:clearreward", "Remove a reward override", false},
		{"addpanel", "manage_panels", "admin:panel:add", "Add a panel and connect accounts", false},
		{"paneltest syncnumbers servicemappings", "manage_panels", "admin:panels", "Provider tests, inventory and service mappings", false},
		{"panels panelhealth paneltoggle", "manage_panels", "admin:panels", "Manage panels and connection health", false},
		{"addrequired", "manage_settings", "admin:required:add", "Add a membership requirement", false},
		{"required removerequired", "manage_settings", "admin:required", "Manage required chats", false},
		{"settheme", "manage_settings", "admin:themes", "Choose the default OTP theme", false},
		{"imports", "manage_settings", "admin:imports:list:0", "Review, retry or cancel number imports", false},
		{"addadmin", "manage_admins", "admin:admin:add", "Add an administrator", false}, {"removeadmin", "manage_admins", "admin:admins", "Manage administrators", false},
		{"withdrawapprove withdrawreject", "manage_withdrawals", "admin:withdrawals", "Review withdrawal requests", false},
		{"backupusers", "manage_backups", "admin:backups", "Create an encrypted user backup", true},
		{"restoreusers", "manage_backups", "admin:backups:upload:command", "Upload and review a user backup", true},
		{"broadcast", "broadcast", "admin:broadcast", "Compose and preview a broadcast", false},
		{"settier", "manage_tiers", "admin:user:tier-help", "Set a user subscription tier", false},
		{"tutorialadd", "manage_tutorials", "admin:tutorial:add", "Add a tutorial", false}, {"tutorialremove", "manage_tutorials", "admin:tutorials", "Manage tutorials", false},
		{"botinstances botapprove botreject bottoggle", "manage_bots", "admin:bots", "Manage child bot requests", true},
		{"systemstats stats", "view_analytics", "admin:analytics", "View bot statistics", false},
		{"patternadd", "manage_patterns", "admin:pattern:add", "Add an OTP extraction pattern", false},
		{"patterns patternremove", "manage_patterns", "admin:patterns", "Manage OTP extraction patterns", false},
	}
	for _, g := range groups {
		for _, name := range strings.Fields(g.Names) {
			user = append(user, commandDefinition{name, g.Description, g.Action, g.Permission, g.Main})
		}
	}
	return user
}

func adminCommandPermission(command string) string {
	for _, definition := range commandRegistry {
		if definition.Name == command && definition.Permission != "@admin" {
			return definition.Permission
		}
	}
	return ""
}

func (a *App) commandMenu(ctx context.Context, user int64) ([]tgbotapi.BotCommand, error) {
	admin := false
	if user != 0 {
		var err error
		admin, err = a.store.HasAnyAdminRole(ctx, a.botInstanceID, user)
		if err != nil {
			return nil, err
		}
	}
	permissions := map[string]bool{"@admin": admin}
	out := []tgbotapi.BotCommand{}
	for _, d := range commandRegistry {
		if d.MainOnly && !a.isMain {
			continue
		}
		if d.Permission != "" {
			if !admin {
				continue
			}
			allowed, known := permissions[d.Permission]
			if !known {
				var err error
				allowed, err = a.store.HasAdminPermission(ctx, a.botInstanceID, user, d.Permission)
				if err != nil {
					return nil, err
				}
				permissions[d.Permission] = allowed
			}
			if !allowed {
				continue
			}
		}
		out = append(out, tgbotapi.BotCommand{Command: d.Name, Description: d.Description})
	}
	return out, nil
}
func (a *App) syncCommandMenu(ctx context.Context, user int64) error {
	commands, err := a.commandMenu(ctx, user)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(commands)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	a.ui.mu.Lock()
	old := a.ui.commandHashes[user]
	a.ui.mu.Unlock()
	if old == hash {
		return nil
	}
	scope := map[string]any{"type": "all_private_chats"}
	if user != 0 {
		scope = map[string]any{"type": "chat", "chat_id": user}
	}
	p := tgbotapi.Params{"commands": string(raw)}
	if err = p.AddInterface("scope", scope); err != nil {
		return err
	}
	if _, err = a.bot.MakeRequest("setMyCommands", p); err != nil {
		return err
	}
	a.ui.mu.Lock()
	a.ui.commandHashes[user] = hash
	a.ui.mu.Unlock()
	if user != 0 {
		_, err = a.store.Pool().Exec(ctx, `INSERT INTO telegram_command_scopes(bot_instance_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, a.botInstanceID, user)
	}
	return err
}
func (a *App) registerCommands(ctx context.Context) error {
	if e := a.syncGroupCommands(ctx, 0, 0); e != nil {
		return e
	}
	if err := a.syncCommandMenu(ctx, 0); err != nil {
		return err
	}
	if _, err := a.bot.MakeRequest("setChatMenuButton", tgbotapi.Params{"menu_button": `{"type":"commands"}`}); err != nil {
		return err
	}
	admins, err := a.store.ListInstanceAdmins(ctx, a.botInstanceID)
	if err != nil {
		return err
	}
	users := map[int64]bool{}
	for _, v := range admins {
		users[v.UserID] = true
	}
	rows, err := a.store.Pool().Query(ctx, `SELECT user_id FROM telegram_command_scopes WHERE bot_instance_id=$1`, a.botInstanceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		users[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for user := range users {
		if err = a.syncCommandMenu(ctx, user); err != nil {
			slog.Warn("private command menu unavailable", "bot_instance", a.botInstanceID, "error_type", fmt.Sprintf("%T", err))
		}
	}
	return nil
}

func (a *App) handleCommandEntry(ctx context.Context, m *tgbotapi.Message, command, args string) bool {
	if args != "" || command == "start" || command == "cancel" {
		return false
	}
	for _, d := range commandRegistry {
		if d.Name == command && d.Action != "" {
			if d.MainOnly && !a.isMain {
				a.sendHTML(m.Chat.ID, "This command is available in the main bot.", userBackMenu())
				return true
			}
			action := d.Action
			if command == "restoreusers" {
				action = "admin:backups:upload:" + broadcastKey()
			}
			a.handleCallback(ctx, &tgbotapi.CallbackQuery{From: m.From, Message: &tgbotapi.Message{Chat: m.Chat}, Data: action})
			return true
		}
	}
	return false
}

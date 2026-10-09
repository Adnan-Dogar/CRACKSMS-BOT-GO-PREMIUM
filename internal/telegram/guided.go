package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	webhookservice "github.com/adnan-dogar/cracksms-vnext/internal/webhook"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type guidedField struct {
	Label, Kind string
	Choices     []string
}
type guidedAction struct {
	Title, Command, Prefix, Back, Feature string
	Fields                                []guidedField
}

var guidedActions = map[string]guidedAction{
	"group":          {"Add OTP Group", "addgroup", "", "admin:groups", "", []guidedField{{"Group chat ID (add the bot to the group first)", "chat", nil}, {"OTP buttons", "enum", []string{"buttons", "nobuttons"}}, {"Group title", "text", nil}}},
	"reward":         {"Set Daily Rewards", "setrewards", "", "admin:rewards", "", []guidedField{{"Recipient: global or Telegram user ID", "recipient", []string{"global"}}, {"Milestones, e.g. 30=30,100=50", "rewards", nil}}},
	"limited_reward": {"Limited Daily Reward · replaces global schedule", "limitedreward", "", "admin:rewards", "", []guidedField{{"Counted OTP target per user per day, e.g. 1000", "positive_int", nil}, {"Number of users who can receive it, e.g. 5", "positive_int", nil}, {"Reward per qualifying user, e.g. 2 USD or 500 PKR", "reward_amount", nil}}},
	"clearreward":    {"Remove Reward Override", "clearreward", "", "admin:rewards", "", []guidedField{{"Telegram user ID", "id", nil}}},
	"admin":          {"Add Administrator", "addadmin", "", "admin:admins", "", []guidedField{{"Telegram user ID", "id", nil}, {"Permissions: * or comma-separated permission names", "permissions", nil}}},
	"required":       {"Add Required Chat", "addrequired", "", "admin:required", "", []guidedField{{"Chat ID", "chat", nil}, {"Chat title", "text", nil}, {"HTTPS invite link", "https", nil}}},
	"tutorial":       {"Add Tutorial", "tutorialadd", "", "admin:tutorials", "", []guidedField{{"Title", "text", nil}, {"Description", "text", nil}, {"Tutorial text", "body", nil}}},
	"broadcast":      {"Broadcast", "broadcast", "", "menu:admin", "", []guidedField{{"Announcement text (Telegram HTML supported)", "body", nil}}},
	"tier":           {"Set User Tier", "settier", "", "admin:users", "", []guidedField{{"Telegram user ID", "id", nil}, {"Tier", "enum", []string{"free", "pro", "enterprise"}}, {"Expiry date YYYY-MM-DD, or none", "date", []string{"none"}}}},
	"pattern":        {"Add OTP Pattern", "patternadd", "", "admin:patterns", "custom_patterns", []guidedField{{"Pattern name", "text", nil}, {"RE2 expression; first capture group contains the OTP", "regex", nil}}},
	"child":          {"Create My Bot", "createbot", "", "menu:home", "", []guidedField{{"Bot name", "text", nil}, {"BotFather token (your message will be deleted)", "secret", nil}}},
	"webhook":        {"Add Webhook", "webhook", "add", "menu:webhooks", "webhooks", []guidedField{{"Public HTTPS endpoint", "webhook", nil}, {"Events, separated by commas", "events", []string{"otp.received"}}}},
	"unhook":         {"Remove Webhook", "webhook", "remove", "menu:webhooks", "webhooks", []guidedField{{"Webhook ID", "id", nil}}},
	"schedule":       {"Schedule Message", "schedule", "add", "menu:schedule", "scheduling", []guidedField{{"Time YYYY-MM-DD HH:MM in the bot timezone", "time", nil}, {"Destination: user:ID, group:ID, or all", "target", nil}, {"Message text", "body", nil}}},
	"unschedule":     {"Cancel Scheduled Message", "schedule", "cancel", "menu:schedule", "scheduling", []guidedField{{"Scheduled message ID", "id", nil}}},
	"apikey":         {"Create API Key", "apikey", "create", "menu:api", "api_access", []guidedField{{"Key name", "text", nil}}},
	"revoke":         {"Revoke API Key", "apikey", "revoke", "menu:api", "api_access", []guidedField{{"API key ID", "id", nil}}},
}

var guidedEntries = map[string]string{
	"admin:group:add": "group", "admin:reward:set": "reward", "admin:admin:add": "admin",
	"admin:reward:limited": "limited_reward",
	"admin:required:add":   "required", "admin:tutorial:add": "tutorial", "admin:broadcast": "broadcast",
	"admin:user:tier-help": "tier", "admin:pattern:add": "pattern", "menu:createbot": "child",
}

func (a *App) guidedAllowed(ctx context.Context, userID int64, action guidedAction) bool {
	if action.Command == "createbot" && !a.isMain {
		return false
	}
	if permission := adminCommandPermission(action.Command); permission != "" {
		allowed, err := a.store.HasAdminPermission(ctx, a.botInstanceID, userID, permission)
		if err != nil || !allowed {
			return false
		}
	}
	if action.Feature != "" {
		tier, err := a.store.UserTier(ctx, a.botInstanceID, userID)
		if err != nil || !store.TierAllows(tier, action.Feature) {
			return false
		}
	}
	return true
}

func (a *App) handleGuidedCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) bool {
	key, entry := guidedEntries[cb.Data]
	if strings.HasPrefix(cb.Data, "guide:start:") {
		key = strings.TrimPrefix(cb.Data, "guide:start:")
		entry = true
	}
	if entry {
		action, ok := guidedActions[key]
		if !ok {
			a.sendHTML(cb.Message.Chat.ID, "This action is no longer available.", userBackMenu())
			return true
		}
		if !a.guidedAllowed(ctx, cb.From.ID, action) {
			a.sendHTML(cb.Message.Chat.ID, "This action requires the appropriate permission or premium tier.", userBackMenu())
			return true
		}
		nonce := make([]byte, 8)
		if _, err := rand.Read(nonce); err != nil {
			a.sendError(cb.Message.Chat.ID, err)
			return true
		}
		flow := store.TelegramFlow{Kind: "guided", Step: "0", Data: map[string]string{"action": key, "nonce": hex.EncodeToString(nonce)}}
		a.saveGuidedPrompt(ctx, cb.Message.Chat.ID, cb.From.ID, flow)
		return true
	}
	if !strings.HasPrefix(cb.Data, "guide:") {
		return false
	}
	flow, err := a.store.TelegramFlow(ctx, a.botInstanceID, cb.From.ID)
	parts := strings.Split(cb.Data, ":")
	if err != nil || flow.Kind != "guided" || len(parts) < 3 || flow.Data["nonce"] != parts[2] {
		a.sendHTML(cb.Message.Chat.ID, "This form expired or was replaced. Please start again.", userBackMenu())
		return true
	}
	action, ok := guidedActions[flow.Data["action"]]
	if !ok || !a.guidedAllowed(ctx, cb.From.ID, action) {
		a.sendHTML(cb.Message.Chat.ID, "You no longer have access to this action.", userBackMenu())
		return true
	}
	step, stepErr := strconv.Atoi(flow.Step)
	if stepErr != nil || step < 0 || step > len(action.Fields) {
		a.sendHTML(cb.Message.Chat.ID, "This form is no longer valid.", userBackMenu())
		return true
	}
	switch parts[1] {
	case "back":
		if len(parts) != 4 || parts[3] != flow.Step {
			break
		}
		if step > 0 {
			flow.Step = strconv.Itoa(step - 1)
		}
		a.saveGuidedPrompt(ctx, cb.Message.Chat.ID, cb.From.ID, flow)
		return true
	case "choice":
		if len(parts) != 5 || parts[3] != flow.Step || step >= len(action.Fields) {
			break
		}
		index, e := strconv.Atoi(parts[4])
		if e != nil || index < 0 || index >= len(action.Fields[step].Choices) {
			break
		}
		a.acceptGuidedInput(ctx, &tgbotapi.Message{Chat: cb.Message.Chat, From: cb.From}, flow, action.Fields[step].Choices[index])
		return true
	case "confirm":
		if step != len(action.Fields) {
			break
		}
		// Claim the exact encrypted form before any side effect. Old/repeated buttons
		// cannot execute a newer form, including after a process restart.
		claimed, e := a.store.ClaimTelegramFlow(ctx, a.botInstanceID, cb.From.ID, flow)
		if e != nil {
			a.sendError(cb.Message.Chat.ID, e)
			return true
		}
		if !claimed {
			break
		}
		values := []string{}
		if action.Prefix != "" {
			values = append(values, action.Prefix)
		}
		for i := range action.Fields {
			values = append(values, flow.Data[strconv.Itoa(i)])
		}
		message := &tgbotapi.Message{Chat: cb.Message.Chat, From: cb.From}
		args := strings.Join(values, "|")
		switch action.Command {
		case "createbot":
			a.handleCreateBot(ctx, message, args)
		case "webhook":
			a.handleWebhook(ctx, message, args)
		case "schedule":
			a.handleSchedule(ctx, message, args)
		case "apikey":
			a.handleAPIKey(ctx, message, args)
		default:
			a.handleAdminCommand(ctx, message, action.Command, args)
		}
		return true
	}
	a.sendHTML(cb.Message.Chat.ID, "This button does not match the current form step.", flowCancelMenu(action.Back))
	return true
}

func (a *App) saveGuidedPrompt(ctx context.Context, chatID, userID int64, flow store.TelegramFlow) {
	if err := a.store.SetTelegramFlow(ctx, a.botInstanceID, userID, flow); err != nil {
		a.sendError(chatID, err)
		return
	}
	action := guidedActions[flow.Data["action"]]
	step, _ := strconv.Atoi(flow.Step)
	text := "📋 <b>" + html.EscapeString(action.Title) + "</b>\n\n"
	rows := [][]premium.InlineButton{}
	if step < len(action.Fields) {
		field := action.Fields[step]
		text += fmt.Sprintf("Step %d/%d\n%s", step+1, len(action.Fields), html.EscapeString(field.Label))
		for i, choice := range field.Choices {
			rows = append(rows, []premium.InlineButton{premium.Button(choice, fmt.Sprintf("guide:choice:%s:%d:%d", flow.Data["nonce"], step, i), "primary", "check")})
		}
	} else {
		text += "Review before confirming:\n"
		for i, field := range action.Fields {
			value := flow.Data[strconv.Itoa(i)]
			if field.Kind == "secret" {
				value = "[stored securely]"
			}
			if value == "" {
				value = "none"
			}
			text += html.EscapeString(field.Label) + ": <code>" + html.EscapeString(short(value, 250)) + "</code>\n"
		}
		rows = append(rows, []premium.InlineButton{premium.Button("Confirm", "guide:confirm:"+flow.Data["nonce"], "success", "check")})
	}
	if step > 0 {
		rows = append(rows, []premium.InlineButton{premium.Button("Back", fmt.Sprintf("guide:back:%s:%d", flow.Data["nonce"], step), "primary", "back")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Cancel", "flow:cancel", "danger", "cancel"), premium.Button("Return to Menu", action.Back, "primary", "home")})
	a.sendHTML(chatID, text, premium.InlineKeyboard{InlineKeyboard: rows})
}

func (a *App) acceptGuidedInput(ctx context.Context, m *tgbotapi.Message, flow store.TelegramFlow, value string) bool {
	action, ok := guidedActions[flow.Data["action"]]
	if !ok || !a.guidedAllowed(ctx, m.From.ID, action) {
		a.sendHTML(m.Chat.ID, "You no longer have access to this action.", userBackMenu())
		return true
	}
	step, err := strconv.Atoi(flow.Step)
	if err != nil || step < 0 || step >= len(action.Fields) {
		a.sendHTML(m.Chat.ID, "Use Confirm, Back, or Cancel on the review screen.", flowCancelMenu(action.Back))
		return true
	}
	field := action.Fields[step]
	if field.Kind == "secret" && m.MessageID != 0 {
		_, _ = a.bot.Request(tgbotapi.NewDeleteMessage(m.Chat.ID, m.MessageID))
	}
	value = strings.TrimSpace(value)
	if err := validateGuidedField(field, value, a.location); err != nil {
		a.sendHTML(m.Chat.ID, html.EscapeString(err.Error()), flowCancelMenu(action.Back))
		return true
	}
	if field.Kind == "date" && value == "none" {
		value = ""
	}
	flow.Data[strconv.Itoa(step)] = value
	flow.Step = strconv.Itoa(step + 1)
	a.saveGuidedPrompt(ctx, m.Chat.ID, m.From.ID, flow)
	return true
}

func validateGuidedField(field guidedField, value string, location *time.Location) error {
	invalid := func() error { return fmt.Errorf("Invalid value. %s", field.Label) }
	if value == "" || len(value) > 3000 || (strings.Contains(value, "|") && field.Kind != "body" && field.Kind != "regex") {
		return invalid()
	}
	switch field.Kind {
	case "id", "chat":
		id, e := strconv.ParseInt(value, 10, 64)
		if e != nil || id == 0 || (field.Kind == "id" && id < 0) {
			return invalid()
		}
	case "recipient":
		if value != "global" {
			id, e := strconv.ParseInt(value, 10, 64)
			if e != nil || id <= 0 {
				return invalid()
			}
		}
	case "positive_int":
		n, e := strconv.Atoi(value)
		if e != nil || n <= 0 || n > 1000000 {
			return invalid()
		}
	case "reward_amount":
		parts := strings.Fields(value)
		if len(parts) != 2 || (strings.ToUpper(parts[1]) != "USD" && strings.ToUpper(parts[1]) != "PKR") || !regexp.MustCompile(`^\d+(?:\.\d{1,4})?$`).MatchString(parts[0]) {
			return invalid()
		}
		amount, e := strconv.ParseFloat(parts[0], 64)
		if e != nil || amount <= 0 || amount > 1000000 {
			return invalid()
		}
	case "enum":
		for _, choice := range field.Choices {
			if value == choice {
				return nil
			}
		}
		return invalid()
	case "date":
		if value != "none" {
			if _, e := time.ParseInLocation("2006-01-02", value, location); e != nil {
				return invalid()
			}
		}
	case "time":
		t, e := time.ParseInLocation("2006-01-02 15:04", value, location)
		if e != nil || !t.After(time.Now()) {
			return invalid()
		}
	case "https":
		u, e := url.ParseRequestURI(value)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" {
			return invalid()
		}
	case "webhook":
		if webhookservice.ValidateURL(value) != nil {
			return invalid()
		}
	case "regex":
		pattern, e := regexp.Compile(value)
		if e != nil || pattern.NumSubexp() < 1 {
			return invalid()
		}
	case "secret":
		if !childTokenPattern.MatchString(value) {
			return invalid()
		}
	case "target":
		if value != "all" {
			p := strings.Split(value, ":")
			if len(p) != 2 || (p[0] != "user" && p[0] != "group") {
				return invalid()
			}
			if id, e := strconv.ParseInt(p[1], 10, 64); e != nil || id == 0 {
				return invalid()
			}
		}
	case "rewards":
		seen := map[int]bool{}
		for _, item := range strings.Split(value, ",") {
			p := strings.Split(item, "=")
			if len(p) != 2 {
				return invalid()
			}
			n, e := strconv.Atoi(strings.TrimSpace(p[0]))
			v, e2 := strconv.ParseFloat(strings.TrimSpace(p[1]), 64)
			if e != nil || e2 != nil || n <= 0 || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || seen[n] {
				return invalid()
			}
			seen[n] = true
		}
	case "permissions":
		allowed := map[string]bool{"*": true, "manage_panels": true, "manage_groups": true, "manage_rewards": true, "manage_withdrawals": true, "manage_bots": true, "manage_tutorials": true, "manage_patterns": true, "manage_admins": true, "manage_tiers": true, "manage_settings": true, "broadcast": true, "view_analytics": true, "manage_users": true}
		for _, p := range strings.Split(value, ",") {
			if !allowed[strings.TrimSpace(p)] {
				return invalid()
			}
		}
	}
	return nil
}

func integrationMenu(create, remove string) premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Add New", "guide:start:"+create, "success", "check"), premium.Button("Remove / Cancel", "guide:start:"+remove, "danger", "admin")},
		{premium.Button("Settings", "menu:settings", "primary", "settings"), premium.Button("Main Menu", "menu:home", "primary", "phone")},
	}}
}

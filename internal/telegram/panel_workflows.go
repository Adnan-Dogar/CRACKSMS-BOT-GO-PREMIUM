package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/panels"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (a *App) listPanelSources(ctx context.Context, chat int64) {
	sources, e := a.store.PanelSources(ctx, a.botInstanceID)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	rows := [][]premium.InlineButton{}
	text := "🔌 <b>Panels & Accounts</b>\n\nSave a panel's name and link first, then add its accounts.\n"
	if len(sources) == 0 {
		text += "\nNo panels added yet."
	}
	for _, p := range sources {
		status := fmt.Sprintf("%d/%d online", p.Online, p.Accounts)
		if p.Accounts == 0 {
			status = "Needs Account"
		}
		if p.Shared {
			status = "Main template"
		}
		rows = append(rows, []premium.InlineButton{premium.Button(p.Name+" · "+status, fmt.Sprintf("admin:source:view:%d", p.ID), "primary", "plug")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Add Panel", "admin:panel:add", "success", "add"), premium.Button("Refresh", "admin:panels", "primary", "refresh")}, []premium.InlineButton{premium.Button("Admin Home", "menu:admin", "primary", "home")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) showPanelSource(ctx context.Context, chat, id int64) {
	p, e := a.store.PanelSource(ctx, a.botInstanceID, id)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	if p.Shared {
		a.sendHTML(chat, fmt.Sprintf("🔌 <b>%s</b>\n\nShared panel template from the main bot.\nType: %s\nLink: %s\n\nUse this template and connect your own account. Your credentials and OTP feed belong to this child bot.", html.EscapeString(p.Name), html.EscapeString(domain.Profile(p.Kind).Name), html.EscapeString(p.URL)), premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button("Use Panel / Add Account", fmt.Sprintf("admin:source:use:%d", id), "success", "add")}, {premium.Button("Panels", "admin:panels", "primary", "plug")}}})
		return
	}
	accounts, e := a.store.SourceAccounts(ctx, a.botInstanceID, id)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	text := fmt.Sprintf("🔌 <b>%s</b>\n\nType: <b>%s</b>\nLink: %s\nAccounts: <b>%d</b>\n\n", html.EscapeString(p.Name), html.EscapeString(domain.Profile(p.Kind).Category), html.EscapeString(p.URL), len(accounts))
	text += providerEndpointInfo(p.Kind, p.URL)
	if len(accounts) > 0 {
		text += "Editing this panel link applies to new accounts. Use Edit Credentials on an account to reconnect it.\n\n"
	}
	if len(accounts) == 0 {
		text += "Add an account to connect this panel."
	}
	if domain.Profile(p.Kind).Category == "Live SMS Stream" {
		text += "Connection method: " + html.EscapeString(domain.Profile(p.Kind).Name) + "\n"
	}
	if strings.HasPrefix(p.URL, "http://") {
		text += "Transport: HTTP\n"
	} else if strings.HasPrefix(p.URL, "https://") {
		text += "Transport: HTTPS\n"
	}
	if p.Kind == "ivas" {
		text += "\nConnect with a current authenticated Socket.IO URL, or renew the normal portal session. Credentials are encrypted and hidden."
	}
	rows := [][]premium.InlineButton{}
	for _, account := range accounts {
		status := "Disabled"
		if account.Enabled {
			status = "Connecting"
		}
		if account.Enabled && account.Healthy {
			status = "Online"
		}
		if account.LastError != "" {
			status = "Connection error"
		}
		if p.Kind == "ivas" && !account.Enabled {
			status = "Session/token needed"
		}
		rows = append(rows, []premium.InlineButton{premium.Button(account.Name+" · "+status, fmt.Sprintf("admin:panel:view:%d", account.ID), "primary", "user")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Add Account", fmt.Sprintf("admin:source:account:%d", id), "success", "add"), premium.Button("Edit Panel", fmt.Sprintf("admin:source:edit:%d", id), "primary", "edit")}, []premium.InlineButton{premium.Button("Remove Panel", fmt.Sprintf("admin:source:delete:%d", id), "danger", "trash")}, []premium.InlineButton{premium.Button("Panels", "admin:panels", "primary", "plug"), premium.Button("Home", "menu:admin", "primary", "home")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) startSourceForm(ctx context.Context, chat, user, id int64) {
	flow := store.TelegramFlow{Kind: "panel_source", Step: "name", Data: map[string]string{"nonce": broadcastKey(), "source_id": fmt.Sprint(id)}}
	if id != 0 {
		p, e := a.store.PanelSource(ctx, a.botInstanceID, id)
		if e != nil {
			a.sendError(chat, e)
			return
		}
		flow.Data["kind"] = p.Kind
	}
	a.sourcePrompt(ctx, chat, user, flow, "Send the panel name.", nil)
}
func (a *App) sourcePrompt(ctx context.Context, chat, user int64, flow store.TelegramFlow, text string, menu any) {
	if e := a.store.SetTelegramFlow(ctx, a.botInstanceID, user, flow); e != nil {
		a.sendError(chat, e)
		return
	}
	if menu == nil {
		menu = flowCancelMenu("admin:panels")
	}
	if k, ok := menu.(premium.InlineKeyboard); ok && flow.Step != "name" && flow.Step != "label" {
		k.InlineKeyboard = append(k.InlineKeyboard, []premium.InlineButton{premium.Button("Back / Edit", "admin:source:back:"+flow.Data["nonce"], "primary", "back")})
		menu = k
	}
	a.sendHTML(chat, "🔌 <b>Panel Setup</b>\n\n"+text, menu)
}
func (a *App) handleSourceText(ctx context.Context, m *tgbotapi.Message, flow store.TelegramFlow) bool {
	value := strings.TrimSpace(m.Text)
	switch flow.Step {
	case "name":
		if value == "" || len(value) > 80 {
			a.sendHTML(m.Chat.ID, "Use a name of 1–80 characters.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["name"] = value
		flow.Step = "kind"
		if flow.Data["kind"] != "" {
			flow.Step = "url"
			a.sourcePrompt(ctx, m.Chat.ID, m.From.ID, flow, "Send the panel link: HTTP/HTTPS for login and legacy APIs, HTTPS for other APIs, or WSS for streams. Credentials belong in Add Account.", nil)
			return true
		}
		menu := panelKindMenu()
		a.sourcePrompt(ctx, m.Chat.ID, m.From.ID, flow, "Choose the provider type.", menu)
	case "url":
		id, _ := strconv.ParseInt(flow.Data["source_id"], 10, 64)
		// Validate using the store's shared source validator without committing yet.
		if !store.ValidPanelSource(flow.Data["kind"], value) {
			a.sendHTML(m.Chat.ID, "Use a credential-free link: HTTP/HTTPS for login and legacy APIs, HTTPS for other APIs, or WSS for streams.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["url"] = value
		flow.Step = "confirm"
		a.sourcePrompt(ctx, m.Chat.ID, m.From.ID, flow, fmt.Sprintf("Review panel <b>%s</b>\nType: %s\nLink: %s\n%s\nCredentials are added separately.", html.EscapeString(flow.Data["name"]), html.EscapeString(domain.Profile(flow.Data["kind"]).Name), html.EscapeString(value), providerEndpointInfo(flow.Data["kind"], value)), confirmationMenu("admin:source:save:"+flow.Data["nonce"], fmt.Sprintf("admin:source:edit:%d", id)))
	}
	return true
}

func providerEndpointInfo(kind, base string) string {
	if strings.HasPrefix(base, "http://") {
		return "⚠️ HTTP sends credentials and SMS without transport encryption. Confirm only if you accept this legacy transport.\n"
	}
	operations := []string{"sms"}
	if kind == "augestel" {
		operations = []string{"messages", "numbers", "statistics"}
	} else if kind != "axon_asp" {
		return ""
	}
	text := ""
	for _, operation := range operations {
		endpoint, err := panels.RESTEndpoint(kind, base, operation)
		if err == nil {
			label := map[string]string{"sms": "SMS", "messages": "Messages", "numbers": "Numbers", "statistics": "Statistics"}[operation]
			text += fmt.Sprintf("%s endpoint: <code>%s</code>\n", label, html.EscapeString(endpoint))
		}
	}
	return text
}
func (a *App) startAccountForm(ctx context.Context, chat, user, source, id int64) {
	p, e := a.store.PanelSource(ctx, a.botInstanceID, source)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	if p.Shared {
		source, e = a.store.UsePanelTemplate(ctx, a.botInstanceID, source)
		if e != nil {
			a.sendError(chat, e)
			return
		}
		p, e = a.store.PanelSource(ctx, a.botInstanceID, source)
		if e != nil {
			a.sendError(chat, e)
			return
		}
	}
	flow := store.TelegramFlow{Kind: "panel_account", Step: "label", Data: map[string]string{"nonce": broadcastKey(), "source_id": fmt.Sprint(source), "account_id": fmt.Sprint(id), "kind": p.Kind, "url": p.URL}}
	a.sourcePrompt(ctx, chat, user, flow, "Send a label for this account.", nil)
}
func (a *App) handleAccountText(ctx context.Context, m *tgbotapi.Message, flow store.TelegramFlow) bool {
	v := strings.TrimSpace(m.Text)
	secret := flow.Step == "password" || flow.Step == "token" || flow.Step == "stream_url"
	if secret && m.MessageID != 0 {
		_, _ = a.bot.Request(tgbotapi.NewDeleteMessage(m.Chat.ID, m.MessageID))
	}
	if v == "" {
		a.sendHTML(m.Chat.ID, "This value cannot be empty.", flowCancelMenu("admin:panels"))
		return true
	}
	switch flow.Step {
	case "label":
		if len(v) > 80 {
			a.sendHTML(m.Chat.ID, "Account label must be at most 80 characters.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["label"] = v
		if flow.Data["url"] == "" {
			flow.Step = "url"
			a.sourcePrompt(ctx, m.Chat.ID, m.From.ID, flow, "Send the panel link: HTTP/HTTPS for login and legacy APIs, HTTPS for other APIs, or WSS for streams.", nil)
			return true
		}
		if flow.Data["kind"] == "ivas" || flow.Data["kind"] == "socketio" {
			flow.Step = "stream_url"
			menu := flowCancelMenu("admin:panels")
			prompt := "Paste the complete authenticated WSS Socket.IO URL. Its token and user value are stored encrypted."
			if flow.Data["kind"] == "ivas" {
				prompt += " You can also use normal portal login."
				menu.InlineKeyboard = append([][]premium.InlineButton{{premium.Button("Normal Portal Login", "admin:source:portal:"+flow.Data["nonce"], "primary", "key")}}, menu.InlineKeyboard...)
			}
			a.sourcePrompt(ctx, m.Chat.ID, m.From.ID, flow, prompt, menu)
			return true
		}
		a.nextAccountCredential(ctx, m.Chat.ID, m.From.ID, flow)
	case "url":
		if !store.ValidPanelSource(flow.Data["kind"], v) {
			a.sendHTML(m.Chat.ID, "Enter an HTTP/HTTPS login link, HTTPS API link, or WSS stream link.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["url"] = v
		a.nextAccountCredential(ctx, m.Chat.ID, m.From.ID, flow)
	case "username", "email":
		if len(v) > 200 {
			a.sendHTML(m.Chat.ID, "Enter a username/email of at most 200 characters.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["username"] = v
		flow.Step = "password"
		a.sourcePrompt(ctx, m.Chat.ID, m.From.ID, flow, "Send the password. The credential message is deleted after capture.", nil)
	case "password", "token":
		flow.Data[flow.Step] = v
		flow.Step = "confirm"
		a.sourcePrompt(ctx, m.Chat.ID, m.From.ID, flow, "Review account <b>"+html.EscapeString(flow.Data["label"])+"</b>\nCredentials: <b>encrypted / hidden</b>\n\n"+providerEndpointInfo(flow.Data["kind"], flow.Data["url"])+"\nConfirm to test and save this connection.", confirmationMenu("admin:source:accountsave:"+flow.Data["nonce"], "flow:cancel"))
	case "stream_url":
		config, e := panels.ParseSocketCredentials(v)
		if e != nil {
			a.sendHTML(m.Chat.ID, "Paste a valid authenticated WSS Socket.IO URL.", flowCancelMenu("admin:panels"))
			return true
		}
		for k, value := range config {
			flow.Data[k] = value
		}
		flow.Step = "confirm"
		a.sourcePrompt(ctx, m.Chat.ID, m.From.ID, flow, "Review account <b>"+html.EscapeString(flow.Data["label"])+"</b>\nSocket credentials: <b>encrypted / hidden</b>\n\nConfirm to test and save.", confirmationMenu("admin:source:accountsave:"+flow.Data["nonce"], "flow:cancel"))
	}
	return true
}
func (a *App) nextAccountCredential(ctx context.Context, chat, user int64, flow store.TelegramFlow) {
	if flow.Data["kind"] == "login" {
		flow.Step = "username"
		a.sourcePrompt(ctx, chat, user, flow, "Send the username or email.", nil)
	} else {
		flow.Step = "token"
		a.sourcePrompt(ctx, chat, user, flow, "Send the API/stream token. Its message will be deleted.", nil)
	}
}
func (a *App) saveAccountForm(ctx context.Context, cb *tgbotapi.CallbackQuery, flow store.TelegramFlow) {
	config := map[string]any{"url": flow.Data["url"], "token": flow.Data["token"]}
	kind := flow.Data["kind"]
	if kind == "login" || kind == "ivas" {
		config = map[string]any{"base_url": flow.Data["url"], "username": flow.Data["username"], "password": flow.Data["password"]}
	}
	if flow.Data["stream_url"] != "" {
		config["stream_url"] = flow.Data["stream_url"]
		if kind == "socketio" {
			config["url"] = flow.Data["stream_url"]
		}
		config["token"] = flow.Data["token"]
		config["user"] = flow.Data["user"]
	}
	if kind == "axon_asp" || kind == "augestel" {
		config["api_base"] = flow.Data["url"]
	}
	if kind == "token_api" {
		config["api_type"] = "crapi"
	}
	if kind == "legacy_api" {
		config["api_type"] = "reseller"
	}
	source, _ := strconv.ParseInt(flow.Data["source_id"], 10, 64)
	account, _ := strconv.ParseInt(flow.Data["account_id"], 10, 64)
	id, e := a.store.SavePanelAccount(ctx, a.botInstanceID, source, account, flow.Data["label"], config, false)
	if e != nil {
		a.sendError(cb.Message.Chat.ID, e)
		return
	}
	_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, cb.From.ID)
	if e = a.store.SchedulePanelTest(ctx, a.botInstanceID, id, time.Now(), true); e != nil {
		a.sendError(cb.Message.Chat.ID, e)
		return
	}
	a.sendHTML(cb.Message.Chat.ID, fmt.Sprintf("Account #%d saved securely. Connection test queued. Use Refresh or Test Now to view the result.", id), userBackMenu())
	a.showPanelSource(ctx, cb.Message.Chat.ID, source)
}
func (a *App) handlePanelWorkflowCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) bool {
	if a.handleProviderCallback(ctx, cb) {
		return true
	}
	d := cb.Data
	if d != "admin:panel:add" && !strings.HasPrefix(d, "admin:source:") && !strings.HasPrefix(d, "admin:panel:credentials:") && !strings.HasPrefix(d, "admin:panel:kind:") {
		return false
	}
	allowed, e := a.store.HasAdminPermission(ctx, a.botInstanceID, cb.From.ID, "manage_panels")
	if e != nil || !allowed {
		a.sendHTML(cb.Message.Chat.ID, "You do not have permission to manage panels.", userBackMenu())
		return true
	}
	if d == "admin:panel:add" {
		a.startSourceForm(ctx, cb.Message.Chat.ID, cb.From.ID, 0)
		return true
	}
	parts := strings.Split(d, ":")
	last := parts[len(parts)-1]
	id, _ := strconv.ParseInt(last, 10, 64)
	switch {
	case strings.HasPrefix(d, "admin:panel:credentials:"):
		source, e := a.store.AccountSourceID(ctx, a.botInstanceID, id)
		if e != nil {
			a.sendError(cb.Message.Chat.ID, e)
		} else {
			a.startAccountForm(ctx, cb.Message.Chat.ID, cb.From.ID, source, id)
		}
	case strings.HasPrefix(d, "admin:source:view:"):
		a.showPanelSource(ctx, cb.Message.Chat.ID, id)
	case strings.HasPrefix(d, "admin:source:edit:"):
		a.startSourceForm(ctx, cb.Message.Chat.ID, cb.From.ID, id)
	case strings.HasPrefix(d, "admin:source:use:"):
		a.startAccountForm(ctx, cb.Message.Chat.ID, cb.From.ID, id, 0)
	case strings.HasPrefix(d, "admin:source:account:"):
		a.startAccountForm(ctx, cb.Message.Chat.ID, cb.From.ID, id, 0)
	case strings.HasPrefix(d, "admin:source:delete:"):
		a.sendHTML(cb.Message.Chat.ID, "Remove this panel and all its accounts? Accepted OTP history is retained.", confirmationMenu(fmt.Sprintf("admin:source:deleteyes:%d", id), fmt.Sprintf("admin:source:view:%d", id)))
	case strings.HasPrefix(d, "admin:source:deleteyes:"):
		if e = a.store.DeletePanelSource(ctx, a.botInstanceID, id); e != nil {
			a.sendError(cb.Message.Chat.ID, e)
		} else {
			a.listPanelSources(ctx, cb.Message.Chat.ID)
		}
	default:
		flow, e := a.store.TelegramFlow(ctx, a.botInstanceID, cb.From.ID)
		if e != nil {
			a.sendHTML(cb.Message.Chat.ID, "This setup expired. Start again from Panels.", userBackMenu())
			return true
		}
		if strings.HasPrefix(d, "admin:source:back:") && last == flow.Data["nonce"] {
			if flow.Kind == "panel_source" {
				flow.Step = "name"
				a.sourcePrompt(ctx, cb.Message.Chat.ID, cb.From.ID, flow, "Send the panel name again to edit these details.", nil)
			} else if flow.Kind == "panel_account" {
				flow.Step = "label"
				delete(flow.Data, "password")
				delete(flow.Data, "token")
				delete(flow.Data, "stream_url")
				delete(flow.Data, "user")
				a.sourcePrompt(ctx, cb.Message.Chat.ID, cb.From.ID, flow, "Send the account label again to edit credentials.", nil)
			}
		} else if strings.HasPrefix(d, "admin:source:portal:") && last == flow.Data["nonce"] && flow.Kind == "panel_account" && flow.Data["kind"] == "ivas" {
			delete(flow.Data, "stream_url")
			delete(flow.Data, "token")
			delete(flow.Data, "user")
			flow.Step = "email"
			a.sourcePrompt(ctx, cb.Message.Chat.ID, cb.From.ID, flow, "Send the IVAS account email for normal portal login.", nil)
		} else if strings.HasPrefix(d, "admin:panel:kind:") && flow.Kind == "panel_source" && flow.Step == "kind" {
			if last == "types" {
				a.sourcePrompt(ctx, cb.Message.Chat.ID, cb.From.ID, flow, "Choose the panel system.", panelKindMenu())
				return true
			}
			if last == "stream" {
				a.sourcePrompt(ctx, cb.Message.Chat.ID, cb.From.ID, flow, "Choose how to connect the live stream. IVAS Account supports portal login or a supplied authenticated stream URL. Stream URL uses Socket.IO by default.", streamMethodsMenu())
				return true
			}
			kind := map[string]string{"login": "login", "token": "token_api", "legacy": "legacy_api", "ws": "websocket", "ivas": "ivas", "socketio": "socketio", "axon_asp": "axon_asp", "augestel": "augestel"}[last]
			if kind == "" {
				a.sendError(cb.Message.Chat.ID, errors.New("invalid provider"))
				return true
			}
			flow.Data["kind"] = kind
			flow.Step = "url"
			prompt := "Send the panel link: HTTP/HTTPS for login and legacy APIs, HTTPS for other APIs, or WSS for streams."
			if profile := domain.Profile(kind); profile.APIPath != "" {
				prompt = "Send the HTTPS API base URL. A domain root uses <code>" + profile.APIPath + "</code>; include the complete API path for a compatible panel with a custom endpoint."
			}
			a.sourcePrompt(ctx, cb.Message.Chat.ID, cb.From.ID, flow, prompt, nil)
		} else if strings.HasPrefix(d, "admin:source:save:") && last == flow.Data["nonce"] && flow.Kind == "panel_source" && flow.Step == "confirm" {
			old, _ := strconv.ParseInt(flow.Data["source_id"], 10, 64)
			saved, e := a.store.SavePanelSource(ctx, a.botInstanceID, old, flow.Data["name"], flow.Data["kind"], flow.Data["url"])
			if e != nil {
				a.sendError(cb.Message.Chat.ID, e)
				return true
			}
			_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, cb.From.ID)
			a.showPanelSource(ctx, cb.Message.Chat.ID, saved)
		} else if strings.HasPrefix(d, "admin:source:accountsave:") && last == flow.Data["nonce"] && flow.Kind == "panel_account" && flow.Step == "confirm" {
			a.saveAccountForm(ctx, cb, flow)
		} else {
			a.sendHTML(cb.Message.Chat.ID, "This action does not match the current setup step.", flowCancelMenu("admin:panels"))
		}
	}
	return true
}

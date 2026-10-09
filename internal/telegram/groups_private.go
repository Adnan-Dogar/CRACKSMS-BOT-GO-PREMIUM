package telegram

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type groupInteraction struct {
	Chat, User               int64
	Thread, Incoming, Screen int
	Callback                 string
}

func (a *App) prepareGroupInteraction(ctx context.Context, u tgbotapi.Update) bool {
	var m *tgbotapi.Message
	var user *tgbotapi.User
	var meta messageMetadata
	var callback string
	raw, _ := ctx.Value(entityContextKey{}).(incomingUpdate)
	if u.CallbackQuery != nil {
		m = u.CallbackQuery.Message
		user = u.CallbackQuery.From
		meta = raw.CallbackMeta
		callback = u.CallbackQuery.ID
	} else {
		m = u.Message
		if m != nil {
			user = m.From
		}
		meta = raw.MessageMeta
	}
	if m == nil || m.Chat == nil || user == nil {
		return false
	}
	if m.Chat.IsPrivate() {
		return true
	}
	if m.Chat.Type != "group" && m.Chat.Type != "supergroup" {
		return false
	}
	if callback == "" && !m.IsCommand() {
		return false
	}
	if callback != "" && meta.Receiver != nil && meta.Receiver.ID != user.ID {
		return false
	}
	a.group = &groupInteraction{Chat: m.Chat.ID, User: user.ID, Thread: meta.ThreadID, Callback: callback}
	if callback != "" {
		a.group.Screen = meta.EphemeralID
	} else {
		a.group.Incoming = meta.EphemeralID
	}
	// Unsolicited ephemeral replies require administrator status. Check before
	// allocating stock or changing account preferences for an ordinary command.
	if callback == "" && meta.EphemeralID == 0 {
		member, err := a.bot.GetChatMember(tgbotapi.GetChatMemberConfig{ChatConfigWithUser: tgbotapi.ChatConfigWithUser{ChatID: m.Chat.ID, UserID: a.bot.Self.ID}})
		if err != nil || (member.Status != "administrator" && member.Status != "creator") {
			a.privateHandoff(m.Chat.ID, m.Command())
			return false
		}
	}
	if callback == "" && meta.EphemeralID != 0 && m.Date > 0 && time.Now().Unix()-int64(m.Date) > 13 {
		a.privateHandoff(m.Chat.ID, m.Command())
		return false
	}
	return true
}
func groupCommandAllowed(command string) bool {
	switch command {
	case "start", "help", "services", "getnumber", "balance", "account", "mystats", "myprofile", "myhistory", "top", "referral", "theme", "premium", "analytics", "tutorials", "settings", "topapps", "topcountries", "favorites", "lastselection", "alerts", "cancel", "mynumbers", "search", "notifications":
		return true
	}
	return false
}
func groupCallbackAllowed(route string) bool {
	if route == "prefs:timezone" {
		return false
	}
	if strings.HasPrefix(route, "admin:") || strings.HasPrefix(route, "guide:") || strings.HasPrefix(route, "withdraw:") || strings.HasPrefix(route, "webhook:") || strings.HasPrefix(route, "api:") || strings.HasPrefix(route, "schedule:") {
		return false
	}
	switch route {
	case "menu:admin", "menu:withdraw", "menu:api", "menu:webhooks", "menu:schedule", "menu:createbot", "menu:mybots", "menu:liveotp", "menu:myotps", "menu:mywithdrawals":
		return false
	}
	return strings.HasPrefix(route, "menu:") || strings.HasPrefix(route, "buy:") || strings.HasPrefix(route, "tools:") || strings.HasPrefix(route, "prefs:") || strings.HasPrefix(route, "theme:") || strings.HasPrefix(route, "tutorial:") || strings.HasPrefix(route, "help:") || strings.HasPrefix(route, "activity:") || strings.HasPrefix(route, "live:") && !strings.Contains(route, "private") || strings.HasPrefix(route, "stats:") || route == "check_membership" || route == "ignore"
}
func privateCommandForRoute(route string) string {
	switch {
	case strings.HasPrefix(route, "prefs:"):
		return "settings"
	case strings.HasPrefix(route, "admin:") || route == "menu:admin":
		return "admin"
	case strings.Contains(route, "live") || strings.Contains(route, "myotps"):
		return "liveotp"
	case strings.Contains(route, "withdraw"):
		return "withdraw"
	case strings.Contains(route, "api"):
		return "apikey"
	case strings.Contains(route, "webhook"):
		return "webhook"
	case strings.Contains(route, "schedule"):
		return "schedule"
	case strings.Contains(route, "createbot"):
		return "createbot"
	}
	return "start"
}
func (a *App) privateHandoff(chat int64, command string) {
	if command == "" {
		command = "start"
	}
	name := a.bot.Self.UserName
	link := "https://t.me/" + name + "?start=open_" + command
	a.sendHTML(chat, "🔒 <b>Continue in private chat</b>\n\nLive OTP delivery, credentials and administration are available in your private conversation with the bot.", premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{{Text: "Open Private Chat", URL: link, Style: "primary", IconCustomEmojiID: premium.ID("lock")}}}})
}
func (a *App) groupParams(method string, params tgbotapi.Params) (string, error) {
	g := a.group
	params["chat_id"] = fmt.Sprint(g.Chat)
	if g.Thread != 0 {
		params["message_thread_id"] = fmt.Sprint(g.Thread)
	}
	if g.Screen != 0 {
		delete(params, "message_thread_id")
		params["receiver_user_id"] = fmt.Sprint(g.User)
		params["ephemeral_message_id"] = fmt.Sprint(g.Screen)
		if method == "sendMessage" || method == "sendRichMessage" {
			method = "editEphemeralMessageText"
		}
		return method, nil
	}
	ephemeral := map[string]any{"receiver_user_id": g.User}
	if g.Callback != "" {
		ephemeral["callback_query_id"] = g.Callback
	}
	if e := params.AddInterface("ephemeral_message_parameters", ephemeral); e != nil {
		return "", e
	}
	if g.Incoming != 0 {
		if e := params.AddInterface("reply_parameters", map[string]any{"ephemeral_message_id": g.Incoming}); e != nil {
			return "", e
		}
	}
	return method, nil
}
func (a *App) renderGroupDocument(chat int64, document screenDocument) error {
	g := a.group
	if g == nil || chat != g.Chat {
		return fmt.Errorf("missing private group context")
	}
	a.ui.mu.Lock()
	rich := a.ui.richEnabled && !a.ui.richUnsupported && a.displayFormat != "classic" && document.RichHTML != ""
	a.ui.mu.Unlock()
	params := tgbotapi.Params{"chat_id": fmt.Sprint(chat)}
	method := "sendMessage"
	if rich {
		method = "sendRichMessage"
		_ = params.AddInterface("rich_message", inputRichMessage{HTML: premium.AnimateHTML(document.RichHTML)})
	} else {
		params["text"] = premium.AnimateHTML(document.ClassicHTML)
		params["parse_mode"] = "HTML"
	}
	method, e := a.groupParams(method, params)
	if e != nil {
		return e
	}
	markup := a.styledMarkup(document.Keyboard, document.ClassicHTML)
	if e = params.AddInterface("reply_markup", markup); e != nil {
		return e
	}
	response, e := a.bot.MakeRequest(method, params)
	if e != nil && strings.Contains(strings.ToLower(e.Error()), "message is not modified") {
		return nil
	}
	api, definite := tgtransport.APIError(e)
	if e != nil && definite && (api.Code == 400 || api.Code == 404) && rich {
		delete(params, "rich_message")
		params["text"] = premium.AnimateHTML(document.ClassicHTML)
		params["parse_mode"] = "HTML"
		if method == "sendRichMessage" {
			method = "sendMessage"
		}
		response, e = a.bot.MakeRequest(method, params)
	}
	if renderingRejected(e) {
		params["text"] = customEmojiTag.ReplaceAllString(premium.AnimateHTML(document.ClassicHTML), "")
		if strings.Contains(strings.ToLower(e.Error()), "can't parse entities") {
			params["text"] = html.UnescapeString(telegramHTMLTag.ReplaceAllString(params["text"], ""))
			delete(params, "parse_mode")
		}
		delete(params, "rich_message")
		if method == "sendRichMessage" {
			method = "sendMessage"
		}
		_ = params.AddInterface("reply_markup", plainMarkup(markup))
		response, e = a.bot.MakeRequest(method, params)
	}
	if e == nil {
		a.rememberMessage(response.Result, g.Screen)
		return nil
	}
	// A private response must never become an ordinary group message.
	if api, ok := tgtransport.APIError(e); ok && (api.Code == 400 || api.Code == 403 || api.Code == 404) {
		if g.Callback != "" {
			_, _ = a.bot.MakeRequest("answerCallbackQuery", tgbotapi.Params{"callback_query_id": g.Callback, "text": "Open the bot's private chat to continue.", "show_alert": "true"})
		}
		// A generic private handoff is safe even when the group feature is
		// unavailable. Never retry personal content as a public group message.
		_, _ = a.bot.MakeRequest("sendMessage", tgbotapi.Params{"chat_id": fmt.Sprint(g.User), "text": "Continue in private chat: https://t.me/" + a.bot.Self.UserName + "?start=open_start"})
		return e
	}
	return e
}

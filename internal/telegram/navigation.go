package telegram

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Users always reach the same FIFO worker, including their document uploads.
func updateShard(update tgbotapi.Update, count int) int {
	var id int64
	if update.CallbackQuery != nil && update.CallbackQuery.From != nil {
		id = update.CallbackQuery.From.ID
	} else if update.Message != nil && update.Message.From != nil {
		id = update.Message.From.ID
	}
	return int(uint64(id) % uint64(count))
}

func isNavigationCallback(data string) bool {
	return strings.HasPrefix(data, "live:") || strings.HasPrefix(data, "activity:") || strings.HasPrefix(data, "tools:") || strings.HasPrefix(data, "prefs:") || strings.HasPrefix(data, "help:") || strings.HasPrefix(data, "stats:") || strings.HasPrefix(data, "admin:stats:") || strings.HasPrefix(data, "admin:bots:filter:") || strings.HasPrefix(data, "admin:broadcast:status:") || strings.HasPrefix(data, "menu:") || strings.HasPrefix(data, "buy:service:") || strings.HasPrefix(data, "admin:source:view:") ||
		data == "admin:numbers" || data == "admin:inventory" || data == "admin:backups" || data == "admin:panels" ||
		data == "admin:groups" || data == "admin:users" || data == "admin:withdrawals" ||
		data == "admin:admins" || data == "admin:required" || data == "admin:settings" ||
		data == "admin:themes" || data == "admin:patterns" || data == "admin:rewards" ||
		data == "admin:tutorials" || data == "admin:bots" || data == "admin:analytics"
}

var customEmojiTag = regexp.MustCompile(`</?tg-emoji\b[^>]*>`)

func plainMarkup(markup any) any {
	keyboard, ok := markup.(premium.InlineKeyboard)
	if !ok {
		return markup
	}
	rows := make([][]premium.InlineButton, len(keyboard.InlineKeyboard))
	for i, row := range keyboard.InlineKeyboard {
		rows[i] = append([]premium.InlineButton(nil), row...)
		for j := range rows[i] {
			rows[i][j].IconCustomEmojiID = ""
		}
	}
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func renderingRejected(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "can't parse entities") || strings.Contains(s, "custom emoji") ||
		strings.Contains(s, "custom_emoji") || strings.Contains(s, "emoji_id_invalid")
}

// MakeRequest preserves modern keyboard fields absent from the older Go SDK's
// edit-message structs. Both send and edit use the same renderer.
func (a *App) renderScreen(chatID int64, text string, markup any) error {
	markup = a.styledMarkup(markup, text)
	if a.group != nil && a.group.Chat == chatID {
		keyboard, _ := markup.(premium.InlineKeyboard)
		return a.renderGroupDocument(chatID, screenDocument{ClassicHTML: text, Keyboard: keyboard})
	}
	method := "sendMessage"
	params := tgbotapi.Params{"chat_id": fmt.Sprint(chatID), "text": premium.AnimateHTML(text), "parse_mode": "HTML", "disable_web_page_preview": "true"}
	if a.screenMessageID != 0 && a.screenChatID == chatID {
		method = "editMessageText"
		params["message_id"] = fmt.Sprint(a.screenMessageID)
		a.screenMessageID = 0
	}
	if err := params.AddInterface("reply_markup", markup); err != nil {
		return err
	}
	response, err := a.bot.MakeRequest(method, params)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		a.lastMessageID, _ = strconv.Atoi(params["message_id"])
		return nil
	}
	if renderingRejected(err) {
		params["text"] = customEmojiTag.ReplaceAllString(premium.AnimateHTML(text), "")
		if strings.Contains(strings.ToLower(err.Error()), "can't parse entities") {
			params["text"] = html.UnescapeString(telegramHTMLTag.ReplaceAllString(customEmojiTag.ReplaceAllString(text, ""), ""))
			delete(params, "parse_mode")
		}
		if e := params.AddInterface("reply_markup", plainMarkup(markup)); e != nil {
			return e
		}
		response, err = a.bot.MakeRequest(method, params)
	}
	if err != nil && method == "editMessageText" {
		description := strings.ToLower(err.Error())
		if strings.Contains(description, "message to edit not found") || strings.Contains(description, "message can't be edited") || strings.Contains(description, "message_id_invalid") {
			delete(params, "message_id")
			response, err = a.bot.MakeRequest("sendMessage", params)
		}
	}
	if err == nil {
		a.rememberMessage(response.Result, 0)
	}
	return err
}

func (a *App) sendAdminDashboard(ctx context.Context, chatID, userID int64) {
	admin, err := a.store.HasAnyAdminRole(ctx, a.botInstanceID, userID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	if !admin {
		a.sendHTML(chatID, "You do not have access to the admin dashboard.", userBackMenu())
		return
	}
	menu := adminDashboard()
	rows := make([][]premium.InlineButton, 0, len(menu.InlineKeyboard))
	for _, row := range menu.InlineKeyboard {
		filtered := []premium.InlineButton{}
		for _, button := range row {
			if !a.isMain && (button.CallbackData == "admin:bots" || button.CallbackData == "admin:backups") {
				continue
			}
			permission := adminCallbackPermission(button.CallbackData)
			if permission != "" {
				allowed, e := a.store.HasAdminPermission(ctx, a.botInstanceID, userID, permission)
				if e != nil {
					a.sendError(chatID, e)
					return
				}
				if !allowed {
					continue
				}
			}
			filtered = append(filtered, button)
		}
		if len(filtered) > 0 {
			rows = append(rows, filtered)
		}
	}
	text := "👮 <b>ADMIN PANEL</b>\n\nChoose a management tool below."
	if allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, userID, "view_analytics"); allowed {
		catalog, e := a.store.CatalogForInstance(ctx, a.botInstanceID)
		if e != nil {
			a.sendError(chatID, e)
			return
		}
		count := 0
		for _, countries := range catalog {
			for _, country := range countries {
				count += country.Available
			}
		}
		panels, e := a.store.PanelHealthReportForInstance(ctx, a.botInstanceID)
		if e != nil {
			a.sendError(chatID, e)
			return
		}
		healthy := 0
		for _, panel := range panels {
			if panel.Enabled && panel.Healthy {
				healthy++
			}
		}
		text = fmt.Sprintf("👮 <b>ADMIN PANEL</b>\n\n📱 Available numbers: <b>%d</b>\n📡 Healthy panels: <b>%d/%d</b>\n\nChoose a management tool below.", count, healthy, len(panels))
	}
	a.sendHTML(chatID, text, premium.InlineKeyboard{InlineKeyboard: rows})
}

var telegramHTMLTag = regexp.MustCompile(`</?(?:b|strong|i|em|u|ins|s|strike|del|span|tg-spoiler|a|code|pre|blockquote)(?:\s[^>]*)?>`)

func safeTelegramError(err error) string {
	if apiError, ok := tgtransport.APIError(err); ok {
		return fmt.Sprintf("Telegram API error %d: %s", apiError.Code, apiError.Message)
	}
	return fmt.Sprintf("%T", err)
}

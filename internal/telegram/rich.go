package telegram

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type inputRichMessage struct {
	HTML string `json:"html"`
}
type screenDocument struct {
	RichHTML, ClassicHTML string
	Keyboard              premium.InlineKeyboard
}

func (a *App) rememberMessage(result json.RawMessage, fallback int) {
	var message struct {
		MessageID   int `json:"message_id"`
		EphemeralID int `json:"ephemeral_message_id"`
	}
	_ = json.Unmarshal(result, &message)
	if a.group != nil && message.EphemeralID != 0 {
		a.lastMessageID = message.EphemeralID
		return
	}
	if message.MessageID != 0 {
		a.lastMessageID = message.MessageID
	} else {
		a.lastMessageID = fallback
	}
}
func (a *App) renderDocument(chat int64, document screenDocument) error {
	if a.group != nil && a.group.Chat == chat {
		return a.renderGroupDocument(chat, document)
	}
	document.Keyboard = a.styledMarkup(document.Keyboard, document.ClassicHTML).(premium.InlineKeyboard)
	a.ui.mu.Lock()
	unsupported := a.ui.richUnsupported
	a.ui.mu.Unlock()
	if !a.ui.richEnabled || a.displayFormat == "classic" || unsupported || document.RichHTML == "" {
		return a.renderScreen(chat, document.ClassicHTML, document.Keyboard)
	}
	method := "sendRichMessage"
	messageID := 0
	if a.screenMessageID != 0 && a.screenChatID == chat {
		method = "editMessageText"
		messageID = a.screenMessageID
	}
	params := tgbotapi.Params{"chat_id": fmt.Sprint(chat), "disable_notification": "true"}
	if messageID != 0 {
		params["message_id"] = fmt.Sprint(messageID)
	}
	if err := params.AddInterface("rich_message", inputRichMessage{HTML: premium.AnimateHTML(document.RichHTML)}); err != nil {
		return err
	}
	if err := params.AddInterface("reply_markup", document.Keyboard); err != nil {
		return err
	}
	response, err := a.bot.MakeRequest(method, params)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		a.lastMessageID = messageID
		a.screenMessageID = 0
		return nil
	}
	if err == nil {
		a.rememberMessage(response.Result, messageID)
		a.screenMessageID = 0
		return nil
	}
	description := strings.ToLower(err.Error())
	// Only a definite rejection allows another send. A timeout may mean the
	// first send succeeded, so never duplicate it through a fallback.
	apiError, isAPI := tgtransport.APIError(err)
	if !isAPI || (apiError.Code != 400 && apiError.Code != 404) {
		return err
	}
	if strings.Contains(description, "message to edit not found") || strings.Contains(description, "message can't be edited") {
		delete(params, "message_id")
		response, err = a.bot.MakeRequest("sendRichMessage", params)
		if err == nil {
			a.rememberMessage(response.Result, 0)
			a.screenMessageID = 0
		}
		if err == nil {
			return nil
		}
		apiError, isAPI = tgtransport.APIError(err)
		if !isAPI || (apiError.Code != 400 && apiError.Code != 404) {
			return err
		}
		description = strings.ToLower(err.Error())
		messageID = 0
	}
	if apiError.Code == 404 || strings.Contains(description, "method not found") || strings.Contains(description, "unsupported") || strings.Contains(description, "unknown method") {
		a.ui.mu.Lock()
		a.ui.richUnsupported = true
		a.ui.mu.Unlock()
	}
	// The independently built classic document preserves table rows/actions.
	a.screenChatID, a.screenMessageID = chat, messageID
	return a.renderScreen(chat, document.ClassicHTML, document.Keyboard)
}
func (a *App) sendDocument(chat int64, document screenDocument) {
	if err := a.renderDocument(chat, document); err != nil {
		slog.Warn("Telegram rich screen failed", "error", safeTelegramError(err))
	}
}

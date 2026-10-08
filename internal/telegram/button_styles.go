package telegram

import (
	"context"
	"strings"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
)

func (a *App) clearNavigationFlow(ctx context.Context, user int64) error {
	if a.group != nil {
		return nil
	}
	if err := a.store.DetachImportScreens(ctx, a.botInstanceID, user); err != nil {
		return err
	}
	return a.store.ClearTelegramFlow(ctx, a.botInstanceID, user)
}
func (a *App) styledMarkup(markup any, text string) any {
	keyboard, ok := markup.(premium.InlineKeyboard)
	if !ok {
		return markup
	}
	context := "detail"
	if strings.Contains(strings.ToLower(text), "main menu") || strings.Contains(strings.ToLower(text), "welcome") {
		context = "user"
	}
	for _, row := range keyboard.InlineKeyboard {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "admin:") {
				context = "admin"
			}
		}
	}
	rows := make([][]premium.InlineButton, len(keyboard.InlineKeyboard))
	for i, row := range keyboard.InlineKeyboard {
		rows[i] = append([]premium.InlineButton(nil), row...)
		for j := range rows[i] {
			b := &rows[i][j]
			if b.Style == "" {
				b.Style = "primary"
			}
			if role := buttonRole(b.Text); role != "" {
				b.IconCustomEmojiID = premium.ID(role)
			}
			if b.IconCustomEmojiID == "" {
				b.IconCustomEmojiID = premium.Button(b.Text, b.CallbackData, b.Style, "app").IconCustomEmojiID
			}
			b.IconCustomEmojiID = premium.ContextEmojiID(b.IconCustomEmojiID, context)
		}
	}
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

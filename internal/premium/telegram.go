package premium

import "strings"

func Emoji(name, fallback string) string {
	return CustomEmoji(ID(name), fallback)
}

type CopyText struct {
	Text string `json:"text"`
}

type InlineButton struct {
	Text              string    `json:"text"`
	URL               string    `json:"url,omitempty"`
	CallbackData      string    `json:"callback_data,omitempty"`
	CopyText          *CopyText `json:"copy_text,omitempty"`
	Style             string    `json:"style,omitempty"`
	IconCustomEmojiID string    `json:"icon_custom_emoji_id,omitempty"`
}

type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

type KeyboardButton struct {
	Text              string `json:"text"`
	Style             string `json:"style,omitempty"`
	IconCustomEmojiID string `json:"icon_custom_emoji_id,omitempty"`
}

type ReplyKeyboard struct {
	Keyboard       [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard bool               `json:"resize_keyboard,omitempty"`
	IsPersistent   bool               `json:"is_persistent,omitempty"`
}

func Button(text, callback, style, emojiName string) InlineButton {
	if style == "" {
		style = "primary"
	}
	if role, ok := actionEmoji[callback]; ok {
		emojiName = role
	}
	if callback == "menu:withdraw" {
		style = "success"
	}
	if callback == "menu:api" || callback == "menu:premium" || callback == "admin:broadcast" || callback == "admin:withdrawals" || callback == "admin:admins" {
		style = "primary"
	}
	// Navigation has one meaning across every theme and legacy menu.
	if callback == "menu:home" || callback == "menu:compact" || callback == "menu:admin" {
		emojiName, style = "home", "primary"
	} else if strings.HasPrefix(strings.ToLower(text), "back") {
		emojiName, style = "back", "primary"
	}
	if strings.HasPrefix(strings.ToLower(text), "cancel") {
		emojiName = "cancel"
		style = "danger"
	}
	if strings.HasPrefix(strings.ToLower(text), "delete") || strings.HasPrefix(strings.ToLower(text), "remove") {
		emojiName = "trash"
		style = "danger"
	}
	if strings.HasPrefix(strings.ToLower(text), "confirm") {
		emojiName = "check"
		if style != "danger" {
			style = "success"
		}
	}
	if strings.HasPrefix(strings.ToLower(text), "refresh") {
		emojiName = "refresh"
	}
	return InlineButton{Text: text, CallbackData: callback, Style: style, IconCustomEmojiID: ID(emojiName)}
}

var actionEmoji = map[string]string{
	"menu:services": "phone", "menu:profile": "user", "menu:stats": "chart", "menu:help": "help", "menu:settings": "settings",
	"menu:themes": "palette", "menu:premium": "premium", "menu:withdraw": "withdraw", "menu:schedule": "calendar", "menu:api": "developer", "menu:tutorials": "book",
	"menu:createbot": "bot", "menu:mybots": "bot", "menu:liveotp": "live", "tools:favorites:0": "favorite", "tools:alerts:0": "bell",
	"admin:broadcast": "megaphone", "admin:analytics": "chart", "admin:panels": "satellite", "admin:groups": "people", "admin:bots": "bot",
	"admin:settings": "settings", "admin:users": "people", "admin:admins": "shield", "admin:withdrawals": "withdraw", "admin:rewards": "gift",
	"admin:required": "lock", "admin:patterns": "puzzle", "admin:themes": "palette", "admin:tutorials": "book", "admin:numbers": "folder",
}

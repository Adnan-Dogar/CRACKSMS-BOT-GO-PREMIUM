package premium

import (
	"fmt"
	"html"
)

// IDs are carried forward from the OTP-BOT-PRO Go base. Telegram clients that
// cannot render custom emoji display the fallback character inside the tag.
var emojiIDs = map[string]string{
	"otp":       "6204162490515855272",
	"channel":   "6206497372176913599",
	"number":    "6190336264940559752",
	"check":     "6206479140040743133",
	"lock":      "5296369303661067030",
	"chart":     "5343862721307748990",
	"phone":     "5282843764451195532",
	"money":     "5303479226882603449",
	"celebrate": "5461151367559141950",
	"gold":      "5440539497383087970",
	"silver":    "5447203607294265305",
	"bronze":    "5453902265922376865",
	"link":      "5271604874419647061",
	"message":   "5260537253839621019",
	"developer": "5372981976804366741",
	"support":   "5370858062262433518",
	"settings":  "5375145114223463491",
	"history":   "5373012445396459708",
	"premium":   "5436113877181941026",
	"admin":     "5379774506432444529",
	"bot":       "5377474517305876998",
}

func Emoji(name, fallback string) string {
	id := emojiIDs[name]
	if id == "" {
		return html.EscapeString(fallback)
	}
	return fmt.Sprintf(`<tg-emoji emoji-id="%s">%s</tg-emoji>`, id, html.EscapeString(fallback))
}

func ID(name string) string { return emojiIDs[name] }

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
	return InlineButton{Text: text, CallbackData: callback, Style: style, IconCustomEmojiID: ID(emojiName)}
}

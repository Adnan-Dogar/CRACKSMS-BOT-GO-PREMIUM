package premium

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
	return InlineButton{Text: text, CallbackData: callback, Style: style, IconCustomEmojiID: ID(emojiName)}
}

package themes

import (
	"fmt"
	"html"
	"strings"

	"github.com/adnan-dogar/cracksms-vnext/internal/country"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
)

type Theme struct {
	ID          int
	Name        string
	Badge       string
	CopyLabel   string
	CopyStyle   string
	ShowMessage bool
	ShowNumber  bool
	ShowChannel bool
	ShowDev     bool
	ShowSupport bool
}

var catalog = []Theme{
	{0, "Classic", "❄️", "Copy OTP", "success", true, true, true, false, false},
	{1, "Minimal", "🎯", "Copy", "success", false, false, false, false, false},
	{2, "Developer", "💻", "Copy", "success", false, false, false, true, true},
	{3, "Electric", "⚡", "Copy OTP", "success", false, true, false, false, false},
	{4, "Tech", "🔬", "Copy Code", "success", true, true, false, true, false},
	{5, "Premium", "💎", "Copy OTP", "success", true, true, true, false, false},
	{6, "UltraMinimal", "🎲", "OTP", "success", false, false, false, false, false},
	{7, "Business", "💼", "Copy OTP", "success", false, true, false, false, true},
	{8, "Social", "🌐", "Copy OTP", "success", false, false, true, true, false},
	{9, "Deluxe", "🌟", "Copy OTP", "success", true, true, true, true, true},
}

type Links struct {
	Channel   string
	NumberBot string
	Developer string
	Support   string
}

func Catalog() []Theme { return append([]Theme(nil), catalog...) }

func Get(id int) Theme {
	if id < 0 || id >= len(catalog) {
		id = 0
	}
	return catalog[id]
}

func Format(event domain.OTPEvent, themeID int, forUser bool, visibility string) string {
	theme := Get(themeID)
	if forUser {
		visibility = "visible"
	}
	phone := "+" + event.NormalizedPhone
	if !forUser {
		phone = maskPhone(event.NormalizedPhone)
	}
	code, message := privacyText(event.Code, event.Message, visibility)
	panel := defaultText(event.PanelName, "Unknown")
	serviceName := defaultText(event.Service, "Unknown")
	service := premium.AppEmoji(event.Service, country.ServiceEmoji(event.Service)) + " " + html.EscapeString(serviceName)
	location := country.Detect(event.NormalizedPhone)
	if event.Country != "" {
		location.Name = event.Country
	}
	if location.Name != "" {
		service += " · " + premium.CountryFlag(location.Code, country.Flag(location.Code)) + " " + html.EscapeString(location.Name)
	}
	plainService := serviceName
	if location.Name != "" {
		plainService += " · " + location.Name
	}

	switch theme.ID {
	case 1:
		return fmt.Sprintf("%s <b>%s</b> · <code>%s</code> · <code>%s</code>", theme.Badge, service, html.EscapeString(phone), html.EscapeString(code))
	case 2:
		return fmt.Sprintf("%s <b>OTP EVENT</b>\n%s\n<pre>panel=%s\nservice=%s\nnumber=%s\ncode=%s</pre>", theme.Badge, service,
			html.EscapeString(panel), html.EscapeString(plainService), html.EscapeString(phone), html.EscapeString(code))
	case 3:
		return fmt.Sprintf("⚡ <b>%s OTP</b> ⚡\n\n📱 <code>%s</code>\n🔑 <code>%s</code>", service, html.EscapeString(phone), html.EscapeString(code))
	case 4:
		return fmt.Sprintf("🔬 <b>TECH OTP SIGNAL</b>\n\n📡 %s\n🧪 %s\n☎️ <code>%s</code>\n🔐 <code>%s</code>\n\n<blockquote>%s</blockquote>",
			html.EscapeString(panel), service, html.EscapeString(phone), html.EscapeString(code), html.EscapeString(message))
	case 5:
		return fmt.Sprintf("💎 ━━━ <b>PREMIUM OTP</b> ━━━ 💎\n\n%s <b>Panel:</b> %s\n📱 <b>Service:</b> %s\n☎️ <code>%s</code>\n🔑 <b>OTP:</b> <code>%s</code>\n\n<blockquote>%s</blockquote>",
			premium.Emoji("gold", "✨"), html.EscapeString(panel), service, html.EscapeString(phone), html.EscapeString(code), html.EscapeString(message))
	case 6:
		return fmt.Sprintf("🎲 %s  •  <code>%s</code>  •  <code>%s</code>", service, html.EscapeString(phone), html.EscapeString(code))
	case 7:
		return fmt.Sprintf("💼 <b>BUSINESS VERIFICATION</b>\n\n<b>Provider</b>  %s\n<b>Product</b>  %s\n<b>Account</b>  <code>%s</code>\n<b>Code</b>  <code>%s</code>",
			html.EscapeString(panel), service, html.EscapeString(phone), html.EscapeString(code))
	case 8:
		return fmt.Sprintf("🌐 <b>New %s code</b>\n\n👥 <code>%s</code>\n🔑 <code>%s</code>\n\n💬 %s", service, html.EscapeString(phone), html.EscapeString(code), html.EscapeString(message))
	case 9:
		return fmt.Sprintf("🌟 ━━━━━ <b>DELUXE OTP</b> ━━━━━ 🌟\n\n📡 <b>Panel:</b> %s\n📱 <b>Service:</b> %s\n☎️ <b>Number:</b> <code>%s</code>\n🔑 <b>OTP:</b> <code>%s</code>\n\n📝 <b>Full message</b>\n<blockquote>%s</blockquote>",
			html.EscapeString(panel), service, html.EscapeString(phone), html.EscapeString(code), html.EscapeString(message))
	default:
		return fmt.Sprintf("❄️ ━━━━━━━━━━━━━━━━━━ 🧊\n%s <b>New OTP</b>\n\n📡 <b>Panel:</b> %s\n📱 <b>Service:</b> %s\n☎️ <b>Number:</b> <code>%s</code>\n🔑 <b>OTP:</b> <code>%s</code>\n\n💬 <b>Message:</b>\n<blockquote>%s</blockquote>\n❄️ ━━━━━━━━━━━━━━━━━━ 🧊",
			premium.Emoji("lock", "🔐"), html.EscapeString(panel), service, html.EscapeString(phone), html.EscapeString(code), html.EscapeString(message))
	}
}

func Keyboard(event domain.OTPEvent, themeID int, links Links, exposeOTP bool) premium.InlineKeyboard {
	theme := Get(themeID)
	var rows [][]premium.InlineButton
	if exposeOTP && event.Code != "" {
		rows = append(rows, []premium.InlineButton{{
			Text: theme.CopyLabel + ": " + event.Code, CopyText: &premium.CopyText{Text: event.Code},
			Style: theme.CopyStyle, IconCustomEmojiID: premium.ID("otp"),
		}})
		if theme.ShowMessage && event.Message != "" {
			rows = append(rows, []premium.InlineButton{{
				Text: "Full Message", CopyText: &premium.CopyText{Text: event.Message}, Style: "primary", IconCustomEmojiID: premium.ID("message"),
			}})
		}
	}
	var linkRow []premium.InlineButton
	if theme.ShowNumber && links.NumberBot != "" {
		linkRow = append(linkRow, premium.InlineButton{Text: "Get Numbers", URL: links.NumberBot, Style: "primary", IconCustomEmojiID: premium.ID("number")})
	}
	if theme.ShowChannel && links.Channel != "" {
		linkRow = append(linkRow, premium.InlineButton{Text: "Community", URL: links.Channel, Style: "success", IconCustomEmojiID: premium.ID("channel")})
	}
	if len(linkRow) > 0 {
		rows = append(rows, linkRow)
	}
	var contactRow []premium.InlineButton
	if theme.ShowDev && links.Developer != "" {
		contactRow = append(contactRow, premium.InlineButton{Text: "Developer", URL: links.Developer, Style: "primary", IconCustomEmojiID: premium.ID("developer")})
	}
	if theme.ShowSupport && links.Support != "" {
		contactRow = append(contactRow, premium.InlineButton{Text: "Support", URL: links.Support, Style: "danger", IconCustomEmojiID: premium.ID("support")})
	}
	if len(contactRow) > 0 {
		rows = append(rows, contactRow)
	}
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func privacyText(code, message, visibility string) (string, string) {
	if code == "" {
		code = "Not detected"
	}
	switch visibility {
	case "hidden":
		return "[HIDDEN]", redact(message, code, "[HIDDEN]")
	case "masked":
		masked := strings.Repeat("•", max(0, len(code)-2)) + last(code, 2)
		return masked, redact(message, code, masked)
	default:
		return code, message
	}
}

func redact(message, code, replacement string) string {
	if code == "" || code == "Not detected" {
		return message
	}
	return strings.ReplaceAll(message, code, replacement)
}

func maskPhone(phone string) string {
	if len(phone) <= 5 {
		return "+" + phone
	}
	prefix := 3
	if len(phone) < 8 {
		prefix = 1
	}
	return "+" + phone[:prefix] + strings.Repeat("•", len(phone)-prefix-4) + phone[len(phone)-4:]
}

func last(value string, count int) string {
	if len(value) <= count {
		return value
	}
	return value[len(value)-count:]
}

func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

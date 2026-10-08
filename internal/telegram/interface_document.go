package telegram

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
)

var interfaceBlockTags = regexp.MustCompile(`(?i)</?(?:blockquote|pre)(?:\s[^>]*)?>`)
var interfaceField = regexp.MustCompile(`^(.{1,100}?):\s+(.+)$`)

// Use tables for actual label/value data. Short prompts and prose read better
// as paragraphs; OTP themes and authored broadcasts have separate renderers.
func interfaceDocument(text string, keyboard premium.InlineKeyboard) screenDocument {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	title := lines[0]
	body := lines[1:]
	hasIcon := strings.Contains(premium.AnimateHTML(title), "<tg-emoji")
	if !hasIcon {
		role, fallback := "info", "ℹ️"
		lower := strings.ToLower(title)
		switch {
		case strings.Contains(lower, "permission") || strings.Contains(lower, "authorized"):
			role, fallback = "lock", "🔒"
		case strings.Contains(lower, "expired") || strings.Contains(lower, "failed") || strings.Contains(lower, "invalid"):
			role, fallback = "warning", "⚠️"
		case strings.Contains(lower, "saved") || strings.Contains(lower, "completed") || strings.Contains(lower, "imported"):
			role, fallback = "check", "✅"
		}
		title = premium.Emoji(role, fallback) + " " + title
	}
	inline := func(v string) string { return interfaceBlockTags.ReplaceAllString(v, "") }
	var rich strings.Builder
	rich.WriteString("<h3>" + inline(title) + "</h3>")
	fieldCount := 0
	nonBlank := 0
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonBlank++
		if interfaceField.MatchString(inline(line)) {
			fieldCount++
		}
	}
	useTable := fieldCount >= 2 && fieldCount*2 >= nonBlank
	tableOpen := false
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			if tableOpen {
				rich.WriteString("</table>")
				tableOpen = false
			}
			continue
		}
		line = inline(line)
		if fields := interfaceField.FindStringSubmatch(line); useTable && fields != nil {
			if !tableOpen {
				rich.WriteString("<table bordered striped compact>")
				tableOpen = true
			}
			rich.WriteString("<tr><td>" + fields[1] + "</td><td>" + fields[2] + "</td></tr>")
			continue
		}
		if tableOpen {
			rich.WriteString("</table>")
			tableOpen = false
		}
		rich.WriteString("<p>" + line + "</p>")
	}
	if tableOpen {
		rich.WriteString("</table>")
	}
	// Use a visible icon for short status replies too, retaining their exact
	// information in classic mode rather than exposing rich HTML tags.
	classic := title
	if len(body) > 0 {
		classic += "\n" + strings.Join(body, "\n")
	}
	return screenDocument{RichHTML: rich.String(), ClassicHTML: classic, Keyboard: keyboard}
}

func buttonRole(text string) string {
	label := strings.ToLower(strings.TrimSpace(text))
	label = strings.TrimLeftFunc(label, func(r rune) bool { return !unicode.IsLetter(r) })
	switch label {
	case "home", "main menu", "admin home", "admin menu":
		return "home"
	case "back", "previous", "back / edit":
		return "back"
	case "cancel", "cancel import", "cancel broadcast":
		return "cancel"
	case "search", "search apps":
		return "focus"
	case "refresh", "reconnect", "retry":
		return "refresh"
	}
	return ""
}

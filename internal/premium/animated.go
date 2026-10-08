package premium

import (
	"sort"
	"strings"
	"unicode/utf8"
)

type animatedGlyph struct {
	fallback string
	id       string
}

// These are the UI custom-emoji IDs from CrackSMS. The renderer is applied to
// text nodes only: it never rewrites HTML tags, existing tg-emoji fallbacks,
// or literal code/pre blocks.
var animatedGlyphs = []animatedGlyph{
	{"🛡️", ID("shield")}, {"⚙️", ID("settings")}, {"🗣️", ID("message")},
	{"❄️", ID("snow")}, {"✂️", ID("scissors")}, {"⚠️", ID("warning")},
	{"ℹ️", ID("info")}, {"▶️", ID("play")}, {"⏹️", ID("stop")},
	{"⬇️", ID("download")}, {"☄️", ID("fire")},
	{"🔥", ID("fire")}, {"⚡️", ID("bolt")}, {"⚡", ID("bolt")}, {"👑", ID("crown")},
	{"💎", ID("diamond")}, {"⭐", ID("deluxe_star")}, {"🌟", ID("deluxe_star")},
	{"🔑", ID("key")}, {"🔒", ID("lock")}, {"🔐", ID("lock")},
	{"🤖", ID("bot")}, {"🛡", ID("shield")}, {"🚀", ID("rocket")},
	{"⚙", ID("settings")}, {"📊", ID("chart")}, {"🔔", ID("bell")},
	{"💀", ID("skull")}, {"✅", ID("check")}, {"❌", ID("cancel")},
	{"🚫", ID("cancel")}, {"🌍", ID("earth")}, {"🌐", ID("globe")},
	{"📱", ID("phone")}, {"🖥", ID("desktop")}, {"💬", ID("chat")}, {"🗣", ID("message")},
	{"📞", ID("receiver")}, {"☎️", ID("telephone")}, {"☎", ID("telephone")},
	{"📡", ID("satellite")}, {"✈️", ID("plane")}, {"✈", ID("plane")}, {"🕐", ID("clock")}, {"⏰", ID("alarm")},
	{"🕓", ID("timezone")}, {"📍", ID("pushpin")}, {"❄", ID("snow")},
	{"🧊", ID("ice")}, {"📢", ID("megaphone")}, {"📣", ID("megaphone")},
	{"📄", ID("document")}, {"📝", ID("notepad")}, {"✉️", ID("envelope")}, {"✉", ID("envelope")}, {"✂", ID("scissors")},
	{"💻", ID("laptop")}, {"🧑‍💻", ID("developer")}, {"👥", ID("people")},
	{"👋", ID("people")}, {"👤", ID("user")}, {"👮", ID("police")},
	{"🔬", ID("microscope")}, {"🧪", ID("laboratory")}, {"📋", ID("copy")},
	{"📩", ID("inbox")}, {"📨", ID("inbox")}, {"📭", ID("empty_inbox")},
	{"🎲", ID("dice")}, {"🎯", ID("focus")}, {"💯", ID("hundred")}, {"🎁", ID("gift")},
	{"📖", ID("book")}, {"📚", ID("books")}, {"🔗", ID("link")},
	{"🔙", ID("back")}, {"🗑", ID("trash")}, {"❓", ID("help")},
	{"🟢", ID("online")}, {"🔴", ID("offline")}, {"🔄", ID("refresh")},
	{"⌛", ID("hourglass")}, {"💰", ID("money")}, {"💵", ID("withdraw")},
	{"💸", ID("withdraw")}, {"💲", ID("dollar")}, {"💳", ID("card")},
	{"📂", ID("folder")}, {"📤", ID("upload")}, {"✏️", ID("edit")},
	{"✏", ID("edit")}, {"✨", ID("sparkles")}, {"🔌", ID("plug")},
	{"💼", ID("briefcase")}, {"🤝", ID("handshake")}, {"🏆", ID("trophy")},
	{"👩‍🎨", ID("palette")}, {"👨‍🎨", ID("paint")}, {"🎨", ID("palette")},
	{"🧩", ID("puzzle")}, {"🏠", ID("home")}, {"🚨", ID("warning")},
	{"➕", ID("add")}, {"🗓", ID("calendar")}, {"📅", ID("calendar")}, {"📥", ID("inbox")},
}

func init() {
	sort.SliceStable(animatedGlyphs, func(i, j int) bool {
		return len(animatedGlyphs[i].fallback) > len(animatedGlyphs[j].fallback)
	})
}

func AnimateHTML(input string) string {
	var out strings.Builder
	out.Grow(len(input) + len(input)/4)
	protectedDepth := 0
	for index := 0; index < len(input); {
		if input[index] == '<' {
			end := strings.IndexByte(input[index:], '>')
			if end < 0 {
				out.WriteString(input[index:])
				break
			}
			end += index
			tag := input[index : end+1]
			name, closing, selfClosing := htmlTag(tag)
			out.WriteString(tag)
			if isProtectedTag(name) {
				if closing {
					if protectedDepth > 0 {
						protectedDepth--
					}
				} else if !selfClosing {
					protectedDepth++
				}
			}
			index = end + 1
			continue
		}
		if protectedDepth == 0 {
			matched := false
			for _, glyph := range animatedGlyphs {
				if strings.HasPrefix(input[index:], glyph.fallback) {
					out.WriteString(CustomEmoji(glyph.id, glyph.fallback))
					index += len(glyph.fallback)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			r, size := utf8.DecodeRuneInString(input[index:])
			if isEmojiRune(r) {
				end := index + size
				if end < len(input) {
					next, nextSize := utf8.DecodeRuneInString(input[end:])
					if next == '\ufe0f' {
						end += nextSize
					}
				}
				out.WriteString(input[index:end])
				index = end
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(input[index:])
		if size == 0 {
			break
		}
		out.WriteString(input[index : index+size])
		index += size
	}
	return out.String()
}

func isEmojiRune(r rune) bool {
	return (r >= 0x1F000 && r <= 0x1FAFF) ||
		(r >= 0x2600 && r <= 0x27BF) ||
		(r >= 0x1F1E6 && r <= 0x1F1FF)
}

func htmlTag(tag string) (name string, closing, selfClosing bool) {
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">"))
	if strings.HasPrefix(body, "!") || strings.HasPrefix(body, "?") {
		return "", false, true
	}
	closing = strings.HasPrefix(body, "/")
	body = strings.TrimSpace(strings.TrimPrefix(body, "/"))
	selfClosing = strings.HasSuffix(body, "/")
	body = strings.TrimSpace(strings.TrimSuffix(body, "/"))
	if field := strings.Fields(body); len(field) > 0 {
		name = strings.ToLower(field[0])
	}
	return name, closing, selfClosing
}

func isProtectedTag(name string) bool {
	return name == "tg-emoji" || name == "code" || name == "pre"
}

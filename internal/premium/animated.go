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
	{"🔥", "5773906538459573336"},
	{"⚡", "5461151367559362727"},
	{"👑", "5392399685018067802"},
	{"💎", "5471952986970267163"},
	{"⭐", "5368324170671202286"},
	{"🔑", "5472211234521076011"},
	{"🔒", "5472308992514464048"},
	{"🤖", "5361215897565626609"},
	{"🛡️", "5359311622483678195"},
	{"🛡", "5359311622483678195"},
	{"🚀", "5395303611011550609"},
	{"⚙️", "5359831736784843489"},
	{"⚙", "5359831736784843489"},
	{"📊", "5359735404426468588"},
	{"🔔", "5359766118363525030"},
	{"💀", "5350934059607329445"},
	{"✅", "5368324170671202286"},
	{"🌍", "5368324170671202286"},
	{"📱", "5359831736784843489"},
	{"💬", "5359735404426468588"},
	{"🗣️", "5359766118363525030"},
	{"🗣", "5359766118363525030"},
	{"📞", "5359311622483678195"},
	{"📡", "5359831736784843489"},
	{"🕐", "5368324170671202286"},
	{"📍", "5359831736784843489"},
	{"❄️", "5359831736784843489"},
	{"❄", "5359831736784843489"},
	{"🧊", "5359831736784843489"},
	{"📢", "5359735404426468588"},
	{"📄", "5359735404426468588"},
	{"📝", "5359735404426468588"},
	{"✂️", "5359831736784843489"},
	{"✂", "5359831736784843489"},
	{"💻", "5359831736784843489"},
	{"👥", "5359735404426468588"},
	{"🌐", "5359831736784843489"},
	{"🔬", "5359831736784843489"},
	{"📋", "5359735404426468588"},
	{"📩", "5359735404426468588"},
	{"🎲", "5359831736784843489"},
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

package premium

import (
	"strings"
	"testing"
)

func TestAnimateHTMLOnlyRewritesSafeTextNodes(t *testing.T) {
	input := `🔥 <b>📊 Stats</b> <code>🔑 literal</code> <tg-emoji emoji-id="999">📱</tg-emoji>`
	got := AnimateHTML(input)
	if !strings.Contains(got, `<tg-emoji emoji-id="`+ID("fire")+`">🔥</tg-emoji>`) {
		t.Fatalf("fire was not animated: %s", got)
	}
	if !strings.Contains(got, `<b><tg-emoji emoji-id="`+ID("chart")+`">📊</tg-emoji> Stats</b>`) {
		t.Fatalf("text inside formatting tag was not animated: %s", got)
	}
	if !strings.Contains(got, `<code>🔑 literal</code>`) {
		t.Fatalf("code content was rewritten: %s", got)
	}
	if strings.Count(got, `emoji-id="999"`) != 1 || strings.Contains(got, `emoji-id="999"><tg-emoji`) {
		t.Fatalf("existing custom emoji was nested: %s", got)
	}
}

func TestAnimateHTMLWrapsMappedAndPreservesUnknownEmoji(t *testing.T) {
	got := AnimateHTML("💳 📂 🥳")
	if strings.Count(got, "<tg-emoji ") != 2 {
		t.Fatalf("known icons must animate: %s", got)
	}
	if !strings.Contains(got, `emoji-id="`+ID("card")+`"`) || !strings.Contains(got, `emoji-id="`+ID("folder")+`"`) {
		t.Fatalf("replacement keys were not used: %s", got)
	}
	if !strings.HasSuffix(got, "🥳") {
		t.Fatalf("unknown emoji changed: %s", got)
	}
}

func TestAllSuppliedReplacementEmojiIDsAreResolved(t *testing.T) {
	keys := ReplacementEmojiKeys()
	if len(keys) != 0 {
		t.Fatalf("custom emoji IDs still need replacement: %v", keys)
	}
	for key, id := range replaceCustomEmojiIDs {
		if !numericCustomEmojiID.MatchString(id) || ID(key) != id {
			t.Fatalf("key %q does not use its resolved Telegram custom emoji ID", key)
		}
	}
}

func TestSuppliedCompoundEmojiUseTheirExactCustomIDs(t *testing.T) {
	got := AnimateHTML("👩‍🎨 👨‍🎨 ⚡️ ✈️ ✉️ 🚨")
	for _, id := range []string{
		"5258215635996908355",
		"5258450450448915742",
		"5456140674028019486",
		"5258073068852485953",
		"5253742260054409879",
		"6257780484281997093",
	} {
		if !strings.Contains(got, `emoji-id="`+id+`"`) {
			t.Fatalf("supplied custom emoji ID %s was not rendered: %s", id, got)
		}
	}
}

func TestAnimateHTMLPreservesMalformedTrailingText(t *testing.T) {
	input := "✅ done <broken"
	got := AnimateHTML(input)
	if !strings.HasSuffix(got, "<broken") {
		t.Fatalf("trailing text changed: %s", got)
	}
}

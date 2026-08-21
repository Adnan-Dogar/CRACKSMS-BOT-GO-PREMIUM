package premium

import (
	"strings"
	"testing"
)

func TestAnimateHTMLOnlyRewritesSafeTextNodes(t *testing.T) {
	input := `🔥 <b>📊 Stats</b> <code>🔑 literal</code> <tg-emoji emoji-id="999">📱</tg-emoji>`
	got := AnimateHTML(input)
	if !strings.Contains(got, `<tg-emoji emoji-id="5773906538459573336">🔥</tg-emoji>`) {
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

func TestAnimateHTMLWrapsMappedAndUnknownEmoji(t *testing.T) {
	got := AnimateHTML("💳 📂 🥳")
	if strings.Count(got, "<tg-emoji ") != 3 {
		t.Fatalf("all visible emoji must be custom entities: %s", got)
	}
	if !strings.Contains(got, `emoji-id="`+ID("card")+`"`) || !strings.Contains(got, `emoji-id="`+ID("folder")+`"`) {
		t.Fatalf("replacement keys were not used: %s", got)
	}
	if !strings.Contains(got, `emoji-id="`+ID("unmapped_emoji")+`"`) {
		t.Fatalf("unknown emoji did not use the centralized fallback key: %s", got)
	}
}

func TestReplacementEmojiKeysAreExplicit(t *testing.T) {
	keys := ReplacementEmojiKeys()
	if len(keys) == 0 {
		t.Fatal("expected explicit replacement keys")
	}
	for _, key := range keys {
		if ID(key) == "" || !numericCustomEmojiID.MatchString(ID(key)) {
			t.Fatalf("key %q does not produce a Telegram-safe placeholder ID", key)
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

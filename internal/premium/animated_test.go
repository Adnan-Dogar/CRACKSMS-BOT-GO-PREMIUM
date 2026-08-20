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
	if !strings.Contains(got, `<b><tg-emoji emoji-id="5359735404426468588">📊</tg-emoji> Stats</b>`) {
		t.Fatalf("text inside formatting tag was not animated: %s", got)
	}
	if !strings.Contains(got, `<code>🔑 literal</code>`) {
		t.Fatalf("code content was rewritten: %s", got)
	}
	if strings.Count(got, `emoji-id="999"`) != 1 || strings.Contains(got, `emoji-id="999"><tg-emoji`) {
		t.Fatalf("existing custom emoji was nested: %s", got)
	}
}

func TestAnimateHTMLPreservesMalformedTrailingText(t *testing.T) {
	input := "✅ done <broken"
	got := AnimateHTML(input)
	if !strings.HasSuffix(got, "<broken") {
		t.Fatalf("trailing text changed: %s", got)
	}
}

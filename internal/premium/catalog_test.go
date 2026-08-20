package premium

import (
	"strings"
	"testing"
)

func TestLegacyCountryAndAppEmojiCatalog(t *testing.T) {
	if got := CountryEmojiID("PK"); got != "5224637061985742245" {
		t.Fatalf("Pakistan custom emoji ID=%q", got)
	}
	if got := CountryEmojiID("us"); got != "5224321781321442532" {
		t.Fatalf("US custom emoji ID=%q", got)
	}
	if got := AppEmojiID("WhatsApp Business"); got != "5334998226636390258" {
		t.Fatalf("WhatsApp custom emoji ID=%q", got)
	}
	if got := AppEmojiID("Telegram"); got != "5330237710655306682" {
		t.Fatalf("Telegram custom emoji ID=%q", got)
	}
}

func TestCustomEmojiKeepsFallbackAndEscapesIt(t *testing.T) {
	got := CustomEmoji("123", `<flag>`)
	if !strings.Contains(got, `emoji-id="123"`) || !strings.Contains(got, `&lt;flag&gt;`) {
		t.Fatalf("unexpected custom emoji HTML: %s", got)
	}
}

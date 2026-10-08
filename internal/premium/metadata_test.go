package premium

import (
	"encoding/json"
	"os"
	"testing"
)

func TestUIEmojiAssetsResolveAndAnimate(t *testing.T) {
	raw, e := os.ReadFile("emoji_metadata.json")
	if e != nil {
		t.Fatal(e)
	}
	var metadata map[string]struct {
		Emoji           string
		Animated, Video bool
	}
	if e = json.Unmarshal(raw, &metadata); e != nil {
		t.Fatal(e)
	}
	for _, registry := range []map[string]string{emojiIDs, replaceCustomEmojiIDs, countryEmojiIDs, appEmojiIDs, {"fallback": placeholderCustomEmojiID, "unknown_country": defaultCountryEmojiID, "unknown_app": defaultAppEmojiID}} {
		for key, id := range registry {
			v, ok := metadata[id]
			if !ok || (!v.Animated && !v.Video) {
				t.Errorf("%s: %s must resolve to an animated Telegram asset", key, id)
			}
		}
	}
}

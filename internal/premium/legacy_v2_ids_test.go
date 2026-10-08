package premium

import "testing"

func TestLegacyV2EmojiCatalogIsAvailableWithoutChangingPrimaryIcons(t *testing.T) {
	if len(legacyV2EmojiIDs) != 37 {
		t.Fatal("incomplete legacy V2 UI catalog", len(legacyV2EmojiIDs))
	}
	for name, id := range legacyV2EmojiIDs {
		if !numericCustomEmojiID.MatchString(id) || ID("v2_"+name) != id {
			t.Fatal("legacy V2 ID unavailable", name)
		}
	}
	if ID("phone") != "5407025283456835913" || CountryEmojiID("PK") != "5224637061985742245" || AppEmojiID("WhatsApp") != "5334998226636390258" {
		t.Fatal("primary phone, country or app icon changed")
	}
	for _, id := range []string{legacyV2DefaultAppEmojiID, legacyV2DefaultCountryEmojiID, legacyV2SecondaryUkraineEmojiID} {
		if !numericCustomEmojiID.MatchString(id) {
			t.Fatal("legacy fallback ID invalid")
		}
	}
}

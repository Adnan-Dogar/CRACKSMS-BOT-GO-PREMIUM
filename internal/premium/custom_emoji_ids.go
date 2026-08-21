package premium

import (
	"regexp"
	"sort"
)

// emojiIDs contains verified custom emoji IDs ported from the source bots.
// Keep UI IDs in this file so replacing an icon never requires editing a
// handler, menu, or theme.
var emojiIDs = map[string]string{
	"otp":        "6204162490515855272",
	"channel":    "6206497372176913599",
	"number":     "6190336264940559752",
	"check":      "6206479140040743133",
	"lock":       "5296369303661067030",
	"chart":      "5343862721307748990",
	"phone":      "5282843764451195532",
	"money":      "5303479226882603449",
	"celebrate":  "5461151367559141950",
	"gold":       "5440539497383087970",
	"silver":     "5447203607294265305",
	"bronze":     "5453902265922376865",
	"link":       "5271604874419647061",
	"message":    "5260537253839621019",
	"developer":  "5372981976804366741",
	"support":    "5370858062262433518",
	"settings":   "5375145114223463491",
	"history":    "5373012445396459708",
	"premium":    "5436113877181941026",
	"admin":      "5379774506432444529",
	"bot":        "5377474517305876998",
	"copy":       "5359735404426468588",
	"fire":       "5773906538459573336",
	"bolt":       "5461151367559362727",
	"crown":      "5392399685018067802",
	"diamond":    "5471952986970267163",
	"key":        "5472211234521076011",
	"shield":     "5359311622483678195",
	"rocket":     "5395303611011550609",
	"bell":       "5359766118363525030",
	"globe":      "5359831736784843489",
	"skull":      "5807631052251861399",
	"earth":      "5224450179368767019",
	"chat":       "5040036030414062506",
	"receiver":   "6204108584381322968",
	"satellite":  "5352564488258200671",
	"clock":      "6206508629286196237",
	"pushpin":    "5397782960512444700",
	"snow":       "5449449325434266744",
	"ice":        "5814355643892503100",
	"megaphone":  "6206080502651164081",
	"document":   "5258079129051356005",
	"notepad":    "5262974657329394511",
	"scissors":   "5235728424185649782",
	"laptop":     "5321154224191462061",
	"people":     "5242442819573927209",
	"microscope": "5377580546748588396",
	"envelope":   "6206112371308500200",
	"dice":       "5210701280384668714",
	"download":   "6204177183598974956",
	"hourglass":  "6206118633370818254",
	"gift":       "6206027872121918710",
	"user":       "5321154224191462061",
	"book":       "5411369574157286161",
	"help":       "5436113877181941026",
	"back":       "5255703720078879038",
	"trash":      "6206108815075579644",
	"cancel":     "5974083768233760323",
	"online":     "5319310205752717294",
	"offline":    "5319238892115733906",
	"info":       "5467889436807157272",
	"play":       "6265011645840363936",
	"stop":       "5454380420336466255",
	"refresh":    "5285347429737055549",
	"withdraw":   "6206155797722830770",
	"add":        "4956507094124594921",
	"focus":      "5226851658792717025",
}

// replaceCustomEmojiIDs is the only list where real IDs are still needed.
// Replace each PUT_REAL_CUSTOM_EMOJI_ID_HERE value with the numeric ID you get
// from Telegram. Until then, ID() uses placeholderCustomEmojiID so the bot
// still sends a valid custom-emoji entity instead of a plain Unicode icon.
var replaceCustomEmojiIDs = map[string]string{
	"folder":         "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"warning":        "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"police":         "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"home":           "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"inbox":          "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"puzzle":         "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"palette":        "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"calendar":       "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"empty_inbox":    "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"alarm":          "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"timezone":       "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"dollar":         "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"card":           "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"upload":         "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"edit":           "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"sparkles":       "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"plug":           "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"briefcase":      "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"deluxe_star":    "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"laboratory":     "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"telephone":      "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"books":          "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"handshake":      "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"trophy":         "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"paint":          "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"target":         "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
	"unmapped_emoji": "PUT_REAL_CUSTOM_EMOJI_ID_HERE",
}

// A verified ID keeps Telegram payloads valid until a specific replacement is
// supplied above. It is intentionally centralized and never written to data.
const placeholderCustomEmojiID = "5368324170671202286"

var numericCustomEmojiID = regexp.MustCompile(`^[0-9]{5,32}$`)

func ID(name string) string {
	if id := emojiIDs[name]; numericCustomEmojiID.MatchString(id) {
		return id
	}
	if id := replaceCustomEmojiIDs[name]; numericCustomEmojiID.MatchString(id) {
		return id
	}
	return placeholderCustomEmojiID
}

func ReplacementEmojiKeys() []string {
	keys := make([]string, 0, len(replaceCustomEmojiIDs))
	for key, id := range replaceCustomEmojiIDs {
		if !numericCustomEmojiID.MatchString(id) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

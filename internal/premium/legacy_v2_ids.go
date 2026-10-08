package premium

// LegacyV2EmojiID exposes the exact UI IDs from the public CrackSMS V2
// catalog. The primary UI map uses visually checked semantic replacements.
// Source: https://github.com/Adnan-Dogar/CRACKSMSBOTV2/blob/main/bot.py
var legacyV2EmojiIDs = map[string]string{
	"fire":       "5773906538459573336",
	"bolt":       "5461151367559362727",
	"crown":      "5392399685018067802",
	"diamond":    "5471952986970267163",
	"star":       "5368324170671202286",
	"key":        "5472211234521076011",
	"lock":       "5472308992514464048",
	"robot":      "5361215897565626609",
	"shield":     "5359311622483678195",
	"rocket":     "5395303611011550609",
	"gear":       "5359831736784843489",
	"chart":      "5359735404426468588",
	"bell":       "5359766118363525030",
	"skull":      "5350934059607329445",
	"zap":        "5461151367559362727",
	"check":      "5368324170671202286",
	"earth":      "5368324170671202286",
	"phone":      "5359831736784843489",
	"chat":       "5359735404426468588",
	"speak":      "5359766118363525030",
	"receiver":   "5359311622483678195",
	"satellite":  "5359831736784843489",
	"clock":      "5368324170671202286",
	"pushpin":    "5359831736784843489",
	"snow":       "5359831736784843489",
	"ice":        "5359831736784843489",
	"announce":   "5359735404426468588",
	"document":   "5359735404426468588",
	"notepad":    "5359735404426468588",
	"scissors":   "5359831736784843489",
	"laptop":     "5359831736784843489",
	"people":     "5359735404426468588",
	"globe":      "5359831736784843489",
	"microscope": "5359831736784843489",
	"copy":       "5359735404426468588",
	"envelope":   "5359735404426468588",
	"dice":       "5359831736784843489",
}

func LegacyV2EmojiID(name string) string { return legacyV2EmojiIDs[name] }

const legacyV2DefaultAppEmojiID = "5373026167722876724"
const legacyV2DefaultCountryEmojiID = "5222250679371839695"
const legacyV2SecondaryUkraineEmojiID = "5280587278828193324"

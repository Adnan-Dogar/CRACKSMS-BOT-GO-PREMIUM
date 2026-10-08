package premium

import (
	"regexp"
	"sort"
	"strings"
)

// emojiIDs contains animated assets from V2, verified with Telegram metadata.
// Primary action artwork was also visually inspected on 2026-09-28.
// Keep UI IDs in this file so replacing an icon never requires editing a
// handler, menu, or theme.
var emojiIDs = map[string]string{
	"list":       "5258477770735885832",
	"otp":        "5330115548900501467",
	"channel":    "5843492019128768626",
	"number":     "5407025283456835913",
	"check":      "6206479140040743133",
	"lock":       "5296369303661067030",
	"chart":      "5231200819986047254",
	"phone":      "5407025283456835913",
	"money":      "6190336264940559752",
	"celebrate":  "5461151367559141950",
	"gold":       "5440539497383087970",
	"silver":     "5447203607294265305",
	"bronze":     "5453902265922376865",
	"link":       "5271604874419647061",
	"message":    "6206495649895028694",
	"developer":  "5321154224191462061",
	"support":    "5465169893580086142",
	"settings":   "5341715473882955310",
	"history":    "5413704112220949842",
	"premium":    "5471952986970267163",
	"admin":      "5251203410396458957",
	"bot":        "5372981976804366741",
	"copy":       "5258477770735885832",
	"tap":        "6319056439096644016",
	"fire":       "5424972470023104089",
	"bolt":       "5456140674028019486",
	"crown":      "5217822164362739968",
	"diamond":    "5471952986970267163",
	"key":        "5330115548900501467",
	"shield":     "5251203410396458957",
	"rocket":     "5445284980978621387",
	"bell":       "6206508629286196237",
	"globe":      "5399898266265475100",
	"skull":      "5370971163310693562",
	"earth":      "5224450179368767019",
	"chat":       "6206495649895028694",
	"receiver":   "5800762196954716357",
	"satellite":  "5321304062715517873",
	"clock":      "5413704112220949842",
	"pushpin":    "5397782960512444700",
	"snow":       "5449449325434266744",
	"ice":        "5449449325434266744",
	"megaphone":  "5843492019128768626",
	"document":   "5258477770735885832",
	"notepad":    "5262974657329394511",
	"scissors":   "5237808360882977239",
	"laptop":     "5321154224191462061",
	"people":     "5258513401784573443",
	"microscope": "5377580546748588396",
	"envelope":   "5253742260054409879",
	"dice":       "6206027872121918710",
	"download":   "6204177183598974956",
	"hourglass":  "6206118633370818254",
	"gift":       "6206027872121918710",
	"user":       "5258362837411045098",
	"book":       "5411369574157286161",
	"help":       "5436113877181941026",
	"back":       "5258236805890710909",
	"favorite":   "5438496463044752972",
	"live":       "5456140674028019486",
	"app":        "5407025283456835913",
	"ranking":    "6194737030165959506",
	"trash":      "6206108815075579644",
	"cancel":     "5210952531676504517",
	"online":     "5416081784641168838",
	"offline":    "5411225014148014586",
	"info":       "5334544901428229844",
	"play":       "5264919878082509254",
	"stop":       "5454380420336466255",
	"refresh":    "5375338737028841420",
	"withdraw":   "6206155797722830770",
	"add":        "6206375377925839184",
	"focus":      "6032732324049718580",
	"desktop":    "5282843764451195532",
	"plane":      "5258073068852485953",
	"hundred":    "5341498088408234504",
}

// replaceCustomEmojiIDs contains the custom emoji IDs supplied after the
// initial source-bot migration. Keep them separate so ReplacementEmojiKeys
// can report any future entry that is deliberately left non-numeric.
var replaceCustomEmojiIDs = map[string]string{
	"folder":         "5257965810634202885",
	"warning":        "6257780484281997093",
	"police":         "5287388737498529298",
	"home":           "5416041192905265756",
	"inbox":          "5776303079758500750",
	"puzzle":         "4958903389523018769",
	"palette":        "5258215635996908355",
	"calendar":       "5258105663359294787",
	"empty_inbox":    "5776303079758500750",
	"alarm":          "5413704112220949842",
	"timezone":       "5399898266265475100",
	"dollar":         "6190336264940559752",
	"card":           "5389078268689265131",
	"upload":         "5433614747381538714",
	"edit":           "6204162490515855272",
	"sparkles":       "5325547803936572038",
	"plug":           "5456140674028019486",
	"briefcase":      "6206236607532504295",
	"deluxe_star":    "5438496463044752972",
	"laboratory":     "4956561910792192697",
	"telephone":      "5332531536723984111",
	"books":          "5411369574157286161",
	"handshake":      "5800745206064093065",
	"trophy":         "6194737030165959506",
	"paint":          "5258450450448915742",
	"target":         "6032732324049718580",
	"unmapped_emoji": "5334544901428229844",
}

// A verified ID keeps Telegram payloads valid for unknown programmatic keys.
// It is intentionally centralized and never written to data.
const placeholderCustomEmojiID = "5334544901428229844"

var numericCustomEmojiID = regexp.MustCompile(`^[0-9]{5,32}$`)

func ID(name string) string {
	if strings.HasPrefix(name, "v2_") {
		if id := LegacyV2EmojiID(strings.TrimPrefix(name, "v2_")); numericCustomEmojiID.MatchString(id) {
			return id
		}
	}
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

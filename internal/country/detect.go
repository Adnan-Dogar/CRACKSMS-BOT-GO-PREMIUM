package country

import (
	"sort"
	"strings"
)

type Info struct {
	Code string
	Name string
}

var callingCodes = map[string]Info{
	"1": {"US", "United States/Canada"}, "7": {"RU", "Russia/Kazakhstan"}, "20": {"EG", "Egypt"},
	"27": {"ZA", "South Africa"}, "30": {"GR", "Greece"}, "31": {"NL", "Netherlands"}, "32": {"BE", "Belgium"},
	"33": {"FR", "France"}, "34": {"ES", "Spain"}, "36": {"HU", "Hungary"}, "39": {"IT", "Italy"},
	"40": {"RO", "Romania"}, "41": {"CH", "Switzerland"}, "43": {"AT", "Austria"}, "44": {"GB", "United Kingdom"},
	"45": {"DK", "Denmark"}, "46": {"SE", "Sweden"}, "47": {"NO", "Norway"}, "48": {"PL", "Poland"},
	"49": {"DE", "Germany"}, "51": {"PE", "Peru"}, "52": {"MX", "Mexico"}, "53": {"CU", "Cuba"},
	"54": {"AR", "Argentina"}, "55": {"BR", "Brazil"}, "56": {"CL", "Chile"}, "57": {"CO", "Colombia"},
	"58": {"VE", "Venezuela"}, "60": {"MY", "Malaysia"}, "61": {"AU", "Australia"}, "62": {"ID", "Indonesia"},
	"63": {"PH", "Philippines"}, "64": {"NZ", "New Zealand"}, "65": {"SG", "Singapore"}, "66": {"TH", "Thailand"},
	"81": {"JP", "Japan"}, "82": {"KR", "South Korea"}, "84": {"VN", "Vietnam"}, "86": {"CN", "China"},
	"90": {"TR", "Turkey"}, "91": {"IN", "India"}, "92": {"PK", "Pakistan"}, "93": {"AF", "Afghanistan"},
	"94": {"LK", "Sri Lanka"}, "95": {"MM", "Myanmar"}, "98": {"IR", "Iran"}, "211": {"SS", "South Sudan"},
	"212": {"MA", "Morocco"}, "213": {"DZ", "Algeria"}, "216": {"TN", "Tunisia"}, "218": {"LY", "Libya"},
	"220": {"GM", "Gambia"}, "221": {"SN", "Senegal"}, "223": {"ML", "Mali"}, "225": {"CI", "Ivory Coast"},
	"226": {"BF", "Burkina Faso"}, "229": {"BJ", "Benin"}, "230": {"MU", "Mauritius"}, "231": {"LR", "Liberia"},
	"232": {"SL", "Sierra Leone"}, "233": {"GH", "Ghana"}, "234": {"NG", "Nigeria"}, "237": {"CM", "Cameroon"},
	"240": {"GQ", "Equatorial Guinea"}, "242": {"CG", "Congo"}, "243": {"CD", "DR Congo"}, "244": {"AO", "Angola"},
	"249": {"SD", "Sudan"}, "250": {"RW", "Rwanda"}, "251": {"ET", "Ethiopia"}, "254": {"KE", "Kenya"},
	"255": {"TZ", "Tanzania"}, "256": {"UG", "Uganda"}, "260": {"ZM", "Zambia"}, "263": {"ZW", "Zimbabwe"},
	"351": {"PT", "Portugal"}, "352": {"LU", "Luxembourg"}, "353": {"IE", "Ireland"}, "354": {"IS", "Iceland"},
	"358": {"FI", "Finland"}, "359": {"BG", "Bulgaria"}, "370": {"LT", "Lithuania"}, "371": {"LV", "Latvia"},
	"372": {"EE", "Estonia"}, "374": {"AM", "Armenia"}, "375": {"BY", "Belarus"}, "380": {"UA", "Ukraine"},
	"381": {"RS", "Serbia"}, "385": {"HR", "Croatia"}, "420": {"CZ", "Czechia"}, "421": {"SK", "Slovakia"},
	"880": {"BD", "Bangladesh"}, "886": {"TW", "Taiwan"}, "960": {"MV", "Maldives"}, "961": {"LB", "Lebanon"},
	"962": {"JO", "Jordan"}, "963": {"SY", "Syria"}, "964": {"IQ", "Iraq"}, "965": {"KW", "Kuwait"},
	"966": {"SA", "Saudi Arabia"}, "967": {"YE", "Yemen"}, "968": {"OM", "Oman"}, "970": {"PS", "Palestine"},
	"971": {"AE", "United Arab Emirates"}, "972": {"IL", "Israel"}, "973": {"BH", "Bahrain"}, "974": {"QA", "Qatar"},
	"975": {"BT", "Bhutan"}, "976": {"MN", "Mongolia"}, "977": {"NP", "Nepal"}, "992": {"TJ", "Tajikistan"},
	"993": {"TM", "Turkmenistan"}, "994": {"AZ", "Azerbaijan"}, "995": {"GE", "Georgia"}, "996": {"KG", "Kyrgyzstan"},
	"998": {"UZ", "Uzbekistan"},
}

var sortedPrefixes []string

func init() {
	for prefix := range callingCodes {
		sortedPrefixes = append(sortedPrefixes, prefix)
	}
	sort.Slice(sortedPrefixes, func(i, j int) bool { return len(sortedPrefixes[i]) > len(sortedPrefixes[j]) })
}

func Detect(phone string) Info {
	phone = digits(phone)
	for _, prefix := range sortedPrefixes {
		if strings.HasPrefix(phone, prefix) {
			return callingCodes[prefix]
		}
	}
	return Info{}
}

func Flag(code string) string {
	code = strings.ToUpper(code)
	if len(code) != 2 {
		return "🌍"
	}
	return string([]rune{rune(code[0]) - 'A' + 0x1F1E6, rune(code[1]) - 'A' + 0x1F1E6})
}

func ServiceEmoji(service string) string {
	value := strings.ToLower(service)
	for keyword, emoji := range map[string]string{
		"whatsapp": "🟢", "telegram": "✈️", "instagram": "📸", "facebook": "🔵", "messenger": "💬",
		"google": "🌈", "gmail": "✉️", "microsoft": "🪟", "apple": "🍎", "amazon": "📦", "paypal": "💳",
		"binance": "🟡", "coinbase": "🪙", "tiktok": "🎵", "snapchat": "👻", "discord": "🎮", "steam": "🎮",
		"netflix": "🎬", "uber": "🚗", "lyft": "🚕", "twitter": "🐦", "x.com": "𝕏", "linkedin": "💼",
		"yahoo": "🟣", "viber": "☎️", "wechat": "💚", "line": "🟩", "shopify": "🛍️", "twitch": "🟪",
	} {
		if strings.Contains(value, keyword) {
			return emoji
		}
	}
	return "📱"
}

func digits(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

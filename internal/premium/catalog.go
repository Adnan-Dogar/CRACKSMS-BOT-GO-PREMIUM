package premium

import (
	"html"
	"strings"
)

// countryEmojiIDs and appEmojiIDs are the animated Telegram custom-emoji
// catalogs used by the legacy CrackSMS bot. Every renderer keeps a Unicode
// fallback inside the tg-emoji entity for clients that cannot animate it.
var countryEmojiIDs = map[string]string{
	"UA": "5222250679371839695", "US": "5224321781321442532", "PL": "5224670399521892983",
	"KZ": "5222276376161171525", "AZ": "5224426544163728284", "EU": "5222108911091331711",
	"UN": "5451772687993031127", "AM": "5224369957969603463", "RU": "5280582975270963511",
	"CN": "5224435456220868088", "UZ": "5222404546575219535", "DE": "5222165617544542414",
	"JP": "5222390089715299207", "TR": "5224601903383457698", "BY": "5280820319458707404",
	"GB": "5224518800061245598", "IN": "5222300011366200403", "BR": "5224688610183228070",
	"VN": "5222359651282071925", "AE": "5224565851427976312", "TH": "5224638530864556281",
	"TZ": "5224397364155923150", "TJ": "5222217865821696536", "CH": "5224707263226194753",
	"SE": "5222201098269373561", "ES": "5222024776976970940", "KR": "5222345550904439270",
	"ZA": "5224696216570309138", "RS": "5222145396838512729", "SA": "5224698145010624573",
	"QA": "5222225596762830469", "PT": "5224404094369672274", "PH": "5222065042295376892",
	"PE": "5224482026551258766", "PK": "5224637061985742245", "OM": "5222396686785066306",
	"NO": "5224465228934163949", "NG": "5224723614166691638", "NZ": "5224573595254009705",
	"NL": "5224516489368841614", "NP": "5222444378101925267", "MA": "5224530035695693965",
	"MX": "5221971386238514431", "MY": "5224312886444174057", "KE": "5222089648163009103",
	"IQ": "5221980268230882832", "IR": "5224374154152653367", "ID": "5224405893960969756",
	"HU": "5224691998912427164", "GR": "5222463490706389920", "GH": "5224511339703056124",
	"GE": "5222152195771742239", "FR": "5222029789203804982", "FI": "5224282903277482188",
	"ET": "5224467805914542024", "EE": "5222195463272281351", "EG": "5222161185138292290",
	"DK": "5222297215342490217", "CZ": "5222073533445714675", "CO": "5224455152940886669",
	"CL": "5222350726340032308", "CA": "5222001124592071204", "BG": "5222092074819530668",
	"BE": "5224513182244024630", "BD": "5224407289825340729", "BH": "5224492892818518587",
	"AU": "5224659803837574114", "AR": "5221980461504411710", "DZ": "5224260376174015500",
	"AL": "5224312057515486246", "AF": "5222096009009575868", "ZW": "5222060442385397848",
	"VE": "5294476442854247878", "LB": "5222244425899455269", "LV": "5224401229626484931",
	"LT": "5224245902134226386", "KG": "5224388147156102493", "KW": "5221949726718442491",
	"JO": "5222292177345853436", "IT": "5222460101977190141", "IL": "5224720599099648709",
	"IE": "5224257017509588818", "RO": "5222273794885826118",
}

const defaultCountryEmojiID = "5447410659077661506"
const defaultAppEmojiID = "5407025283456835913"

var appEmojiIDs = map[string]string{
	"whatsapp":  "5334998226636390258",
	"telegram":  "5330237710655306682",
	"instagram": "5319160079465857105",
	"facebook":  "5323261730283863478",
	"google":    "5359758030198031389",
	"gmail":     "5359758030198031389",
	"twitter":   "5330337435500951363",
	"x.com":     "5330337435500951363",
	"tiktok":    "5327982530702359565",
	"snapchat":  "5330248916224983855",
	"binance":   "5359437015752401733",
}

type AppDefinition struct {
	Name          string
	CustomEmojiID string
}

func DefaultApps() []AppDefinition {
	names := []string{"WhatsApp", "Telegram", "Instagram", "Facebook", "Google", "Gmail", "Twitter / X", "TikTok", "Snapchat", "Binance"}
	out := make([]AppDefinition, 0, len(names))
	for _, name := range names {
		out = append(out, AppDefinition{Name: name, CustomEmojiID: AppEmojiID(name)})
	}
	return out
}

func CountryEmojiID(code string) string {
	if id := countryEmojiIDs[strings.ToUpper(strings.TrimSpace(code))]; id != "" {
		return id
	}
	return defaultCountryEmojiID
}

func AppEmojiID(service string) string {
	if id := appEmojiIDs[AppKey(service)]; id != "" {
		return id
	}
	return defaultAppEmojiID
}

// A stable, word-boundary match prevents random map iteration and accidental
// matches such as "nottelegram" being branded as Telegram.
func AppKey(service string) string {
	value := strings.ToLower(strings.TrimSpace(service))
	if value == "x" || value == "twitter / x" || value == "twitter/x" {
		return "twitter"
	}
	for _, key := range []string{"whatsapp", "telegram", "instagram", "facebook", "google", "gmail", "twitter", "x.com", "tiktok", "snapchat", "binance"} {
		if value == key || strings.HasPrefix(value, key+" ") || strings.HasPrefix(value, key+"-") {
			return key
		}
	}
	return value
}

func CustomEmoji(id, fallback string) string {
	if strings.TrimSpace(id) == "" {
		return html.EscapeString(fallback)
	}
	return `<tg-emoji emoji-id="` + html.EscapeString(id) + `">` + html.EscapeString(fallback) + `</tg-emoji>`
}

func CountryFlag(code, fallback string) string {
	if CountryEmojiID(code) == defaultCountryEmojiID {
		fallback = "🌐"
	}
	return CustomEmoji(CountryEmojiID(code), fallback)
}

func AppEmoji(service, fallback string) string {
	return CustomEmoji(AppEmojiID(service), fallback)
}

func AppEmojiWithID(service, customEmojiID, fallback string) string {
	if strings.TrimSpace(customEmojiID) == "" {
		customEmojiID = AppEmojiID(service)
	}
	return CustomEmoji(customEmojiID, fallback)
}

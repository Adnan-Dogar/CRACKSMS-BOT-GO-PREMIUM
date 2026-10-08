package otp

import (
	"regexp"
	"strings"
)

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:code|otp|pin|passcode|verification|confirmation|security)[^0-9]{0,30}([0-9]{3,4})[- ]([0-9]{3,4})`),
	regexp.MustCompile(`(?i)(?:code|otp|pin|passcode|verification|confirmation|security)[^0-9]{0,30}([0-9]{2,4})[- .]([0-9]{2,4})[- .]([0-9]{2,4})`),
	regexp.MustCompile(`(?i)(?:your|the)?\s*(?:otp|one[ -]?time (?:password|code)|verification code|security code|passcode|pin|code)\s*(?:is|:|=|#)?\s*([0-9]{4,8})`),
	regexp.MustCompile(`(?i)(?:auth(?:entication)?|login|sign[ -]?(?:in|up)|activate|access|confirm|validate|2fa|mfa)[^0-9]{0,40}([0-9]{4,8})`),
	regexp.MustCompile(`(?i)([0-9]{4,8})\s*(?:is|as)\s*(?:your|the)\s*(?:otp|code|pin|verification)`),
	regexp.MustCompile(`(?i)(?:do not share|never share)[^0-9]{0,60}([0-9]{4,8})`),
	regexp.MustCompile(`(?i)(?:whatsapp|telegram|facebook|instagram|google|gmail|microsoft|apple|amazon|paypal|discord|binance|tiktok|snapchat)[^0-9]{0,40}([0-9]{4,8})`),
	regexp.MustCompile(`(?i)(?:رمز|رمز التحقق|كود|کد|کوڈ|验证码|驗證碼|認証コード|인증번호|코드|код|пароль|código|codigo|senha|mot de passe|code de vérification|bestätigungscode|verifizierungscode|codice|doğrulama kodu|doğrulama|kod weryfikacyjny|ověřovací kód|overovací kód|verificatiecode|bekräftelsekod|bekreftelseskode|vahvistuskoodi|bekræftelseskode|κωδικός|אימות|รหัส|mã xác minh|kode verifikasi|kod pengesahan|ကုဒ်|पुष्टि कोड|सत्यापन कोड|पिन|যাচাইকরণ কোড|પુષ્ટિકરણ કોડ|ਪੁਸ਼ਟੀਕਰਨ ਕੋਡ|உறுதிப்படுத்தல் குறியீடு|ధృవీకరణ కోడ్|ಪರಿಶೀಲನಾ ಕೋಡ್|സ്ഥിരീകരണ കോഡ്)[^0-9]{0,40}([0-9]{4,8})`),
	regexp.MustCompile(`(?i)(?:use|enter|type|input)[^0-9]{0,30}([0-9]{4,8})[^A-Za-z0-9]{0,20}(?:to|for)[^\n]{0,30}(?:verify|confirm|login|sign in|authenticate|activate)`),
	regexp.MustCompile(`(?i)(?:\[|\(|<)([0-9]{4,8})(?:\]|\)|>)`),
	regexp.MustCompile(`(?:^|[^0-9])([0-9]{6})(?:[^0-9]|$)`),
	regexp.MustCompile(`(?:^|[^0-9])([0-9]{4,5}|[0-9]{7,8})(?:[^0-9]|$)`),
}

func Extract(message string) string {
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatch(message)
		if len(match) < 2 {
			continue
		}
		var code strings.Builder
		for _, group := range match[1:] {
			for _, r := range group {
				if r >= '0' && r <= '9' {
					code.WriteRune(r)
				}
			}
		}
		if code.Len() >= 4 && code.Len() <= 9 {
			return code.String()
		}
	}
	return ""
}
func ValidCode(code string) bool {
	if len(code) < 4 || len(code) > 9 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func ExtractWithCustom(message string, custom []string) string {
	if code := Extract(message); code != "" {
		return code
	}
	for _, raw := range custom {
		if len(raw) == 0 || len(raw) > 500 {
			continue
		}
		pattern, err := regexp.Compile(raw)
		if err != nil {
			continue
		}
		match := pattern.FindStringSubmatch(message)
		if len(match) < 2 {
			continue
		}
		var code strings.Builder
		for _, r := range match[1] {
			if r >= '0' && r <= '9' {
				code.WriteRune(r)
			}
		}
		if code.Len() >= 4 && code.Len() <= 9 {
			return code.String()
		}
	}
	return ""
}

package otp

import "testing"

func TestExtract(t *testing.T) {
	tests := map[string]string{
		"Your verification code is 123456":       "123456",
		"WhatsApp code 359-072. Do not share it": "359072",
		"رمز التحقق: 778899":                     "778899",
		"Your security code is 1234-5678":        "12345678",
		"인증번호 442211":                            "442211",
		"कृपया सत्यापन कोड 889900 दर्ज करें": "889900",
		"There is no code here": "",
	}
	for message, expected := range tests {
		if actual := Extract(message); actual != expected {
			t.Fatalf("Extract(%q)=%q, want %q", message, actual, expected)
		}
	}
}

func TestExtractWithCustomPattern(t *testing.T) {
	if actual := ExtractWithCustom("ticket=482-901", []string{`ticket=([0-9]{3}-[0-9]{3})`}); actual != "482901" {
		t.Fatalf("custom extraction=%q, want 482901", actual)
	}
}

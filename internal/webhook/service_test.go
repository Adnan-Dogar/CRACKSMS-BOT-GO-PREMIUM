package webhook

import "testing"

func TestValidateURLRejectsPrivateTargets(t *testing.T) {
	for _, value := range []string{"http://example.com/hook", "https://127.0.0.1/hook", "https://10.0.0.1/hook", "https://localhost/hook", "https://100.100.100.200/latest", "https://[64:ff9b::a9fe:a9fe]/meta", "https://0.0.0.0/hook"} {
		if err := ValidateURL(value); err == nil {
			t.Fatalf("ValidateURL(%q) unexpectedly succeeded", value)
		}
	}
	if err := ValidateURL("https://example.com/hooks/otp"); err != nil {
		t.Fatalf("public HTTPS URL rejected: %v", err)
	}
}

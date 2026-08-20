package webhook

import "testing"

func TestValidateURLRejectsPrivateTargets(t *testing.T) {
	for _, value := range []string{"http://example.com/hook", "https://127.0.0.1/hook", "https://10.0.0.1/hook", "https://localhost/hook"} {
		if err := ValidateURL(value); err == nil {
			t.Fatalf("ValidateURL(%q) unexpectedly succeeded", value)
		}
	}
	if err := ValidateURL("https://example.com/hooks/otp"); err != nil {
		t.Fatalf("public HTTPS URL rejected: %v", err)
	}
}

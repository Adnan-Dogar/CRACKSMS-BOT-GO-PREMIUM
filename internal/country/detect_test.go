package country

import "testing"

func TestDetectAndFlag(t *testing.T) {
	info := Detect("+923001234567")
	if info.Code != "PK" || info.Name != "Pakistan" || Flag(info.Code) != "🇵🇰" {
		t.Fatalf("unexpected country: %+v %s", info, Flag(info.Code))
	}
}

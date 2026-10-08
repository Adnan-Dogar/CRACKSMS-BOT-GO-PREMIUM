package telegram

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

func TestParsePositiveAmountRejectsNonFiniteValues(t *testing.T) {
	for _, value := range []string{"NaN", "nan", "Inf", "+Inf", "-1", "0", "", "abc", "1e400"} {
		if _, ok := parsePositiveAmount(value); ok {
			t.Fatalf("parsePositiveAmount(%q) accepted an invalid amount", value)
		}
	}
	if amount, ok := parsePositiveAmount(" 12,5 "); !ok || amount != 12.5 {
		t.Fatalf("decimal comma amount = %v, %v", amount, ok)
	}
}

func TestAssignmentCopyRowsOfferPerNumberAndBulkCopy(t *testing.T) {
	numbers := []domain.Number{{NormalizedPhone: "923001112233"}, {NormalizedPhone: "923004445566"}, {NormalizedPhone: "923007778899"}}
	rows := assignmentCopyRows(numbers)
	if len(rows) != 3 || len(rows[0]) != 2 || len(rows[1]) != 1 {
		t.Fatalf("unexpected copy layout: %+v", rows)
	}
	last := rows[len(rows)-1][0]
	if last.CopyText == nil || last.CopyText.Text != "+923001112233\n+923004445566\n+923007778899" {
		t.Fatalf("copy-all button = %+v", last)
	}
	for _, row := range rows {
		for _, button := range row {
			if button.IconCustomEmojiID == "" || button.Style == "" {
				t.Fatalf("copy button %q lacks premium styling", button.Text)
			}
		}
	}
	many := make([]domain.Number, assignmentCopyLimit+1)
	if assignmentCopyRows(many) != nil {
		t.Fatal("large assignments must not flood the keyboard with copy buttons")
	}
}

func TestCountryListStaysWithinTelegramLimit(t *testing.T) {
	countries := make([]store.CatalogCountry, 400)
	for i := range countries {
		countries[i] = store.CatalogCountry{Country: fmt.Sprintf("Country number %d", i), CountryCode: "PK", Available: i % 3, PricePKR: 1.5}
	}
	text := countriesText("WhatsApp", countries)
	if utf8.RuneCountInString(telegramHTMLTag.ReplaceAllString(text, "")) > 4096 || !strings.Contains(text, "more countries below") {
		t.Fatalf("country list is not capped: %d runes", utf8.RuneCountInString(text))
	}
}

func TestPriceLabelShowsBothCurrencies(t *testing.T) {
	if got := priceLabel(2, 0.05); got != "2.00 PKR + 0.0500 USD" {
		t.Fatalf("priceLabel = %q", got)
	}
	if got := priceLabel(0, 0.1); got != "0.1000 USD" {
		t.Fatalf("priceLabel = %q", got)
	}
}

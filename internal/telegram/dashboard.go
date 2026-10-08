package telegram

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	countrypkg "github.com/adnan-dogar/cracksms-vnext/internal/country"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

// Telegram rejects messages longer than 4096 visible characters. Lists that
// grow with inventory are capped well below that so large catalogs still render.
const listTextBudget = 3000

// assignmentCopyLimit keeps per-number copy buttons to a readable keyboard.
const assignmentCopyLimit = 10

// dashboardSummary is the short personal status shown on Start and Home.
// Lookup failures only hide a line; they never block navigation.
func (a *App) dashboardSummary(ctx context.Context, userID int64) string {
	var lines []string
	if pkr, usd, total, err := a.store.UserBalance(ctx, userID); err == nil {
		balance := fmt.Sprintf("<b>%.2f PKR</b>", pkr)
		if usd > 0 {
			balance += fmt.Sprintf(" · <b>%.4f USD</b>", usd)
		}
		lines = append(lines, "💰 Balance: "+balance, fmt.Sprintf("🔐 Total OTPs: <b>%d</b>", total))
	}
	if today, err := a.store.TodayOTPCount(ctx, userID); err == nil {
		lines = append(lines, fmt.Sprintf("📅 Today: <b>%d</b> OTPs", today))
	}
	if catalog, err := a.store.CatalogForInstance(ctx, a.botInstanceID); err == nil {
		stock, apps := catalogStock(catalog)
		lines = append(lines, fmt.Sprintf("📱 In stock: <b>%d</b> numbers · <b>%d</b> apps", stock, apps))
	}
	return strings.Join(lines, "\n")
}

func catalogStock(catalog map[string][]store.CatalogCountry) (stock, apps int) {
	for _, countries := range catalog {
		available := 0
		for _, country := range countries {
			available += country.Available
		}
		if available > 0 {
			apps++
		}
		stock += available
	}
	return stock, apps
}

func (a *App) sendHome(ctx context.Context, chatID, userID int64, admin bool) {
	text := "🏠 <b>Main Menu</b>"
	if summary := a.dashboardSummary(ctx, userID); summary != "" {
		text += "\n\n" + summary
	}
	text += "\n\nChoose what you want to do next."
	a.sendHTML(chatID, text, a.homeMenu(ctx, userID, admin))
}

func (a *App) handleBalance(ctx context.Context, chatID, userID int64) {
	pkr, usd, total, err := a.store.UserBalance(ctx, userID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	tier, _ := a.store.UserTier(ctx, a.botInstanceID, userID)
	today, _ := a.store.TodayOTPCount(ctx, userID)
	referrals, _, earned, _ := a.store.ReferralStats(ctx, userID)
	var text strings.Builder
	text.WriteString("👤 <b>My Account</b>\n\n")
	fmt.Fprintf(&text, "🆔 User ID: <code>%d</code>\n", userID)
	fmt.Fprintf(&text, "💎 Plan: <b>%s</b>\n", html.EscapeString(capitalize(tier)))
	fmt.Fprintf(&text, "💵 PKR balance: <b>%.2f</b>\n", pkr)
	fmt.Fprintf(&text, "💲 USD balance: <b>%.4f</b>\n", usd)
	fmt.Fprintf(&text, "🔐 Total OTPs: <b>%d</b>\n", total)
	fmt.Fprintf(&text, "📅 Today: <b>%d</b> OTPs\n", today)
	fmt.Fprintf(&text, "🤝 Referrals: <b>%d</b> · earned <b>%.2f PKR</b>\n", referrals, earned)
	text.WriteString("\nWithdraw your balance, review your OTPs, or invite friends below.")
	a.sendHTML(chatID, text.String(), profileMenu())
}

func (a *App) handleReferral(ctx context.Context, chatID, userID int64) {
	total, qualified, earned, err := a.store.ReferralStats(ctx, userID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	link := fmt.Sprintf("https://t.me/%s?start=ref%d", a.bot.Self.UserName, userID)
	var text strings.Builder
	text.WriteString("🤝 <b>Invite Friends</b>\n\n")
	fmt.Fprintf(&text, "🔗 Your link: <code>%s</code>\n", html.EscapeString(link))
	fmt.Fprintf(&text, "👥 Invited: <b>%d</b>\n", total)
	fmt.Fprintf(&text, "✅ Qualified: <b>%d</b>\n", qualified)
	fmt.Fprintf(&text, "💰 Earned: <b>%.2f PKR</b>\n", earned)
	text.WriteString("\nNew users who join with your link and complete their OTP milestone earn you a referral reward.")
	share := "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape("Get virtual numbers and OTPs instantly with CrackSMS!")
	markup := premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{{Text: "Copy Invite Link", CopyText: &premium.CopyText{Text: link}, Style: "success", IconCustomEmojiID: premium.ID("copy")},
			{Text: "Share Link", URL: share, Style: "primary", IconCustomEmojiID: premium.ID("megaphone")}},
		{premium.Button("My Account", "menu:profile", "primary", "user"), premium.Button("Main Menu", "menu:home", "primary", "home")},
	}}
	a.sendHTML(chatID, text.String(), markup)
}

func (a *App) handleServices(ctx context.Context, chatID int64) {
	catalog, err := a.store.CatalogForInstance(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	if len(catalog) == 0 {
		a.sendHTML(chatID, "📭 <b>No inventory yet</b>\n\nNo numbers are available right now. Please check back soon.", userBackMenu())
		return
	}
	stock, apps := catalogStock(catalog)
	var text strings.Builder
	fmt.Fprintf(&text, "📱 <b>Choose an Application</b>\n\n<b>%d</b> numbers ready across <b>%d</b> apps.\n", stock, apps)
	services := store.SortedServices(catalog)
	for i, service := range services {
		if text.Len() > listTextBudget {
			fmt.Fprintf(&text, "\n…and <b>%d</b> more apps below.", len(services)-i)
			break
		}
		available := 0
		for _, country := range catalog[service] {
			available += country.Available
		}
		status := fmt.Sprintf("%d available", available)
		if available == 0 {
			status = "out of stock"
		}
		fmt.Fprintf(&text, "\n%s <b>%s</b> — %d countries · %s", premium.CustomEmoji(catalogServiceEmojiID(service, catalog[service]), "📱"),
			html.EscapeString(service), len(catalog[service]), status)
	}
	text.WriteString("\n\nTap an app, then choose its country.")
	a.sendHTML(chatID, text.String(), servicesMenu(catalog))
}

func countriesText(service string, countries []store.CatalogCountry) string {
	var text strings.Builder
	fmt.Fprintf(&text, "📱 <b>%s · Choose a Country</b>\n", html.EscapeString(service))
	for i, country := range countries {
		if text.Len() > listTextBudget {
			fmt.Fprintf(&text, "\n…and <b>%d</b> more countries below.", len(countries)-i)
			break
		}
		state := "🟢"
		if country.Available == 0 {
			state = "🔴"
		}
		fmt.Fprintf(&text, "\n%s %s <b>%s</b> — %d available · %s", state, premium.CountryFlag(country.CountryCode, countrypkg.Flag(country.CountryCode)),
			html.EscapeString(country.Country), country.Available, priceLabel(country.PricePKR, country.PriceUSD))
	}
	text.WriteString("\n\nGreen countries have numbers ready now.")
	return text.String()
}

func priceLabel(pkr, usd float64) string {
	switch {
	case pkr > 0 && usd > 0:
		return fmt.Sprintf("%.2f PKR + %.4f USD", pkr, usd)
	case usd > 0:
		return fmt.Sprintf("%.4f USD", usd)
	}
	return fmt.Sprintf("%.2f PKR", pkr)
}

func (a *App) assignmentText(service string, catalog []store.CatalogCountry, assignment domain.Assignment) string {
	var text strings.Builder
	fmt.Fprintf(&text, "%s ✅ <b>%d %s number(s) assigned</b>\n\n",
		premium.CustomEmoji(catalogServiceEmojiID(service, catalog), "📱"), len(assignment.Numbers), html.EscapeString(service))
	fmt.Fprintf(&text, "🌍 Country: <b>%s</b>\n", html.EscapeString(assignment.Country))
	for i, number := range assignment.Numbers {
		fmt.Fprintf(&text, "%d. <code>+%s</code>\n", i+1, number.NormalizedPhone)
	}
	minutes := int(time.Until(assignment.ExpiresAt).Round(time.Minute) / time.Minute)
	fmt.Fprintf(&text, "\n⏳ Valid until <b>%s</b> (about %d min)\n", assignment.ExpiresAt.In(a.location).Format("03:04 PM"), max(minutes, 1))
	text.WriteString("📨 OTPs arrive here automatically. Numbers without an OTP return to stock when the time ends.")
	return text.String()
}

// assignmentCopyRows gives each assigned number a one-tap copy button.
func assignmentCopyRows(numbers []domain.Number) [][]premium.InlineButton {
	if len(numbers) == 0 || len(numbers) > assignmentCopyLimit {
		return nil
	}
	var rows [][]premium.InlineButton
	var row []premium.InlineButton
	for _, number := range numbers {
		row = append(row, premium.InlineButton{Text: "+" + number.NormalizedPhone, CopyText: &premium.CopyText{Text: "+" + number.NormalizedPhone}, Style: "success", IconCustomEmojiID: premium.ID("copy")})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	if len(numbers) > 1 {
		all := make([]string, 0, len(numbers))
		for _, number := range numbers {
			all = append(all, "+"+number.NormalizedPhone)
		}
		rows = append(rows, []premium.InlineButton{{Text: "Copy All Numbers", CopyText: &premium.CopyText{Text: strings.Join(all, "\n")}, Style: "primary", IconCustomEmojiID: premium.ID("copy")}})
	}
	return rows
}

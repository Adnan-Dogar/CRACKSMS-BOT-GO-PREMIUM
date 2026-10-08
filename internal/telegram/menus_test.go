package telegram

import (
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
)

func TestPremiumMenuCoverageAndFields(t *testing.T) {
	themeID := 5
	group := domain.OTPGroupDestination{ChatID: -1001234567890, Title: "Main OTP", Enabled: true, Healthy: true, ButtonsEnabled: true, OTPVisibility: "masked", ThemeID: &themeID}
	panel := store.PanelHealth{ID: 7, Name: "Provider", Kind: "token_api", Enabled: true, Healthy: true}
	bot := domain.BotInstance{ID: 8, Name: "Child", Status: "running", Tier: "pro", Enabled: true}
	menus := map[string]premium.InlineKeyboard{
		"compact":          compactMenu(true, true),
		"full":             fullMenu(true, true, themes.Links{Channel: "https://t.me/channel", NumberBot: "https://t.me/numbers", Developer: "https://t.me/dev", Support: "https://t.me/support"}),
		"admin":            adminDashboard(),
		"profile":          profileMenu(),
		"settings":         settingsMenu(),
		"services":         servicesMenu(map[string][]store.CatalogCountry{"WhatsApp": {{Country: "Pakistan", CountryCode: "PK", Available: 3}}}),
		"countries":        countriesMenu("WhatsApp", []store.CatalogCountry{{Country: "Pakistan", CountryCode: "PK", Available: 3}}),
		"assignment":       assignmentMenu("WhatsApp", "Pakistan"),
		"withdraw_user":    withdrawalMenu([]store.WithdrawalAccount{{ID: 3, Method: "jazzcash", DisplayHint: "0300•••567"}}),
		"withdraw_method":  withdrawalMethodMenu(),
		"withdraw_confirm": withdrawalConfirmMenu(),
		"upload_services":  uploadServicesMenu(),
		"upload_pricing":   uploadPricingMenu(),
		"upload_confirm":   uploadConfirmMenu(),
		"panel_kind":       panelKindMenu(),
		"flow_cancel":      flowCancelMenu("menu:compact"),
		"numbers":          adminNumbersMenu(),
		"panels":           adminPanelMenu([]store.PanelHealth{panel}),
		"panel_actions":    panelActionsMenu(panel),
		"groups":           adminGroupsMenu([]domain.OTPGroupDestination{group}),
		"group_actions":    groupActionsMenu(group),
		"group_themes":     groupThemesMenu(group.ChatID, group.ThemeID),
		"rewards":          adminRewardsMenu([]domain.RewardSchedule{{ID: 1, Name: "Global"}}),
		"users":            adminUsersMenu([]store.InstanceUser{{UserID: 9, FirstName: "User", Tier: "pro", TotalOTPs: 10}}),
		"user_tier":        userTierMenu(9, "pro"),
		"withdrawals":      adminWithdrawalsMenu([]store.Withdrawal{{ID: 10, UserID: 9, AmountPKR: 100}}),
		"withdrawal":       withdrawalActionsMenu(10),
		"admins":           adminAdminsMenu([]store.InstanceAdmin{{UserID: 11, FirstName: "Admin"}}),
		"bots":             adminBotsMenu([]domain.BotInstance{bot}),
		"bot_actions":      botActionsMenu(bot),
		"patterns":         adminPatternsMenu([]store.CustomOTPPattern{{ID: 12, Name: "Six digit"}}),
		"required":         adminRequiredMenu([]store.RequiredChat{{ChatID: -1001, Title: "Updates", InviteURL: "https://t.me/updates"}}),
		"tutorials":        adminTutorialsMenu([]domain.Tutorial{{ID: 13, Title: "Guide"}}),
		"admin_settings":   adminSettingsMenu(),
		"admin_themes":     adminThemesMenu(0),
		"confirmation":     confirmationMenu("admin:confirm", "menu:admin"),
		"command":          commandTemplateMenu("Copy", "/command", "menu:admin"),
	}

	for name, menu := range menus {
		if len(menu.InlineKeyboard) == 0 {
			t.Fatalf("%s menu has no rows", name)
		}
		for rowIndex, row := range menu.InlineKeyboard {
			if len(row) == 0 {
				t.Fatalf("%s row %d is empty", name, rowIndex)
			}
			for _, button := range row {
				if button.Text == "" {
					t.Fatalf("%s has a button without text", name)
				}
				if button.Style != "primary" && button.Style != "success" && button.Style != "danger" {
					t.Fatalf("%s button %q has invalid style %q", name, button.Text, button.Style)
				}
				if button.IconCustomEmojiID == "" {
					t.Fatalf("%s button %q has no custom emoji ID", name, button.Text)
				}
				for _, r := range button.Text {
					if (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) || (r >= 0x1F1E6 && r <= 0x1F1FF) {
						t.Fatalf("%s button %q contains a plain Unicode emoji; use icon_custom_emoji_id", name, button.Text)
					}
				}
				if len(button.CallbackData) > 64 {
					t.Fatalf("%s callback exceeds Telegram's 64-byte limit: %q", name, button.CallbackData)
				}
			}
		}
	}
}

func TestServiceAndCountryButtonsUseLegacyCustomIDs(t *testing.T) {
	service := servicesMenu(map[string][]store.CatalogCountry{"WhatsApp": nil}).InlineKeyboard[0][0]
	if service.IconCustomEmojiID != "5334998226636390258" {
		t.Fatalf("service ID=%q", service.IconCustomEmojiID)
	}
	country := countriesMenu("WhatsApp", []store.CatalogCountry{{Country: "Pakistan", CountryCode: "PK", Available: 1}}).InlineKeyboard[0][0]
	if country.IconCustomEmojiID != "5224637061985742245" {
		t.Fatalf("country ID=%q", country.IconCustomEmojiID)
	}
}

func TestCustomServiceButtonUsesStoredCustomID(t *testing.T) {
	service := servicesMenu(map[string][]store.CatalogCountry{"My App": {{Country: "Pakistan", CustomEmojiID: "123456789012345"}}}).InlineKeyboard[0][0]
	if service.IconCustomEmojiID != "123456789012345" {
		t.Fatalf("custom service ID=%q", service.IconCustomEmojiID)
	}
}

func TestAdminCallbackPermissionCoverage(t *testing.T) {
	for _, callback := range []string{
		"admin:numbers", "admin:upload:confirm", "admin:inventory", "admin:broadcast", "admin:analytics", "admin:users",
		"admin:withdrawals", "admin:admins", "admin:required", "admin:patterns", "admin:settings",
		"admin:themes", "admin:panels", "admin:groups", "admin:rewards", "admin:bots", "admin:tutorials",
	} {
		if permission := adminCallbackPermission(callback); permission == "" {
			t.Fatalf("callback %q has no permission gate", callback)
		}
	}
}

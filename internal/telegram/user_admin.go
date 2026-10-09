package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
)

const ledgerPageSize = 8

// ---- Admin user manager ----

func (a *App) sendUserProfile(ctx context.Context, chatID, adminID int64, query string) {
	profile, err := a.store.FindUser(ctx, a.botInstanceID, query)
	if errors.Is(err, pgx.ErrNoRows) {
		a.sendHTML(chatID, "🔎 <b>User not found</b>\n\nNo user of this bot matches <code>"+html.EscapeString(query)+"</code>. Users appear after they start the bot.", userSearchMenu())
		return
	}
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	name := strings.TrimSpace(profile.FirstName + " " + profile.LastName)
	if name == "" {
		name = "—"
	}
	username := "—"
	if profile.Username != "" {
		username = "@" + profile.Username
	}
	status := "✅ Active"
	if profile.Banned {
		status = "🚫 Banned"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "👤 <b>User Profile</b>\n\n")
	fmt.Fprintf(&text, "🆔 User ID: <code>%d</code>\n", profile.UserID)
	fmt.Fprintf(&text, "📛 Name: %s\n", html.EscapeString(name))
	fmt.Fprintf(&text, "🔗 Username: %s\n", html.EscapeString(username))
	fmt.Fprintf(&text, "📌 Status: <b>%s</b>\n", status)
	fmt.Fprintf(&text, "💎 Plan: <b>%s</b>\n", html.EscapeString(capitalize(profile.Tier)))
	fmt.Fprintf(&text, "💵 PKR balance: <b>%.2f</b>\n", profile.BalancePKR)
	fmt.Fprintf(&text, "💲 USD balance: <b>%.4f</b>\n", profile.BalanceUSD)
	fmt.Fprintf(&text, "🔐 Total OTPs: <b>%d</b>\n", profile.TotalOTPs)
	fmt.Fprintf(&text, "🤝 Referrals: <b>%d</b>\n", profile.Referrals)
	fmt.Fprintf(&text, "💸 Pending withdrawals: <b>%d</b>\n", profile.PendingWithdrawals)
	fmt.Fprintf(&text, "📅 Joined: %s\n", profile.JoinedAt.In(a.location).Format("02 Jan 2006"))
	fmt.Fprintf(&text, "🕓 Last active: %s", profile.LastActiveAt.In(a.location).Format("02 Jan 2006 15:04"))
	a.sendHTML(chatID, text.String(), a.userProfileMenu(ctx, adminID, profile))
}

func (a *App) userProfileMenu(ctx context.Context, adminID int64, profile store.UserProfile) premium.InlineKeyboard {
	id := profile.UserID
	var rows [][]premium.InlineButton
	if a.isMain {
		if allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, adminID, "manage_users"); allowed {
			ban := premium.Button("Ban User", fmt.Sprintf("admin:user:ban:%d:1", id), "danger", "stop")
			if profile.Banned {
				ban = premium.Button("Unban User", fmt.Sprintf("admin:user:ban:%d:0", id), "success", "check")
			}
			rows = append(rows,
				[]premium.InlineButton{premium.Button("Adjust Balance", fmt.Sprintf("admin:user:balance:%d", id), "primary", "money"), ban},
				[]premium.InlineButton{premium.Button("Transactions", fmt.Sprintf("admin:user:ledger:%d", id), "primary", "history")},
			)
		}
	}
	if allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, adminID, "manage_tiers"); allowed {
		tiers := userTierMenu(id, profile.Tier)
		rows = append(rows, tiers.InlineKeyboard[:2]...)
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("Refresh", fmt.Sprintf("admin:user:view:%d", id), "primary", "refresh"), premium.Button("Find Another", "admin:user:find", "primary", "search")},
		[]premium.InlineButton{premium.Button("Users", "admin:users", "primary", "people"), premium.Button("Admin Home", "menu:admin", "primary", "home")},
	)
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func userSearchMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Search Again", "admin:user:find", "primary", "search"), premium.Button("Users", "admin:users", "primary", "people")},
		{premium.Button("Admin Home", "menu:admin", "primary", "home")},
	}}
}

// handleUserAdminCallback serves admin:user:find/ban/banyes/balance/ledger.
// Permission gates have already run in handleStyledCallback.
func (a *App) handleUserAdminCallback(ctx context.Context, callback *tgbotapi.CallbackQuery, parts []string) bool {
	chatID, adminID := callback.Message.Chat.ID, callback.From.ID
	if len(parts) < 3 || parts[1] != "user" {
		return false
	}
	switch parts[2] {
	case "find":
		flow := store.TelegramFlow{Kind: "user_admin", Step: "find", Data: map[string]string{}, ExpiresAt: time.Now().Add(interactiveFlowLifetime)}
		if err := a.store.SetTelegramFlow(ctx, a.botInstanceID, adminID, flow); err != nil {
			a.sendError(chatID, err)
			return true
		}
		a.sendHTML(chatID, "🔎 <b>Find User</b>\n\nSend a Telegram user ID or @username.", flowCancelMenu("admin:users"))
		return true
	case "ban", "banyes", "balance", "ledger", "ledgerpage":
	default:
		return false
	}
	if !a.isMain {
		a.sendHTML(chatID, "Account bans and balances are managed in the main bot.", userBackMenu())
		return true
	}
	if len(parts) < 4 {
		return false
	}
	target, ok := parseCallbackInt(parts[3])
	if !ok {
		a.sendHTML(chatID, "Invalid user ID.", userSearchMenu())
		return true
	}
	switch parts[2] {
	case "ban":
		if len(parts) != 5 {
			return false
		}
		verb, note := "Ban", "They will no longer be able to use the bot. Their balance is kept."
		if parts[4] == "0" {
			verb, note = "Unban", "They will be able to use the bot again."
		}
		a.sendHTML(chatID, fmt.Sprintf("⚠️ <b>%s user %d?</b>\n\n%s", verb, target, note),
			confirmationMenu(fmt.Sprintf("admin:user:banyes:%d:%s", target, parts[4]), fmt.Sprintf("admin:user:view:%d", target)))
	case "banyes":
		if len(parts) != 5 {
			return false
		}
		banned := parts[4] == "1"
		if err := a.store.SetUserBanned(ctx, a.botInstanceID, adminID, target, banned, "inline admin action"); err != nil {
			a.sendError(chatID, err)
			return true
		}
		a.sendUserProfile(ctx, chatID, adminID, strconv.FormatInt(target, 10))
	case "balance":
		flow := store.TelegramFlow{Kind: "user_admin", Step: "balance", Data: map[string]string{"user_id": strconv.FormatInt(target, 10)}, ExpiresAt: time.Now().Add(interactiveFlowLifetime)}
		if err := a.store.SetTelegramFlow(ctx, a.botInstanceID, adminID, flow); err != nil {
			a.sendError(chatID, err)
			return true
		}
		a.sendHTML(chatID, fmt.Sprintf("💰 <b>Adjust Balance · User %d</b>\n\nSend <code>amount currency reason</code>.\n\nCredit: <code>50 PKR bonus</code>\nDebit: <code>-10 USD correction</code>\n\nThe user is notified and the change is recorded in their transactions.", target), flowCancelMenu(fmt.Sprintf("admin:user:view:%d", target)))
	case "ledger":
		a.sendLedger(ctx, chatID, target, 0, fmt.Sprintf("admin:user:view:%d", target))
	case "ledgerpage":
		offset := 0
		if len(parts) == 5 {
			offset, _ = strconv.Atoi(parts[4])
		}
		a.sendLedger(ctx, chatID, target, max(offset, 0), fmt.Sprintf("admin:user:view:%d", target))
	}
	return true
}

func (a *App) handleUserAdminText(ctx context.Context, message *tgbotapi.Message, flow store.TelegramFlow) bool {
	chatID, adminID := message.Chat.ID, message.From.ID
	text := strings.TrimSpace(message.Text)
	switch flow.Step {
	case "find":
		if allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, adminID, "manage_tiers"); !allowed {
			a.sendHTML(chatID, "🚫 You no longer have permission to manage users.", userBackMenu())
			return true
		}
		_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, adminID)
		a.sendUserProfile(ctx, chatID, adminID, text)
	case "balance":
		if allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, adminID, "manage_users"); !allowed || !a.isMain {
			a.sendHTML(chatID, "🚫 You no longer have permission to adjust balances.", userBackMenu())
			return true
		}
		target, _ := strconv.ParseInt(flow.Data["user_id"], 10, 64)
		a.applyBalanceAdjustment(ctx, chatID, adminID, target, text, func() {
			_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, adminID)
		})
	default:
		_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, adminID)
		a.sendHTML(chatID, "This form expired. Please start again.", userBackMenu())
	}
	return true
}

// applyBalanceAdjustment parses "amount currency reason" and applies it.
func (a *App) applyBalanceAdjustment(ctx context.Context, chatID, adminID, target int64, input string, onSuccess func()) {
	fields := strings.Fields(input)
	if len(fields) < 3 {
		a.sendHTML(chatID, "Use <code>amount currency reason</code>, for example <code>50 PKR bonus</code> or <code>-10 USD correction</code>.", flowCancelMenu(fmt.Sprintf("admin:user:view:%d", target)))
		return
	}
	amount, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", "."), 64)
	if err != nil {
		a.sendHTML(chatID, "The amount must be a number such as <code>50</code> or <code>-10</code>.", flowCancelMenu(fmt.Sprintf("admin:user:view:%d", target)))
		return
	}
	currency := strings.ToUpper(fields[1])
	reason := strings.Join(fields[2:], " ")
	balance, err := a.store.AdjustBalance(ctx, a.botInstanceID, adminID, target, currency, amount, reason)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	if onSuccess != nil {
		onSuccess()
	}
	verb := "credited to"
	if amount < 0 {
		verb = "debited from"
	}
	a.notifyUser(target, fmt.Sprintf("💰 <b>Balance updated</b>\n\n<b>%s %s</b> was %s your balance.\nReason: %s\nNew %s balance: <b>%s</b>",
		formatMoney(abs(amount), currency), currency, verb, html.EscapeString(reason), currency, formatMoney(balance, currency)))
	a.sendHTML(chatID, fmt.Sprintf("✅ <b>Balance adjusted</b>\n\nUser: <code>%d</code>\nChange: <b>%+.4f %s</b>\nNew balance: <b>%s %s</b>\nReason: %s",
		target, amount, currency, formatMoney(balance, currency), currency, html.EscapeString(reason)),
		premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
			{premium.Button("View User", fmt.Sprintf("admin:user:view:%d", target), "primary", "user"), premium.Button("Transactions", fmt.Sprintf("admin:user:ledger:%d", target), "primary", "history")},
			{premium.Button("Admin Home", "menu:admin", "primary", "home")},
		}})
}

// handleUserAdminCommand serves /user, /ban, /unban and /addbalance.
func (a *App) handleUserAdminCommand(ctx context.Context, message *tgbotapi.Message, command, args string) bool {
	chatID, adminID := message.Chat.ID, message.From.ID
	switch command {
	case "user":
		if args == "" {
			a.sendHTML(chatID, "Usage: <code>/user user_id</code> or <code>/user @username</code>", userSearchMenu())
			return true
		}
		a.sendUserProfile(ctx, chatID, adminID, args)
	case "ban", "unban":
		parts := splitExact(args, "|", 2)
		target, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			a.sendHTML(chatID, fmt.Sprintf("Usage: <code>/%s user_id|reason</code>", command), userSearchMenu())
			return true
		}
		reason := ""
		if len(parts) == 2 {
			reason = parts[1]
		}
		if err := a.store.SetUserBanned(ctx, a.botInstanceID, adminID, target, command == "ban", reason); err != nil {
			a.sendError(chatID, err)
			return true
		}
		a.sendUserProfile(ctx, chatID, adminID, strconv.FormatInt(target, 10))
	case "addbalance":
		parts := splitExact(args, "|", 3)
		target, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || len(parts) != 3 {
			a.sendHTML(chatID, "Usage: <code>/addbalance user_id|amount currency|reason</code>\nExample: <code>/addbalance 123456|50 PKR|bonus</code> (use a negative amount to debit)", userSearchMenu())
			return true
		}
		a.applyBalanceAdjustment(ctx, chatID, adminID, target, parts[1]+" "+parts[2], nil)
	default:
		return false
	}
	return true
}

// notifyUser sends a one-off message to a user's private chat without
// replacing the screen of the admin who triggered it.
func (a *App) notifyUser(userID int64, text string) {
	notice := *a
	notice.screenChatID, notice.screenMessageID, notice.group = 0, 0, nil
	notice.sendHTML(userID, text, profileMenu())
}

// ---- User transaction history ----

func (a *App) sendLedger(ctx context.Context, chatID, userID int64, offset int, back string) {
	entries, err := a.store.UserLedger(ctx, userID, ledgerPageSize+1, offset)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	more := len(entries) > ledgerPageSize
	if more {
		entries = entries[:ledgerPageSize]
	}
	var text strings.Builder
	text.WriteString("📒 <b>Transactions</b>\n")
	if back != "menu:profile" {
		fmt.Fprintf(&text, "User <code>%d</code>\n", userID)
	}
	if len(entries) == 0 && offset == 0 {
		text.WriteString("\nNo balance movements yet. Earnings appear here as soon as your first OTP is counted.")
	}
	for _, entry := range entries {
		sign := "➕"
		if entry.Amount < 0 {
			sign = "➖"
		}
		fmt.Fprintf(&text, "\n%s <b>%s %s</b> · %s\n<i>%s</i>", sign, formatMoney(abs(entry.Amount), entry.Currency), entry.Currency,
			ledgerLabel(entry.EntryType), entry.CreatedAt.In(a.location).Format("02 Jan 15:04"))
	}
	prefix := "menu:ledger:"
	if back != "menu:profile" {
		prefix = fmt.Sprintf("admin:user:ledgerpage:%d:", userID)
	}
	var rows [][]premium.InlineButton
	var pager []premium.InlineButton
	if offset > 0 {
		pager = append(pager, premium.Button("Previous", prefix+strconv.Itoa(max(0, offset-ledgerPageSize)), "primary", "back"))
	}
	if more {
		pager = append(pager, premium.Button("Next", prefix+strconv.Itoa(offset+ledgerPageSize), "primary", "history"))
	}
	if len(pager) > 0 {
		rows = append(rows, pager)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Back", back, "primary", "back"), premium.Button("Main Menu", "menu:home", "primary", "home")})
	a.sendHTML(chatID, text.String(), premium.InlineKeyboard{InlineKeyboard: rows})
}

func ledgerLabel(entryType string) string {
	switch entryType {
	case "otp_earning":
		return "OTP earning"
	case "milestone_reward":
		return "Daily milestone reward"
	case "referral_reward":
		return "Referral reward"
	case "withdrawal_hold":
		return "Withdrawal request"
	case "withdrawal_refund":
		return "Withdrawal refund"
	case "admin_adjustment":
		return "Admin adjustment"
	}
	return capitalize(strings.ReplaceAll(entryType, "_", " "))
}

func (a *App) sendMyWithdrawals(ctx context.Context, chatID, userID int64) {
	items, err := a.store.UserWithdrawals(ctx, a.botInstanceID, userID, 10)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("💸 <b>My Withdrawals</b>\n")
	if len(items) == 0 {
		text.WriteString("\nYou have not requested a withdrawal yet.")
	}
	for _, item := range items {
		amount := fmt.Sprintf("%.2f PKR", item.AmountPKR)
		if item.AmountUSD > 0 {
			amount = fmt.Sprintf("%.4f USD", item.AmountUSD)
		}
		fmt.Fprintf(&text, "\n%s <b>#%d · %s</b> · %s\n%s · %s", withdrawalStateIcon(item.State), item.ID, amount, capitalize(item.State),
			html.EscapeString(withdrawalMethodLabel(item.Method)), item.CreatedAt.In(a.location).Format("02 Jan 15:04"))
	}
	text.WriteString("\n\nPending requests are reviewed by the team; rejected amounts return to your balance.")
	a.sendHTML(chatID, text.String(), premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("New Withdrawal", "menu:withdraw", "success", "withdraw"), premium.Button("Transactions", "menu:ledger:0", "primary", "history")},
		{premium.Button("My Account", "menu:profile", "primary", "user"), premium.Button("Main Menu", "menu:home", "primary", "home")},
	}})
}

func withdrawalStateIcon(state string) string {
	switch state {
	case "approved", "paid":
		return "✅"
	case "rejected":
		return "↩️"
	}
	return "⏳"
}

func formatMoney(value float64, currency string) string {
	if currency == "USD" {
		return strconv.FormatFloat(value, 'f', 4, 64)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

// ---- Maintenance mode ----

// maintenanceBlocked tells regular users the bot is paused. Admins always pass.
func (a *App) maintenanceBlocked(ctx context.Context, chatID int64, admin bool) bool {
	if admin {
		return false
	}
	enabled, message, err := a.store.Maintenance(ctx, a.botInstanceID)
	if err != nil || !enabled {
		return false
	}
	if strings.TrimSpace(message) == "" {
		message = "We are upgrading the bot. Please try again in a little while."
	}
	a.sendHTML(chatID, "🛠 <b>Under maintenance</b>\n\n"+html.EscapeString(message), premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{
		{premium.Button("Try Again", "menu:home", "primary", "refresh")},
	}})
	return true
}

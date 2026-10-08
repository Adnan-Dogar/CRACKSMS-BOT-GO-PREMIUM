package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/panels"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
)

const interactiveFlowLifetime = 30 * time.Minute

var (
	customEmojiIDPattern = regexp.MustCompile(`^[0-9]{5,32}$`)
	bep20AddressPattern  = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
)

func (a *App) sendWithdrawalMenu(ctx context.Context, chatID, userID int64) {
	pkr, usd, _, err := a.store.UserBalance(ctx, userID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	accounts, err := a.store.ListWithdrawalAccounts(ctx, a.botInstanceID, userID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	text := fmt.Sprintf("💸 <b>Withdraw Funds</b>\n\nAvailable PKR: <b>%.2f</b>\nAvailable USDT: <b>%.4f</b>\n\n", pkr, usd)
	if len(accounts) == 0 {
		text += "Add a payout account first, then select it to submit a withdrawal."
	} else {
		text += "Select a saved account to continue. The balance is held only after final confirmation."
	}
	a.sendHTML(chatID, text, withdrawalMenu(accounts))
}

func (a *App) handleWithdrawalCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) bool {
	data := callback.Data
	if !strings.HasPrefix(data, "wd:") {
		return false
	}
	chatID, userID := callback.Message.Chat.ID, callback.From.ID
	parts := strings.Split(data, ":")
	switch {
	case data == "wd:add":
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, userID, store.TelegramFlow{
			Kind: "withdrawal_account", Step: "method", Data: map[string]string{}, ExpiresAt: time.Now().Add(interactiveFlowLifetime),
		})
		a.sendHTML(chatID, "💳 <b>Add Withdrawal Account</b>\n\nChoose where you want to receive payments.", withdrawalMethodMenu())
	case len(parts) == 3 && parts[1] == "addmethod" && store.ValidWithdrawalMethod(parts[2]):
		method := parts[2]
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, userID, store.TelegramFlow{
			Kind: "withdrawal_account", Step: "details", Data: map[string]string{"method": method}, ExpiresAt: time.Now().Add(interactiveFlowLifetime),
		})
		a.sendHTML(chatID, withdrawalAccountPrompt(method), flowCancelMenu("menu:withdraw"))
	case len(parts) == 3 && parts[1] == "use":
		accountID, ok := parseCallbackInt(parts[2])
		if !ok {
			a.sendHTML(chatID, "Invalid payout account.", userBackMenu())
			break
		}
		account, err := a.store.WithdrawalAccount(ctx, a.botInstanceID, userID, accountID)
		if err != nil {
			a.sendError(chatID, err)
			break
		}
		pkr, usd, _, err := a.store.UserBalance(ctx, userID)
		if err != nil {
			a.sendError(chatID, err)
			break
		}
		currency, available := withdrawalCurrency(account.Method), pkr
		if currency == "USDT" {
			available = usd
		}
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, userID, store.TelegramFlow{
			Kind: "withdrawal", Step: "amount", Data: map[string]string{"account_id": strconv.FormatInt(accountID, 10)}, ExpiresAt: time.Now().Add(interactiveFlowLifetime),
		})
		a.sendHTML(chatID, fmt.Sprintf("💸 <b>%s Withdrawal</b>\n\nAccount: <code>%s</code>\nAvailable: <b>%.4f %s</b>\n\nSend the amount you want to withdraw.",
			html.EscapeString(withdrawalMethodLabel(account.Method)), html.EscapeString(account.DisplayHint), available, currency), flowCancelMenu("menu:withdraw"))
	case len(parts) == 3 && parts[1] == "delete":
		_, ok := parseCallbackInt(parts[2])
		if ok {
			a.sendHTML(chatID, "⚠️ <b>Delete this payout account?</b>", confirmationMenu("wd:deleteyes:"+parts[2], "menu:withdraw"))
		}
	case len(parts) == 3 && parts[1] == "deleteyes":
		accountID, ok := parseCallbackInt(parts[2])
		if !ok {
			a.sendHTML(chatID, "Invalid payout account.", userBackMenu())
			break
		}
		if err := a.store.RemoveWithdrawalAccount(ctx, a.botInstanceID, userID, accountID); err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendWithdrawalMenu(ctx, chatID, userID)
		}
	case data == "wd:confirm":
		a.confirmWithdrawal(ctx, callback)
	default:
		a.sendHTML(chatID, "This withdrawal action is no longer available.", userBackMenu())
	}
	return true
}

func (a *App) confirmWithdrawal(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	chatID, userID := callback.Message.Chat.ID, callback.From.ID
	flow, err := a.store.TelegramFlow(ctx, a.botInstanceID, userID)
	if err != nil || flow.Kind != "withdrawal" || flow.Step != "confirm" {
		a.sendHTML(chatID, "⏳ This withdrawal session expired. Please start again.", userBackMenu())
		return
	}
	accountID, err1 := strconv.ParseInt(flow.Data["account_id"], 10, 64)
	amount, err2 := strconv.ParseFloat(flow.Data["amount"], 64)
	account, err3 := a.store.WithdrawalAccount(ctx, a.botInstanceID, userID, accountID)
	if err1 != nil || err2 != nil || err3 != nil || amount <= 0 {
		a.sendHTML(chatID, "The payout account or amount is no longer valid.", userBackMenu())
		return
	}
	amountPKR, amountUSD := amount, 0.0
	if withdrawalCurrency(account.Method) == "USDT" {
		amountPKR, amountUSD = 0, amount
	}
	claimed, claimErr := a.store.ClaimTelegramFlow(ctx, a.botInstanceID, userID, flow)
	if claimErr != nil {
		a.sendError(chatID, claimErr)
		return
	}
	if !claimed {
		a.sendHTML(chatID, "This request has already been handled. Check your balance and history.", profileMenu())
		return
	}
	id, err := a.store.CreateWithdrawalForAccount(ctx, a.botInstanceID, userID, accountID, amountPKR, amountUSD)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, userID)
	a.sendHTML(chatID, fmt.Sprintf("✅ <b>Withdrawal #%d submitted</b>\n\nAmount: <b>%.4f %s</b>\nMethod: <b>%s</b>\nAccount: <code>%s</code>\n\nThe owner has been notified and will review it.",
		id, amount, withdrawalCurrency(account.Method), html.EscapeString(withdrawalMethodLabel(account.Method)), html.EscapeString(account.DisplayHint)), profileMenu())
	a.notifyWithdrawalReviewers(ctx, callback.From, id, account, amountPKR, amountUSD)
}

func (a *App) notifyWithdrawalReviewers(ctx context.Context, user *tgbotapi.User, id int64, account store.WithdrawalAccount, amountPKR, amountUSD float64) {
	admins, err := a.store.ListInstanceAdmins(ctx, a.botInstanceID)
	if err != nil {
		return
	}
	amount := fmt.Sprintf("%.2f PKR", amountPKR)
	if amountUSD > 0 {
		amount = fmt.Sprintf("%.4f USDT", amountUSD)
	}
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		name = strconv.FormatInt(user.ID, 10)
	}
	username := "not set"
	if user.UserName != "" {
		username = "@" + user.UserName
	}
	text := fmt.Sprintf("💸 <b>New Withdrawal Request #%d</b>\n\nUser: <b>%s</b> (%s)\nUser ID: <code>%d</code>\nAmount: <b>%s</b>\nMethod: <b>%s</b>\nDetails: <code>%s</code>",
		id, html.EscapeString(name), html.EscapeString(username), user.ID, amount,
		html.EscapeString(withdrawalMethodLabel(account.Method)), html.EscapeString(account.Details))
	seen := map[int64]bool{}
	for _, admin := range admins {
		if seen[admin.UserID] || !hasPermission(admin.Permissions, "manage_withdrawals") {
			continue
		}
		seen[admin.UserID] = true
		a.sendHTML(admin.UserID, text, withdrawalActionsMenu(id))
	}
}

func hasPermission(permissions []string, wanted string) bool {
	for _, permission := range permissions {
		if permission == "*" || permission == wanted {
			return true
		}
	}
	return false
}

func (a *App) handleAdminInteractiveCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) bool {
	data := callback.Data
	switch {
	case strings.HasPrefix(data, "admin:upload:"):
		a.handleNumberImportCallback(ctx, callback)
		return true
	case strings.HasPrefix(data, "admin:panel:kind:"):
		a.handlePanelKindCallback(ctx, callback)
		return true
	case strings.HasPrefix(data, "admin:panel:test:"):
		id, ok := parseCallbackInt(strings.TrimPrefix(data, "admin:panel:test:"))
		if !ok {
			a.sendHTML(callback.Message.Chat.ID, "Invalid panel ID.", adminPanelMenu(nil))
		} else {
			a.testPanelNow(ctx, callback.Message.Chat.ID, id)
		}
		return true
	}
	return false
}

func (a *App) startNumberImport(ctx context.Context, chatID, userID int64) {
	flow := store.TelegramFlow{Kind: "number_import", Step: "file", Data: map[string]string{"nonce": broadcastKey()}, ExpiresAt: time.Now().Add(interactiveFlowLifetime)}
	a.importScreen(ctx, chatID, userID, flow, "📤 <b>Upload Numbers — Step 1</b>\n\nSend a UTF-8 <code>.txt</code> or <code>.csv</code> file containing one number per line, or a CSV with a phone column. No long caption is required.", flowCancelMenu("admin:numbers"))
}

func (a *App) handleInteractiveDocument(ctx context.Context, message *tgbotapi.Message, admin bool) bool {
	flow, err := a.store.TelegramFlow(ctx, a.botInstanceID, message.From.ID)
	if err != nil || flow.Kind != "number_import" || flow.Step != "file" {
		return false
	}
	if !admin {
		a.sendHTML(message.Chat.ID, "🔒 Only authorized admins can import numbers.", userBackMenu())
		return true
	}
	allowed, err := a.store.HasAdminPermission(ctx, a.botInstanceID, message.From.ID, "manage_settings")
	if err != nil || !allowed {
		a.sendHTML(message.Chat.ID, "🔒 Inventory permission is required.", userBackMenu())
		return true
	}
	name := strings.ToLower(strings.TrimSpace(message.Document.FileName))
	if !strings.HasSuffix(name, ".txt") && !strings.HasSuffix(name, ".csv") {
		a.sendHTML(message.Chat.ID, "Please upload a <code>.txt</code> or <code>.csv</code> file.", flowCancelMenu("admin:numbers"))
		return true
	}
	if message.Document.FileSize > 10<<20 {
		a.sendHTML(message.Chat.ID, "The import file must be 10 MB or smaller.", flowCancelMenu("admin:numbers"))
		return true
	}
	a.captureNumberFile(ctx, message, flow)
	return true
}

func (a *App) handleNumberImportCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	chatID, userID := callback.Message.Chat.ID, callback.From.ID
	flow, err := a.store.TelegramFlow(ctx, a.botInstanceID, userID)
	if err != nil || flow.Kind != "number_import" {
		if route, nonce, found := strings.Cut(callback.Data, "~"); found && route == "admin:upload:confirm" {
			if job, e := a.store.ImportByNonce(ctx, a.botInstanceID, userID, nonce); e == nil && job.State != "draft" {
				job.MessageID = callback.Message.MessageID
				_ = a.showImportJob(ctx, chatID, userID, job)
				return
			}
		}
		a.sendHTML(chatID, "⏳ This upload session expired. Start Upload Numbers again.", adminNumbersMenu())
		return
	}
	if flow.Data["nonce"] != "" {
		route, nonce, found := strings.Cut(callback.Data, "~")
		if !found || nonce != flow.Data["nonce"] {
			a.sendHTML(chatID, "⏳ This selection belongs to an older upload. Open the current import.", adminNumbersMenu())
			return
		}
		expected, _ := strconv.Atoi(flow.Data["screen_id"])
		if expected != 0 && expected != callback.Message.MessageID {
			return
		}
		copy := *callback
		copy.Data = route
		callback = &copy
	}
	a.screenChatID = chatID
	a.screenMessageID = callback.Message.MessageID
	if callback.Data == "admin:upload:cancel" {
		_ = a.clearNavigationFlow(ctx, userID)
		a.sendHTML(chatID, "❌ Number import cancelled. No inventory was changed.", adminNumbersMenu())
		return
	}
	if callback.Data == "admin:upload:apps" && (flow.Step == "custom_service_name" || flow.Step == "custom_service_emoji") {
		flow.Step = "multi_service"
		a.showMultiImport(ctx, chatID, userID, flow, "")
		return
	}
	if callback.Data == "admin:upload:other" && flow.Step == "multi_service" {
		if len(importServices(flow)) >= 20 {
			a.showMultiImport(ctx, chatID, userID, flow, "Select at most 20 apps. Deselect an app to add another.")
			return
		}
		flow.Step = "custom_service_name"
		a.importScreen(ctx, chatID, userID, flow, "✏️ <b>Add App</b>\n\nSend the app name (maximum 64 characters).", importAppFormMenu(flow))
		return
	}
	if a.handleMultiImport(ctx, callback, flow) {
		return
	}
	data := callback.Data
	switch {
	case strings.HasPrefix(data, "admin:upload:service:") && flow.Step == "service":
		choice := strings.TrimPrefix(data, "admin:upload:service:")
		if choice == "other" {
			flow.Step = "custom_service_name"
			flow.ExpiresAt = time.Now().Add(interactiveFlowLifetime)
			_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, userID, flow)
			a.sendHTML(chatID, "✏️ <b>Add App</b>\n\nSend the app/service name (maximum 64 characters).", flowCancelMenu("admin:numbers"))
			return
		}
		index, err := strconv.Atoi(choice)
		apps := premium.DefaultApps()
		if err != nil || index < 0 || index >= len(apps) {
			a.sendHTML(chatID, "Invalid service selection.", uploadServicesMenu())
			return
		}
		flow.Data["service"] = apps[index].Name
		flow.Data["emoji_id"] = apps[index].CustomEmojiID
		a.promptImportPricing(ctx, chatID, userID, flow)
	case data == "admin:upload:pricing:default" && flow.Step == "pricing":
		flow.Data["price_pkr"], flow.Data["price_usd"], flow.Data["per_cycle"] = "1", "0", "3"
		a.confirmNumberImport(ctx, chatID, userID, flow)
	case data == "admin:upload:confirm" && flow.Step == "confirm":
		a.finishNumberImport(ctx, chatID, userID, flow)
	default:
		a.sendHTML(chatID, "This import action is not valid for the current step.", flowCancelMenu("admin:numbers"))
	}
}

func (a *App) downloadTelegramFile(ctx context.Context, fileID string) ([]byte, error) {
	fileURL, err := a.bot.GetFileDirectURL(fileID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, err
	}
	client := a.fileClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Telegram file download returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (10<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 10<<20 {
		return nil, errors.New("import file exceeds 10 MB")
	}
	return body, nil
}

func (a *App) startPanelWizard(ctx context.Context, chatID, userID int64) {
	if err := a.store.SetTelegramFlow(ctx, a.botInstanceID, userID, store.TelegramFlow{
		Kind: "panel_add", Step: "name", Data: map[string]string{}, ExpiresAt: time.Now().Add(interactiveFlowLifetime),
	}); err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendHTML(chatID, "➕ <b>Add Panel — Step 1</b>\n\nSend a unique panel name.", flowCancelMenu("admin:panels"))
}

func (a *App) handlePanelKindCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	chatID, userID := callback.Message.Chat.ID, callback.From.ID
	flow, err := a.store.TelegramFlow(ctx, a.botInstanceID, userID)
	if err != nil || flow.Kind != "panel_add" || flow.Step != "kind" {
		a.sendHTML(chatID, "⏳ This panel session expired. Start Add Panel again.", adminPanelMenu(nil))
		return
	}
	choice := strings.TrimPrefix(callback.Data, "admin:panel:kind:")
	kind := map[string]string{"login": "login", "token": "token_api", "legacy": "legacy_api", "ws": "websocket"}[choice]
	if kind == "" {
		a.sendHTML(chatID, "Invalid panel type.", panelKindMenu())
		return
	}
	flow.Data["kind"] = kind
	if kind == "token_api" {
		flow.Data["api_type"] = "crapi"
	} else if kind == "legacy_api" {
		flow.Data["api_type"] = "reseller"
	}
	flow.Step = "url"
	flow.ExpiresAt = time.Now().Add(interactiveFlowLifetime)
	_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, userID, flow)
	prompt := "Send the HTTPS SMS API endpoint URL."
	if kind == "login" {
		prompt = "Send the panel base URL, for example <code>https://panel.example</code>."
	} else if kind == "websocket" {
		prompt = "Send the plain WebSocket URI beginning with <code>wss://</code> or <code>ws://</code>."
	}
	a.sendHTML(chatID, "🔗 <b>Add Panel — Connection</b>\n\n"+prompt, flowCancelMenu("admin:panels"))
}

func (a *App) testPanelNow(ctx context.Context, chatID, panelID int64) {
	panel, err := a.store.PanelForInstance(ctx, a.botInstanceID, panelID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	adapter, err := panels.NewAdapterWithGate(panel, a.store)
	if err == nil {
		testCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = adapter.Test(testCtx)
		cancel()
		_ = adapter.Close()
	}
	_ = a.store.UpdatePanelHealth(ctx, panel.ID, panel.LastCursor, err)
	if err != nil {
		var provider *panels.ProviderError
		if errors.As(err, &provider) && provider.Status == 429 && provider.RetryAt.After(time.Now()) {
			_ = a.store.SchedulePanelTest(ctx, a.botInstanceID, panel.ID, provider.RetryAt, false)
		}
		a.sendHTML(chatID, "❌ <b>Panel test pending / failed</b>\n\n"+html.EscapeString(panels.SafeError(err)), panelActionsMenu(store.PanelHealth{ID: panel.ID, Name: panel.Name, Kind: panel.Kind, Enabled: panel.Enabled}))
		return
	}
	a.sendHTML(chatID, "✅ <b>Panel connection successful</b>", panelActionsMenu(store.PanelHealth{ID: panel.ID, Name: panel.Name, Kind: panel.Kind, Enabled: panel.Enabled, Healthy: true}))
}

func (a *App) handleFlowText(ctx context.Context, message *tgbotapi.Message, admin bool) bool {
	flow, err := a.store.TelegramFlow(ctx, a.botInstanceID, message.From.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return true
	}
	text := strings.TrimSpace(message.Text)
	if strings.EqualFold(text, "/cancel") {
		_ = a.clearNavigationFlow(ctx, message.From.ID)
		a.sendHTML(message.Chat.ID, "❌ Interactive action cancelled.", compactMenu(admin, a.isMain))
		return true
	}
	if message.IsCommand() && text != "/default" && text != "/skip" && !(flow.Kind == "number_import" && flow.Step == "import_search" && text == "/all") {
		return false
	}
	switch flow.Kind {
	case "provider_mapping", "provider_import", "provider_range", "provider_numbers":
		return a.handleProviderText(ctx, message, flow)
	case "ui_setting":
		return a.handleSettingText(ctx, message, flow)
	case "panel_source":
		allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, message.From.ID, "manage_panels")
		if !allowed {
			a.sendHTML(message.Chat.ID, "You no longer have panel permission.", userBackMenu())
			return true
		}
		return a.handleSourceText(ctx, message, flow)
	case "panel_account":
		allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, message.From.ID, "manage_panels")
		if !allowed {
			a.sendHTML(message.Chat.ID, "You no longer have panel permission.", userBackMenu())
			return true
		}
		return a.handleAccountText(ctx, message, flow)
	case "guided":
		return a.acceptGuidedInput(ctx, message, flow, text)
	case "withdrawal_account":
		return a.handleWithdrawalAccountText(ctx, message, flow)
	case "withdrawal":
		return a.handleWithdrawalAmountText(ctx, message, flow)
	case "number_import":
		if !admin {
			return false
		}
		allowed, permissionErr := a.store.HasAdminPermission(ctx, a.botInstanceID, message.From.ID, "manage_settings")
		if permissionErr != nil || !allowed {
			a.sendHTML(message.Chat.ID, "🚫 You no longer have permission to import numbers.", nil)
			return true
		}
		return a.handleNumberImportText(ctx, message, flow)
	case "panel_add":
		if !admin {
			return false
		}
		allowed, permissionErr := a.store.HasAdminPermission(ctx, a.botInstanceID, message.From.ID, "manage_panels")
		if permissionErr != nil || !allowed {
			a.sendHTML(message.Chat.ID, "🚫 You no longer have permission to add panels.", nil)
			return true
		}
		return a.handlePanelText(ctx, message, flow)
	}
	return false
}

func (a *App) handleWithdrawalAccountText(ctx context.Context, message *tgbotapi.Message, flow store.TelegramFlow) bool {
	if flow.Step != "details" {
		return true
	}
	method := flow.Data["method"]
	details, displayHint, err := validateWithdrawalDetails(method, strings.TrimSpace(message.Text))
	if err != nil {
		a.sendHTML(message.Chat.ID, "❌ "+html.EscapeString(err.Error())+"\n\n"+withdrawalAccountPrompt(method), flowCancelMenu("menu:withdraw"))
		return true
	}
	if _, err := a.store.AddWithdrawalAccount(ctx, a.botInstanceID, message.From.ID, method, displayHint, details); err != nil {
		a.sendError(message.Chat.ID, err)
		return true
	}
	_, _ = a.bot.Request(tgbotapi.NewDeleteMessage(message.Chat.ID, message.MessageID))
	_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, message.From.ID)
	a.sendHTML(message.Chat.ID, "✅ Withdrawal account saved with encrypted details.", nil)
	a.sendWithdrawalMenu(ctx, message.Chat.ID, message.From.ID)
	return true
}

func (a *App) handleWithdrawalAmountText(ctx context.Context, message *tgbotapi.Message, flow store.TelegramFlow) bool {
	if flow.Step != "amount" {
		return true
	}
	amount, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(message.Text), ",", "."), 64)
	accountID, accountErr := strconv.ParseInt(flow.Data["account_id"], 10, 64)
	account, lookupErr := a.store.WithdrawalAccount(ctx, a.botInstanceID, message.From.ID, accountID)
	if err != nil || accountErr != nil || lookupErr != nil || amount <= 0 {
		a.sendHTML(message.Chat.ID, "Send a valid positive withdrawal amount.", flowCancelMenu("menu:withdraw"))
		return true
	}
	pkr, usd, _, err := a.store.UserBalance(ctx, message.From.ID)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return true
	}
	available := pkr
	if withdrawalCurrency(account.Method) == "USDT" {
		available = usd
	}
	if amount > available {
		a.sendHTML(message.Chat.ID, fmt.Sprintf("Insufficient balance. Available: <b>%.4f %s</b>.", available, withdrawalCurrency(account.Method)), flowCancelMenu("menu:withdraw"))
		return true
	}
	flow.Step = "confirm"
	flow.Data["amount"] = strconv.FormatFloat(amount, 'f', -1, 64)
	flow.ExpiresAt = time.Now().Add(interactiveFlowLifetime)
	if err := a.store.SetTelegramFlow(ctx, a.botInstanceID, message.From.ID, flow); err != nil {
		a.sendError(message.Chat.ID, err)
		return true
	}
	a.sendHTML(message.Chat.ID, fmt.Sprintf("📋 <b>Confirm Withdrawal</b>\n\nMethod: <b>%s</b>\nAccount: <code>%s</code>\nAmount: <b>%.4f %s</b>\n\nYour balance will be held immediately after confirmation and refunded automatically if rejected.",
		html.EscapeString(withdrawalMethodLabel(account.Method)), html.EscapeString(account.DisplayHint), amount, withdrawalCurrency(account.Method)), withdrawalConfirmMenu())
	return true
}

func (a *App) handleNumberImportText(ctx context.Context, message *tgbotapi.Message, flow store.TelegramFlow) bool {
	switch flow.Step {
	case "import_search":
		query := strings.TrimSpace(message.Text)
		if query == "/all" {
			query = ""
		}
		if len(query) > 64 {
			a.sendHTML(message.Chat.ID, "Search must be 64 characters or fewer.", flowCancelMenu("admin:numbers"))
			return true
		}
		flow.Data["search_query"] = query
		flow.Data["page"] = "0"
		flow.Step = "multi_service"
		a.showMultiImport(ctx, message.Chat.ID, message.From.ID, flow, "")
		return true
	case "custom_service_name":
		name := strings.TrimSpace(message.Text)
		if name == "" || len(name) > 64 {
			a.sendHTML(message.Chat.ID, "App name must be 1–64 characters.", flowCancelMenu("admin:numbers"))
			return true
		}
		choices, _ := a.importChoices(ctx, store.TelegramFlow{Data: map[string]string{"custom_apps": flow.Data["custom_apps"]}})
		for _, existing := range choices {
			if strings.EqualFold(name, existing) {
				flow.Step = "multi_service"
				a.showMultiImport(ctx, message.Chat.ID, message.From.ID, flow, "That app already exists. Select it from the list.")
				return true
			}
		}
		flow.Data["service"] = name
		flow.Step = "custom_service_emoji"
		a.importScreen(ctx, message.Chat.ID, message.From.ID, flow, "✨ <b>Custom App Emoji</b>\n\nSend the numeric Telegram custom emoji ID. It will be used in service buttons and OTP messages.", importAppFormMenu(flow))
		return true
	case "custom_service_emoji":
		emojiID := strings.TrimSpace(message.Text)
		if !customEmojiIDPattern.MatchString(emojiID) {
			a.sendHTML(message.Chat.ID, "Custom emoji ID must contain only 5–32 digits.", flowCancelMenu("admin:numbers"))
			return true
		}
		flow.Data["emoji_id"] = emojiID
		apps := importCustomApps(flow)
		apps[flow.Data["service"]] = emojiID
		raw, _ := json.Marshal(apps)
		flow.Data["custom_apps"] = string(raw)
		if _, ok := flow.Data["services"]; ok {
			services := importServices(flow)
			if len(services) >= 20 {
				a.sendHTML(message.Chat.ID, "Select at most 20 apps.", adminNumbersMenu())
				return true
			}
			services = append(services, flow.Data["service"])
			flow.Data["services"] = importServiceJSON(services)
			flow.Step = "multi_service"
			flow.Data["search_query"] = ""
			flow.Data["page"] = "0"
			a.showMultiImport(ctx, message.Chat.ID, message.From.ID, flow, "App added and selected. Add more apps or choose Continue.")
			return true
		}
		a.promptImportPricing(ctx, message.Chat.ID, message.From.ID, flow)
		return true
	case "pricing":
		if strings.EqualFold(strings.TrimSpace(message.Text), "/default") {
			flow.Data["price_pkr"], flow.Data["price_usd"], flow.Data["per_cycle"] = "1", "0", "3"
			a.confirmNumberImport(ctx, message.Chat.ID, message.From.ID, flow)
			return true
		}
		parts := splitExact(message.Text, "|", 3)
		if len(parts) != 3 {
			a.sendHTML(message.Chat.ID, "Use <code>PricePKR|PriceUSD|NumbersPerCycle</code>, for example <code>1|0|3</code>.", uploadPricingMenu())
			return true
		}
		pkr, err1 := strconv.ParseFloat(parts[0], 64)
		usd, err2 := strconv.ParseFloat(parts[1], 64)
		perCycle, err3 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || err3 != nil || pkr < 0 || usd < 0 || perCycle <= 0 || perCycle > 1000 {
			a.sendHTML(message.Chat.ID, "Prices must be non-negative and numbers-per-cycle must be 1–1000.", uploadPricingMenu())
			return true
		}
		flow.Data["price_pkr"] = strconv.FormatFloat(pkr, 'f', -1, 64)
		flow.Data["price_usd"] = strconv.FormatFloat(usd, 'f', -1, 64)
		flow.Data["per_cycle"] = strconv.Itoa(perCycle)
		a.confirmNumberImport(ctx, message.Chat.ID, message.From.ID, flow)
		return true
	}
	return true
}

func (a *App) handlePanelText(ctx context.Context, message *tgbotapi.Message, flow store.TelegramFlow) bool {
	text := strings.TrimSpace(message.Text)
	switch flow.Step {
	case "name":
		if text == "" || len(text) > 80 {
			a.sendHTML(message.Chat.ID, "Panel name must be 1–80 characters.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["name"] = text
		flow.Step = "kind"
		flow.ExpiresAt = time.Now().Add(interactiveFlowLifetime)
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, message.From.ID, flow)
		a.sendHTML(message.Chat.ID, "🔌 <b>Add Panel — Step 2</b>\n\nChoose the panel connection type.", panelKindMenu())
	case "url":
		kind := flow.Data["kind"]
		if !validPanelURL(kind, text) {
			a.sendHTML(message.Chat.ID, "Send a valid URL for the selected panel type.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["url"] = text
		if kind == "login" {
			flow.Step = "username"
			a.sendHTML(message.Chat.ID, "👤 Send the panel username.", flowCancelMenu("admin:panels"))
		} else {
			flow.Step = "token"
			prompt := "🔐 Send the API token. The message will be deleted after capture."
			if kind == "websocket" {
				prompt = "🔐 Send the WebSocket token, or send <code>/skip</code> if the URI already contains authentication."
			}
			a.sendHTML(message.Chat.ID, prompt, flowCancelMenu("admin:panels"))
		}
		flow.ExpiresAt = time.Now().Add(interactiveFlowLifetime)
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, message.From.ID, flow)
	case "username":
		if text == "" || len(text) > 200 {
			a.sendHTML(message.Chat.ID, "Send a valid panel username.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["username"] = text
		flow.Step = "password"
		flow.ExpiresAt = time.Now().Add(interactiveFlowLifetime)
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, message.From.ID, flow)
		a.sendHTML(message.Chat.ID, "🔐 Send the panel password. The message will be deleted after capture.", flowCancelMenu("admin:panels"))
	case "password":
		if text == "" {
			a.sendHTML(message.Chat.ID, "Panel password cannot be empty.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["password"] = text
		_, _ = a.bot.Request(tgbotapi.NewDeleteMessage(message.Chat.ID, message.MessageID))
		a.finishPanelWizard(ctx, message.Chat.ID, message.From.ID, flow)
	case "token":
		if text == "/skip" && flow.Data["kind"] == "websocket" {
			text = ""
		} else if text == "" || text == "/skip" {
			a.sendHTML(message.Chat.ID, "API token cannot be empty.", flowCancelMenu("admin:panels"))
			return true
		}
		flow.Data["token"] = text
		_, _ = a.bot.Request(tgbotapi.NewDeleteMessage(message.Chat.ID, message.MessageID))
		a.finishPanelWizard(ctx, message.Chat.ID, message.From.ID, flow)
	}
	return true
}

func (a *App) finishPanelWizard(ctx context.Context, chatID, userID int64, flow store.TelegramFlow) {
	tier, err := a.store.BotInstanceTier(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	count, err := a.store.CountPanelsForInstance(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	if count >= store.TierPanelLimit(tier) {
		_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, userID)
		a.sendHTML(chatID, fmt.Sprintf("Panel limit reached for the <b>%s</b> tier (%d).", tier, store.TierPanelLimit(tier)), adminPanelMenu(nil))
		return
	}
	config := map[string]any{}
	switch flow.Data["kind"] {
	case "login":
		config = map[string]any{"base_url": flow.Data["url"], "username": flow.Data["username"], "password": flow.Data["password"]}
	case "token_api", "legacy_api":
		config = map[string]any{"url": flow.Data["url"], "token": flow.Data["token"], "api_type": flow.Data["api_type"]}
	case "websocket":
		config = map[string]any{"url": flow.Data["url"], "token": flow.Data["token"]}
	default:
		a.sendHTML(chatID, "Invalid panel type.", adminPanelMenu(nil))
		return
	}
	panel := domain.Panel{BotInstanceID: a.botInstanceID, Name: flow.Data["name"], Kind: flow.Data["kind"], Config: config, PollInterval: 2 * time.Second, Enabled: true}
	panel.Enabled = false
	_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, userID)
	id, err := a.store.UpsertPanelForInstance(ctx, a.botInstanceID, panel)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	if err = a.store.SchedulePanelTest(ctx, a.botInstanceID, id, time.Now(), true); err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendHTML(chatID, fmt.Sprintf("✅ Panel <b>#%d %s</b> saved securely. Connection test queued.", id, html.EscapeString(panel.Name)), adminPanelMenu(nil))
}

func validPanelURL(kind, value string) bool {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Host == "" {
		return false
	}
	if kind == "websocket" {
		return u.Scheme == "ws" || u.Scheme == "wss"
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func withdrawalAccountPrompt(method string) string {
	switch method {
	case "jazzcash":
		return "💳 <b>JazzCash Details</b>\n\nSend <code>mobile number | account title</code>. Example: <code>03001234567 | Adnan</code>."
	case "easypaisa":
		return "💳 <b>Easypaisa Details</b>\n\nSend <code>mobile number | account title</code>. Example: <code>03001234567 | Adnan</code>."
	case "binance":
		return "💎 <b>Binance Details</b>\n\nSend your Binance Pay ID, UID, or registered email."
	case "usdt_bep20":
		return "💎 <b>USDT BEP20 Details</b>\n\nSend your BEP20 wallet address beginning with <code>0x</code>. Verify the network carefully."
	default:
		return "Send the payout account details."
	}
}

func validateWithdrawalDetails(method, value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 500 {
		return "", "", errors.New("payment details are required")
	}
	switch method {
	case "jazzcash", "easypaisa":
		parts := splitExact(value, "|", 2)
		digits := onlyDigits(parts[0])
		if len(digits) < 10 || len(digits) > 15 {
			return "", "", errors.New("send a valid mobile number, optionally followed by | account title")
		}
		return value, maskValue(digits, 4, 3), nil
	case "binance":
		if len(value) < 4 || len(value) > 120 {
			return "", "", errors.New("send a valid Binance Pay ID, UID, or email")
		}
		return value, maskValue(value, 3, 3), nil
	case "usdt_bep20":
		if !bep20AddressPattern.MatchString(value) {
			return "", "", errors.New("BEP20 address must be 0x followed by 40 hexadecimal characters")
		}
		return value, maskValue(value, 6, 4), nil
	default:
		return "", "", errors.New("unsupported withdrawal method")
	}
}

func withdrawalCurrency(method string) string {
	if method == "binance" || method == "usdt_bep20" {
		return "USDT"
	}
	return "PKR"
}

func onlyDigits(value string) string {
	var out strings.Builder
	for _, char := range value {
		if char >= '0' && char <= '9' {
			out.WriteRune(char)
		}
	}
	return out.String()
}

func maskValue(value string, prefix, suffix int) string {
	runes := []rune(value)
	if len(runes) <= prefix+suffix {
		return strings.Repeat("•", len(runes))
	}
	return string(runes[:prefix]) + "•••" + string(runes[len(runes)-suffix:])
}

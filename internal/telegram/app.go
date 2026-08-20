package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/panels"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
)

type App struct {
	bot           *tgbotapi.BotAPI
	store         *store.Store
	botInstanceID int64
	isMain        bool
	holdDuration  time.Duration
	location      *time.Location
	workerCount   int
	rateMu        sync.Mutex
	rateWindow    map[int64][]time.Time
	links         themes.Links
}

func (a *App) SetLinks(links themes.Links) { a.links = links }

func New(bot *tgbotapi.BotAPI, repo *store.Store, holdDuration time.Duration, location *time.Location) *App {
	return NewForInstance(bot, repo, store.MainBotInstanceID, true, holdDuration, location)
}

func NewForInstance(bot *tgbotapi.BotAPI, repo *store.Store, botInstanceID int64, isMain bool, holdDuration time.Duration, location *time.Location) *App {
	return &App{bot: bot, store: repo, botInstanceID: botInstanceID, isMain: isMain, holdDuration: holdDuration,
		location: location, workerCount: 32, rateWindow: map[int64][]time.Time{}}
}

func (a *App) Run(ctx context.Context) error {
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 30
	updates := a.bot.GetUpdatesChan(updateConfig)
	queue := make(chan tgbotapi.Update, 1000)
	var workers sync.WaitGroup
	for i := 0; i < a.workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for update := range queue {
				a.handleUpdate(ctx, update)
			}
		}()
	}
	defer func() { close(queue); workers.Wait() }()
	for {
		select {
		case <-ctx.Done():
			a.bot.StopReceivingUpdates()
			return nil
		case update, ok := <-updates:
			if !ok {
				return errors.New("Telegram updates channel closed")
			}
			select {
			case queue <- update:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (a *App) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("Telegram handler panic", "panic", recovered)
		}
	}()
	if update.CallbackQuery != nil {
		a.handleCallback(ctx, update.CallbackQuery)
		return
	}
	message := update.Message
	if message == nil || message.From == nil || message.Chat == nil || !message.Chat.IsPrivate() {
		return
	}
	if err := a.store.EnsureUserForInstance(ctx, a.botInstanceID, message.From.ID, message.From.UserName, message.From.FirstName, message.From.LastName); err != nil {
		slog.Error("ensure user", "error", err)
		return
	}
	admin, _ := a.store.HasAnyAdminRole(ctx, a.botInstanceID, message.From.ID)
	if !admin && a.rateLimited(message.From.ID) {
		a.sendHTML(message.Chat.ID, "⏳ Too many requests. Please slow down for a moment.", nil)
		return
	}
	if !admin && message.Command() != "start" {
		if missing, err := a.missingRequiredChats(ctx, message.From.ID); err == nil && len(missing) > 0 {
			a.sendJoinRequired(message.Chat.ID, missing)
			return
		}
	}
	if message.Document != nil {
		a.handleDocument(ctx, message, admin)
		return
	}
	if !message.IsCommand() {
		a.sendHTML(message.Chat.ID, "Use /help to see the available commands.", nil)
		return
	}
	command := strings.ToLower(message.Command())
	args := strings.TrimSpace(message.CommandArguments())
	if admin && a.handleAdminCommand(ctx, message, command, args) {
		return
	}
	switch command {
	case "start":
		a.handleStart(ctx, message, args, admin)
	case "help":
		a.handleHelp(message.Chat.ID, admin)
	case "services":
		a.handleServices(ctx, message.Chat.ID)
	case "getnumber":
		a.handleGetNumber(ctx, message, args)
	case "balance", "account":
		a.handleBalance(ctx, message.Chat.ID, message.From.ID)
	case "mystats", "myprofile":
		a.handleMyStats(ctx, message.Chat.ID, message.From.ID)
	case "myhistory":
		a.handleHistory(ctx, message.Chat.ID, message.From.ID, 0)
	case "top":
		a.handleTop(ctx, message.Chat.ID)
	case "referral":
		a.handleReferral(ctx, message.Chat.ID, message.From.ID)
	case "withdraw":
		a.handleWithdraw(ctx, message, args)
	case "theme", "otpguipreview":
		a.handleTheme(ctx, message, command, args)
	case "premium":
		a.handlePremium(ctx, message.Chat.ID, message.From.ID)
	case "analytics":
		a.handleAnalytics(ctx, message.Chat.ID, message.From.ID)
	case "webhook":
		a.handleWebhook(ctx, message, args)
	case "schedule":
		a.handleSchedule(ctx, message, args)
	case "apikey":
		a.handleAPIKey(ctx, message, args)
	case "tutorials":
		a.handleTutorials(ctx, message.Chat.ID)
	case "settings":
		a.handleSettings(ctx, message.Chat.ID, message.From.ID)
	case "createbot":
		a.handleCreateBot(ctx, message, args)
	case "bots":
		a.handleMyBots(ctx, message.Chat.ID, message.From.ID)
	default:
		a.sendHTML(message.Chat.ID, "Unknown command. Use /help.", nil)
	}
}

func (a *App) handleStart(ctx context.Context, message *tgbotapi.Message, args string, admin bool) {
	if strings.HasPrefix(args, "ref") {
		if referrerID, err := strconv.ParseInt(strings.TrimPrefix(args, "ref"), 10, 64); err == nil {
			_ = a.store.RegisterReferral(ctx, message.From.ID, referrerID)
		}
	}
	missing, _ := a.missingRequiredChats(ctx, message.From.ID)
	if !admin && len(missing) > 0 {
		a.sendJoinRequired(message.Chat.ID, missing)
		return
	}
	name := strings.TrimSpace(message.From.FirstName)
	if name == "" {
		name = "Bhai"
	}
	greeting := "Walaikum Assalam " + name
	if strings.EqualFold(name, "Adnan") {
		greeting = "Walaikum Assalam Adnan Bhai"
	}
	text := fmt.Sprintf("👋 <b>%s</b>\n\nWelcome to the premium OTP management bot. Choose a command below.", html.EscapeString(greeting))
	a.sendHTML(message.Chat.ID, text, compactMenu(admin, a.isMain))
}

func (a *App) handleHelp(chatID int64, admin bool) {
	text := "<b>User commands</b>\n/services — available inventory\n/getnumber Service|Country — assign numbers for 20 minutes\n/mystats, /myhistory — analytics and OTP history\n/theme 0-9, /otpguipreview — premium OTP themes\n/premium — tier and feature access\n/webhook, /schedule, /apikey — Pro/Enterprise integrations\n/tutorials, /settings — guides and preferences\n/balance, /top, /referral, /withdraw\n/createbot Name|token — submit an encrypted child-bot request"
	if admin {
		text += "\n\n<b>Admin commands</b>\n/addgroup, /groups, /groupbuttons, /groupprivacy, /grouptheme, /groupenable, /removegroup\n/setrewards, /rewards, /clearreward, /settier\n/addpanel, /panels, /paneltoggle\n/patternadd, /patterns, /patternremove\n/tutorialadd, /tutorialremove\n/botinstances, /botapprove, /botreject, /bottoggle\nUpload .txt with /addnumbers caption\n/addrequired, /required, /removerequired\n/addadmin user|permissions, /removeadmin\n/withdrawapprove, /withdrawreject, /broadcast, /systemstats"
	}
	a.sendHTML(chatID, text, compactMenu(admin, a.isMain))
}

func (a *App) handleServices(ctx context.Context, chatID int64) {
	catalog, err := a.store.Catalog(ctx)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	if len(catalog) == 0 {
		a.sendHTML(chatID, "No inventory is available right now.", nil)
		return
	}
	var text strings.Builder
	text.WriteString("📱 <b>Available services</b>\n")
	for _, service := range store.SortedServices(catalog) {
		text.WriteString("\n<b>" + html.EscapeString(service) + "</b>\n")
		for _, country := range catalog[service] {
			fmt.Fprintf(&text, "• %s — %d available — %.2f PKR\n", html.EscapeString(country.Country), country.Available, country.PricePKR)
		}
	}
	text.WriteString("\nUse <code>/getnumber Service|Country</code>")
	a.sendHTML(chatID, text.String(), servicesMenu(catalog))
}

func (a *App) handleGetNumber(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 2)
	if len(parts) != 2 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/getnumber Service|Country</code>", nil)
		return
	}
	catalog, err := a.store.Catalog(ctx)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	limit := 3
	if countries, ok := catalog[parts[0]]; ok {
		for _, country := range countries {
			if strings.EqualFold(country.Country, parts[1]) {
				parts[1] = country.Country
				limit = country.PerCycle
			}
		}
	}
	assignment, err := a.store.AssignNumbersForInstance(ctx, a.botInstanceID, message.From.ID, parts[0], parts[1], limit, a.holdDuration)
	if errors.Is(err, store.ErrNoNumbers) {
		a.sendHTML(message.Chat.ID, "No eligible numbers are available. Recycled numbers may still be in your 24-hour personal cooldown.", nil)
		return
	}
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	var text strings.Builder
	fmt.Fprintf(&text, "✅ <b>%d number(s) assigned</b>\n\n", len(assignment.Numbers))
	for i, number := range assignment.Numbers {
		fmt.Fprintf(&text, "%d. <code>+%s</code>\n", i+1, number.NormalizedPhone)
	}
	fmt.Fprintf(&text, "\n⏳ Expires: <b>%s</b>\nNumbers with no OTP return automatically; the first valid OTP consumes its number.",
		assignment.ExpiresAt.In(a.location).Format("03:04:05 PM"))
	serviceIndex, countryIndex := -1, -1
	for i, service := range store.SortedServices(catalog) {
		if service != parts[0] {
			continue
		}
		serviceIndex = i
		for j, item := range catalog[service] {
			if strings.EqualFold(item.Country, parts[1]) {
				countryIndex = j
				break
			}
		}
		break
	}
	var markup any = userBackMenu()
	if serviceIndex >= 0 && countryIndex >= 0 {
		markup = assignmentMenu(serviceIndex, countryIndex)
	}
	a.sendHTML(message.Chat.ID, text.String(), markup)
}

func (a *App) handleBalance(ctx context.Context, chatID, userID int64) {
	pkr, usd, total, err := a.store.UserBalance(ctx, userID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendHTML(chatID, fmt.Sprintf("👤 <b>My Account</b>\n\n🔐 Total OTPs: <b>%d</b>\n💵 PKR balance: <b>%.2f</b>\n💲 USD balance: <b>%.4f</b>", total, pkr, usd), profileMenu())
}

func (a *App) handleTop(ctx context.Context, chatID int64) {
	users, err := a.store.TopUsersForInstance(ctx, a.botInstanceID, 10)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("🏆 <b>Top Users</b>\n\n")
	for i, user := range users {
		name := user.FirstName
		if user.Username != "" {
			name = "@" + user.Username
		}
		fmt.Fprintf(&text, "%d. %s — %d OTPs\n", i+1, html.EscapeString(name), user.TotalOTPs)
	}
	a.sendHTML(chatID, text.String(), userBackMenu())
}

func (a *App) handleReferral(ctx context.Context, chatID, userID int64) {
	total, qualified, earned, err := a.store.ReferralStats(ctx, userID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	link := fmt.Sprintf("https://t.me/%s?start=ref%d", a.bot.Self.UserName, userID)
	a.sendHTML(chatID, fmt.Sprintf("🤝 <b>Referral</b>\n\nLink: %s\nTotal: <b>%d</b>\nQualified: <b>%d</b>\nEarned: <b>%.2f PKR</b>",
		html.EscapeString(link), total, qualified, earned), profileMenu())
}

func (a *App) handleWithdraw(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 2)
	if len(parts) != 2 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/withdraw amount|payment details</code>", nil)
		return
	}
	amount, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || amount <= 0 {
		a.sendHTML(message.Chat.ID, "Withdrawal amount must be positive.", nil)
		return
	}
	id, err := a.store.CreateWithdrawalForInstance(ctx, a.botInstanceID, message.From.ID, "PKR", amount, 0, parts[1])
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Withdrawal request <b>#%d</b> submitted.", id), nil)
}

func (a *App) handleAdminCommand(ctx context.Context, message *tgbotapi.Message, command, args string) bool {
	if permission := adminCommandPermission(command); permission != "" {
		allowed, err := a.store.HasAdminPermission(ctx, a.botInstanceID, message.From.ID, permission)
		if err != nil || !allowed {
			a.sendHTML(message.Chat.ID, "🚫 You do not have permission for this admin action.", nil)
			return true
		}
	}
	switch command {
	case "addgroup":
		parts := splitExact(args, "|", 3)
		if len(parts) < 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/addgroup chat_id|buttons|title</code> (use <code>nobuttons</code> to disable)", nil)
			return true
		}
		chatID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			a.sendHTML(message.Chat.ID, "Invalid chat ID.", nil)
			return true
		}
		buttons := strings.EqualFold(parts[1], "buttons") || strings.EqualFold(parts[1], "on")
		title := "OTP Group"
		if len(parts) == 3 && parts[2] != "" {
			title = parts[2]
		}
		chat, err := a.bot.GetChat(tgbotapi.ChatInfoConfig{ChatConfig: tgbotapi.ChatConfig{ChatID: chatID}})
		if err != nil {
			a.sendHTML(message.Chat.ID, "Bot cannot access that chat: "+html.EscapeString(err.Error()), nil)
			return true
		}
		if chat.Title != "" {
			title = chat.Title
		}
		test := tgbotapi.NewMessage(chatID, "✅ CrackSMS vNext OTP delivery test successful.")
		if _, err := a.bot.Send(test); err != nil {
			a.sendHTML(message.Chat.ID, "Bot cannot post to that chat: "+html.EscapeString(err.Error()), nil)
			return true
		}
		err = a.store.UpsertOTPGroup(ctx, domain.OTPGroupDestination{
			BotInstanceID: a.botInstanceID, ChatID: chatID, Title: title, ButtonsEnabled: buttons, Enabled: true, OTPVisibility: "visible",
		}, message.From.ID)
		if err != nil {
			a.sendError(message.Chat.ID, err)
		} else {
			a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Group <b>%s</b> added; buttons are <b>%s</b>.", html.EscapeString(title), onOff(buttons)), nil)
		}
		return true
	case "groups":
		a.listGroups(ctx, message.Chat.ID)
		return true
	case "groupbuttons":
		parts := splitExact(args, "|", 2)
		if len(parts) != 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/groupbuttons chat_id|on/off</code>", nil)
			return true
		}
		chatID, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			a.sendHTML(message.Chat.ID, "Invalid chat ID.", nil)
			return true
		}
		err := a.store.SetOTPGroupButtonsForInstance(ctx, a.botInstanceID, chatID, strings.EqualFold(parts[1], "on"))
		a.respondAdminResult(message.Chat.ID, "Group button mode updated.", err)
		return true
	case "groupenable":
		parts := splitExact(args, "|", 2)
		if len(parts) != 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/groupenable chat_id|on/off</code>", nil)
			return true
		}
		chatID, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			a.sendHTML(message.Chat.ID, "Invalid chat ID.", nil)
			return true
		}
		err := a.store.SetOTPGroupEnabledForInstance(ctx, a.botInstanceID, chatID, strings.EqualFold(parts[1], "on"))
		a.respondAdminResult(message.Chat.ID, "Group enabled state updated.", err)
		return true
	case "groupprivacy":
		parts := splitExact(args, "|", 2)
		if len(parts) != 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/groupprivacy chat_id|visible/masked/hidden</code>", nil)
			return true
		}
		chatID, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			a.sendHTML(message.Chat.ID, "Invalid chat ID.", nil)
			return true
		}
		err := a.store.SetOTPGroupPrivacy(ctx, a.botInstanceID, chatID, strings.ToLower(parts[1]))
		a.respondAdminResult(message.Chat.ID, "Group OTP privacy updated.", err)
		return true
	case "grouptheme":
		parts := splitExact(args, "|", 2)
		if len(parts) != 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/grouptheme chat_id|0-9/default</code>", nil)
			return true
		}
		chatID, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			a.sendHTML(message.Chat.ID, "Invalid chat ID.", nil)
			return true
		}
		var themeID *int
		if !strings.EqualFold(parts[1], "default") {
			value, parseErr := strconv.Atoi(parts[1])
			if parseErr != nil {
				a.sendHTML(message.Chat.ID, "Theme must be 0-9 or default.", nil)
				return true
			}
			themeID = &value
		}
		err := a.store.SetOTPGroupTheme(ctx, a.botInstanceID, chatID, themeID)
		a.respondAdminResult(message.Chat.ID, "Group theme updated.", err)
		return true
	case "removegroup":
		chatID, err := strconv.ParseInt(args, 10, 64)
		if err == nil {
			err = a.store.RemoveOTPGroupForInstance(ctx, a.botInstanceID, chatID)
		}
		a.respondAdminResult(message.Chat.ID, "Group removed.", err)
		return true
	case "setrewards":
		a.setRewards(ctx, message, args)
		return true
	case "rewards":
		a.listRewards(ctx, message.Chat.ID)
		return true
	case "clearreward":
		userID, err := strconv.ParseInt(args, 10, 64)
		if err == nil {
			err = a.store.RemoveUserRewardOverride(ctx, userID)
		}
		a.respondAdminResult(message.Chat.ID, "User reward override removed.", err)
		return true
	case "addpanel":
		a.addPanel(ctx, message, args)
		return true
	case "panels", "panelhealth":
		a.listPanels(ctx, message.Chat.ID)
		return true
	case "paneltoggle":
		parts := splitExact(args, "|", 2)
		if len(parts) != 2 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/paneltoggle id|on/off</code>", nil)
			return true
		}
		id, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			a.sendHTML(message.Chat.ID, "Invalid panel ID.", nil)
			return true
		}
		err := a.store.SetPanelEnabledForInstance(ctx, a.botInstanceID, id, strings.EqualFold(parts[1], "on"))
		a.respondAdminResult(message.Chat.ID, "Panel state updated; workers will reconcile shortly.", err)
		return true
	case "addrequired":
		parts := splitExact(args, "|", 3)
		if len(parts) != 3 {
			a.sendHTML(message.Chat.ID, "Usage: <code>/addrequired chat_id|title|invite_url</code>", nil)
			return true
		}
		chatID, err := strconv.ParseInt(parts[0], 10, 64)
		if err == nil {
			if parsed, parseErr := url.ParseRequestURI(parts[2]); parseErr != nil || parsed.Scheme != "https" {
				err = errors.New("invite URL must be HTTPS")
			} else {
				err = a.store.UpsertRequiredChat(ctx, store.RequiredChat{BotInstanceID: a.botInstanceID, ChatID: chatID, Title: parts[1], InviteURL: parts[2], Enabled: true})
			}
		}
		a.respondAdminResult(message.Chat.ID, "Required chat saved.", err)
		return true
	case "required":
		chats, err := a.store.ListRequiredChatsForInstance(ctx, a.botInstanceID)
		if err != nil {
			a.sendError(message.Chat.ID, err)
			return true
		}
		var text strings.Builder
		text.WriteString("🔒 <b>Required chats</b>\n")
		for _, chat := range chats {
			fmt.Fprintf(&text, "\n<code>%d</code> — %s", chat.ChatID, html.EscapeString(chat.Title))
		}
		a.sendHTML(message.Chat.ID, text.String(), nil)
		return true
	case "removerequired":
		chatID, err := strconv.ParseInt(args, 10, 64)
		if err == nil {
			err = a.store.RemoveRequiredChatForInstance(ctx, a.botInstanceID, chatID)
		}
		a.respondAdminResult(message.Chat.ID, "Required chat removed.", err)
		return true
	case "addadmin":
		parts := splitExact(args, "|", 2)
		userID, err := strconv.ParseInt(parts[0], 10, 64)
		permissions := []string{"*"}
		if len(parts) == 2 && parts[1] != "" {
			permissions = strings.Split(strings.ReplaceAll(parts[1], " ", ""), ",")
		}
		if err == nil {
			err = a.store.AddInstanceAdmin(ctx, a.botInstanceID, userID, permissions)
		}
		a.respondAdminResult(message.Chat.ID, "Admin added.", err)
		return true
	case "removeadmin":
		userID, err := strconv.ParseInt(args, 10, 64)
		if err == nil {
			err = a.store.RemoveInstanceAdmin(ctx, a.botInstanceID, userID)
		}
		a.respondAdminResult(message.Chat.ID, "Admin removed.", err)
		return true
	case "withdrawapprove", "withdrawreject":
		id, err := strconv.ParseInt(args, 10, 64)
		if err == nil {
			err = a.store.ResolveWithdrawalForInstance(ctx, a.botInstanceID, id, message.From.ID, command == "withdrawapprove")
		}
		a.respondAdminResult(message.Chat.ID, "Withdrawal resolved.", err)
		return true
	case "broadcast":
		if args == "" {
			a.sendHTML(message.Chat.ID, "Usage: <code>/broadcast message</code>", nil)
			return true
		}
		go a.broadcast(ctx, message.Chat.ID, args)
		a.sendHTML(message.Chat.ID, "📢 Broadcast queued.", nil)
		return true
	case "settheme":
		themeID, err := strconv.Atoi(args)
		if err == nil {
			err = a.store.SetInstanceTheme(ctx, a.botInstanceID, themeID)
		}
		a.respondAdminResult(message.Chat.ID, "Default OTP theme updated.", err)
		return true
	case "settier":
		a.adminSetTier(ctx, message, args)
		return true
	case "tutorialadd":
		a.adminAddTutorial(ctx, message, args)
		return true
	case "tutorialremove":
		id, err := strconv.ParseInt(args, 10, 64)
		if err == nil {
			err = a.store.DeleteTutorial(ctx, a.botInstanceID, id)
		}
		a.respondAdminResult(message.Chat.ID, "Tutorial removed.", err)
		return true
	case "botinstances":
		a.adminListBots(ctx, message.Chat.ID)
		return true
	case "botapprove":
		a.adminApproveBot(ctx, message, args)
		return true
	case "botreject":
		a.adminRejectBot(ctx, message, args)
		return true
	case "bottoggle":
		a.adminToggleBot(ctx, message, args)
		return true
	case "systemstats", "stats":
		a.handleAnalyticsAdmin(ctx, message.Chat.ID)
		return true
	case "patternadd":
		a.adminAddPattern(ctx, message, args)
		return true
	case "patterns":
		a.adminListPatterns(ctx, message.Chat.ID)
		return true
	case "patternremove":
		id, err := strconv.ParseInt(args, 10, 64)
		if err == nil {
			err = a.store.RemoveCustomOTPPattern(ctx, a.botInstanceID, id)
		}
		a.respondAdminResult(message.Chat.ID, "Custom OTP pattern removed.", err)
		return true
	}
	return false
}

func (a *App) handleDocument(ctx context.Context, message *tgbotapi.Message, admin bool) {
	if !admin || !strings.HasPrefix(strings.TrimSpace(message.Caption), "/addnumbers") {
		a.sendHTML(message.Chat.ID, "Only admins can import number files.", nil)
		return
	}
	allowed, err := a.store.HasAdminPermission(ctx, a.botInstanceID, message.From.ID, "manage_settings")
	if err != nil || !allowed {
		a.sendHTML(message.Chat.ID, "🚫 You do not have permission to import number files.", nil)
		return
	}
	args := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(message.Caption), "/addnumbers"))
	parts := splitExact(args, "|", 6)
	if len(parts) != 6 {
		a.sendHTML(message.Chat.ID, "Caption format: <code>/addnumbers Service|Country|CC|PricePKR|PriceUSD|PerCycle</code>", nil)
		return
	}
	pricePKR, err1 := strconv.ParseFloat(parts[3], 64)
	priceUSD, err2 := strconv.ParseFloat(parts[4], 64)
	perCycle, err3 := strconv.Atoi(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || pricePKR < 0 || priceUSD < 0 || perCycle <= 0 {
		a.sendHTML(message.Chat.ID, "Invalid price or per-cycle value.", nil)
		return
	}
	fileURL, err := a.bot.GetFileDirectURL(message.Document.FileID)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	phones := strings.Fields(string(body))
	added, err := a.store.AddNumbers(ctx, parts[0], parts[1], parts[2], pricePKR, priceUSD, perCycle, phones)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ <b>%d</b> unique valid numbers imported.", added), nil)
}

func (a *App) listGroups(ctx context.Context, chatID int64) {
	groups, err := a.store.ListOTPGroupsForInstance(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("📨 <b>OTP destinations</b>\n")
	for _, group := range groups {
		theme := "default"
		if group.ThemeID != nil {
			theme = strconv.Itoa(*group.ThemeID)
		}
		fmt.Fprintf(&text, "\n<code>%d</code> — %s\nEnabled: %s · Buttons: %s · Privacy: %s · Theme: %s · Healthy: %s\n",
			group.ChatID, html.EscapeString(group.Title), onOff(group.Enabled), onOff(group.ButtonsEnabled),
			html.EscapeString(group.OTPVisibility), theme, onOff(group.Healthy))
		if group.LastError != "" {
			text.WriteString("Error: " + html.EscapeString(group.LastError) + "\n")
		}
	}
	a.sendHTML(chatID, text.String(), adminGroupsMenu(groups))
}

func (a *App) setRewards(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 2)
	if len(parts) != 2 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/setrewards global|30=30,100=30,200=50</code> or replace global with a user ID.", nil)
		return
	}
	var userID *int64
	name := "Global daily rewards"
	if !strings.EqualFold(parts[0], "global") {
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			a.sendHTML(message.Chat.ID, "Reward target must be global or a user ID.", nil)
			return
		}
		userID = &id
		name = fmt.Sprintf("User %d rewards", id)
	}
	var rules []domain.RewardRule
	for _, item := range strings.Split(parts[1], ",") {
		pair := splitExact(item, "=", 2)
		if len(pair) != 2 {
			a.sendHTML(message.Chat.ID, "Each reward must use threshold=amount.", nil)
			return
		}
		threshold, err1 := strconv.Atoi(pair[0])
		amount, err2 := strconv.ParseFloat(pair[1], 64)
		if err1 != nil || err2 != nil {
			a.sendHTML(message.Chat.ID, "Invalid reward threshold or amount.", nil)
			return
		}
		rules = append(rules, domain.RewardRule{Threshold: threshold, AmountPKR: amount})
	}
	id, err := a.store.ReplaceRewardSchedule(ctx, name, userID, rules, message.From.ID)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Reward schedule <b>#%d</b> activated. Milestones are cumulative and custom schedules replace global rewards.", id), nil)
}

func (a *App) listRewards(ctx context.Context, chatID int64) {
	schedules, err := a.store.ListRewardSchedules(ctx)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("🎁 <b>Active reward schedules</b>\n")
	for _, schedule := range schedules {
		target := "Global"
		if schedule.UserID != nil {
			target = fmt.Sprintf("User %d", *schedule.UserID)
		}
		fmt.Fprintf(&text, "\n<b>%s</b>\n", target)
		for _, rule := range schedule.Rules {
			fmt.Fprintf(&text, "• %d OTP = %.2f PKR extra\n", rule.Threshold, rule.AmountPKR)
		}
	}
	a.sendHTML(chatID, text.String(), adminRewardsMenu(schedules))
}

func (a *App) addPanel(ctx context.Context, message *tgbotapi.Message, args string) {
	parts := splitExact(args, "|", 4)
	if len(parts) != 4 {
		a.sendHTML(message.Chat.ID, "Usage: <code>/addpanel kind|name|poll_duration|config_json</code>\nKinds: token_api, legacy_api, login, websocket", nil)
		return
	}
	poll, err := time.ParseDuration(parts[2])
	if err != nil || poll < time.Second {
		a.sendHTML(message.Chat.ID, "Poll duration must be at least 1s.", nil)
		return
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(parts[3]), &config); err != nil {
		a.sendHTML(message.Chat.ID, "Invalid panel JSON: "+html.EscapeString(err.Error()), nil)
		return
	}
	tier, err := a.store.BotInstanceTier(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	count, err := a.store.CountPanelsForInstance(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	if count >= store.TierPanelLimit(tier) {
		a.sendHTML(message.Chat.ID, fmt.Sprintf("Panel limit reached for the <b>%s</b> tier (%d).", tier, store.TierPanelLimit(tier)), nil)
		return
	}
	panel := domain.Panel{BotInstanceID: a.botInstanceID, Name: parts[1], Kind: parts[0], Config: config, PollInterval: poll, Enabled: true}
	adapter, err := panels.NewAdapter(panel)
	if err == nil {
		testCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = adapter.Test(testCtx)
		cancel()
		adapter.Close()
	}
	// Delete the Telegram command because it may contain credentials.
	_, _ = a.bot.Request(tgbotapi.NewDeleteMessage(message.Chat.ID, message.MessageID))
	if err != nil {
		a.sendHTML(message.Chat.ID, "Panel test failed; nothing was saved: "+html.EscapeString(err.Error()), nil)
		return
	}
	id, err := a.store.UpsertPanelForInstance(ctx, a.botInstanceID, panel)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	a.sendHTML(message.Chat.ID, fmt.Sprintf("✅ Panel <b>#%d %s</b> saved with encrypted configuration.", id, html.EscapeString(panel.Name)), nil)
}

func (a *App) listPanels(ctx context.Context, chatID int64) {
	report, err := a.store.PanelHealthReportForInstance(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	var text strings.Builder
	text.WriteString("📡 <b>Panel health</b>\n")
	for _, panel := range report {
		fmt.Fprintf(&text, "\n<b>#%d %s</b> (%s)\nEnabled: %s · Healthy: %s · Failures: %d · OTPs: %d\n",
			panel.ID, html.EscapeString(panel.Name), panel.Kind, onOff(panel.Enabled), onOff(panel.Healthy), panel.Failures, panel.OTPCount)
		if panel.LastError != "" {
			text.WriteString("Error: " + html.EscapeString(panel.LastError) + "\n")
		}
	}
	a.sendHTML(chatID, text.String(), adminPanelMenu(report))
}

func (a *App) broadcast(ctx context.Context, adminChatID int64, text string) {
	ids, err := a.store.AllUserIDsForInstance(ctx, a.botInstanceID)
	if err != nil {
		a.sendError(adminChatID, err)
		return
	}
	sent, failed := 0, 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		message := tgbotapi.NewMessage(id, premium.AnimateHTML(text))
		message.ParseMode = tgbotapi.ModeHTML
		if _, err := a.bot.Send(message); err != nil {
			failed++
		} else {
			sent++
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	a.sendHTML(adminChatID, fmt.Sprintf("📢 Broadcast complete. Sent: <b>%d</b>, failed: <b>%d</b>.", sent, failed), nil)
}

func (a *App) missingRequiredChats(ctx context.Context, userID int64) ([]store.RequiredChat, error) {
	chats, err := a.store.ListRequiredChatsForInstance(ctx, a.botInstanceID)
	if err != nil {
		return nil, err
	}
	var missing []store.RequiredChat
	for _, chat := range chats {
		member, err := a.bot.GetChatMember(tgbotapi.GetChatMemberConfig{
			ChatConfigWithUser: tgbotapi.ChatConfigWithUser{ChatID: chat.ChatID, UserID: userID},
		})
		if err != nil || member.Status == "left" || member.Status == "kicked" {
			missing = append(missing, chat)
		}
	}
	return missing, nil
}

func (a *App) sendJoinRequired(chatID int64, missing []store.RequiredChat) {
	rows := make([][]premium.InlineButton, 0, len(missing)+1)
	for _, chat := range missing {
		rows = append(rows, []premium.InlineButton{{Text: "Join " + chat.Title, URL: chat.InviteURL, Style: "primary", IconCustomEmojiID: premium.ID("channel")}})
	}
	rows = append(rows, []premium.InlineButton{{Text: "Check Membership", CallbackData: "check_membership", Style: "success", IconCustomEmojiID: premium.ID("check")}})
	a.sendHTML(chatID, "🔒 Join all required chats, then press Check membership.", premium.InlineKeyboard{InlineKeyboard: rows})
}

func (a *App) handleCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	if callback.From == nil || callback.Message == nil {
		return
	}
	_, _ = a.bot.Request(tgbotapi.NewCallback(callback.ID, ""))
	_ = a.store.EnsureUserForInstance(ctx, a.botInstanceID, callback.From.ID, callback.From.UserName, callback.From.FirstName, callback.From.LastName)
	if callback.Data == "check_membership" {
		missing, err := a.missingRequiredChats(ctx, callback.From.ID)
		if err != nil || len(missing) > 0 {
			a.sendJoinRequired(callback.Message.Chat.ID, missing)
			return
		}
		admin, _ := a.store.HasAnyAdminRole(ctx, a.botInstanceID, callback.From.ID)
		a.sendHTML(callback.Message.Chat.ID, "✅ Membership verified.", compactMenu(admin, a.isMain))
		return
	}
	chatID := callback.Message.Chat.ID
	admin, _ := a.store.HasAnyAdminRole(ctx, a.botInstanceID, callback.From.ID)
	if a.handleStyledCallback(ctx, callback, admin) {
		return
	}
	if strings.HasPrefix(callback.Data, "admin:") {
		permission := map[string]string{
			"admin:panels": "manage_panels", "admin:groups": "manage_groups", "admin:analytics": "view_analytics",
			"admin:rewards": "manage_rewards", "admin:bots": "manage_bots", "admin:tutorials": "manage_tutorials",
		}[callback.Data]
		allowed, _ := a.store.HasAdminPermission(ctx, a.botInstanceID, callback.From.ID, permission)
		if !admin || permission == "" || !allowed {
			a.sendHTML(chatID, "🚫 You do not have permission for this admin action.", nil)
			return
		}
	}
	switch {
	case callback.Data == "menu:services":
		a.handleServices(ctx, chatID)
	case callback.Data == "menu:profile":
		a.handleBalance(ctx, chatID, callback.From.ID)
	case callback.Data == "menu:stats":
		a.handleMyStats(ctx, chatID, callback.From.ID)
	case strings.HasPrefix(callback.Data, "menu:history:"):
		offset, _ := strconv.Atoi(strings.TrimPrefix(callback.Data, "menu:history:"))
		a.handleHistory(ctx, chatID, callback.From.ID, offset)
	case callback.Data == "menu:premium":
		a.handlePremium(ctx, chatID, callback.From.ID)
	case callback.Data == "menu:settings":
		a.handleSettings(ctx, chatID, callback.From.ID)
	case callback.Data == "menu:tutorials":
		a.handleTutorials(ctx, chatID)
	case callback.Data == "menu:analytics":
		a.handleAnalytics(ctx, chatID, callback.From.ID)
	case callback.Data == "menu:themes":
		a.handleTheme(ctx, &tgbotapi.Message{Chat: callback.Message.Chat, From: callback.From}, "theme", "")
	case callback.Data == "menu:webhooks":
		a.handleWebhook(ctx, &tgbotapi.Message{Chat: callback.Message.Chat, From: callback.From}, "")
	case callback.Data == "menu:schedule":
		a.handleSchedule(ctx, &tgbotapi.Message{Chat: callback.Message.Chat, From: callback.From}, "")
	case callback.Data == "menu:api":
		a.handleAPIKey(ctx, &tgbotapi.Message{Chat: callback.Message.Chat, From: callback.From}, "")
	case callback.Data == "menu:createbot":
		a.sendHTML(chatID, "🤖 <b>Create a child bot</b>\n\n1. Create a unique token with @BotFather.\n2. Send <code>/createbot Bot Name|token</code>.\n3. An admin selects Free, Pro, or Enterprise and approves it.\n\nThe credential message is automatically deleted and the token is encrypted.", nil)
	case callback.Data == "menu:help":
		a.handleHelp(chatID, admin)
	case callback.Data == "menu:full":
		a.sendHTML(chatID, "📖 <b>Full Menu</b>", fullMenu(admin, a.isMain, a.links))
	case callback.Data == "menu:compact":
		a.sendHTML(chatID, "🏠 <b>Main Menu</b>", compactMenu(admin, a.isMain))
	case callback.Data == "menu:admin":
		if admin {
			a.sendHTML(chatID, "👮 <b>Admin Dashboard</b>", adminDashboard())
		}
	case strings.HasPrefix(callback.Data, "theme:set:"):
		themeID, err := strconv.Atoi(strings.TrimPrefix(callback.Data, "theme:set:"))
		if err == nil {
			err = a.store.SetUserTheme(ctx, a.botInstanceID, callback.From.ID, themeID)
		}
		if err != nil {
			a.sendError(chatID, err)
		} else {
			a.sendHTML(chatID, fmt.Sprintf("✅ Theme changed to <b>T%d · %s</b>.", themeID, themes.Get(themeID).Name), nil)
		}
	case strings.HasPrefix(callback.Data, "tutorial:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(callback.Data, "tutorial:"), 10, 64)
		a.sendTutorial(ctx, chatID, id)
	case callback.Data == "admin:panels" && admin:
		a.listPanels(ctx, chatID)
	case callback.Data == "admin:groups" && admin:
		a.listGroups(ctx, chatID)
	case callback.Data == "admin:analytics" && admin:
		a.handleAnalyticsAdmin(ctx, chatID)
	case callback.Data == "admin:rewards" && admin:
		a.listRewards(ctx, chatID)
	case callback.Data == "admin:bots" && admin:
		a.adminListBots(ctx, chatID)
	case callback.Data == "admin:tutorials" && admin:
		a.handleTutorials(ctx, chatID)
	}
}

func (a *App) sendHTML(chatID int64, text string, markup any) {
	message := tgbotapi.NewMessage(chatID, premium.AnimateHTML(text))
	message.ParseMode = tgbotapi.ModeHTML
	message.DisableWebPagePreview = true
	message.ReplyMarkup = markup
	if _, err := a.bot.Send(message); err != nil {
		slog.Warn("Telegram response failed", "chat_id", chatID, "error", err)
	}
}

func (a *App) sendError(chatID int64, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		a.sendHTML(chatID, "Requested item was not found.", nil)
		return
	}
	slog.Error("command failed", "chat_id", chatID, "error", err)
	a.sendHTML(chatID, "❌ The action could not be completed safely. Please try again or check the logs.", nil)
}

func (a *App) respondAdminResult(chatID int64, success string, err error) {
	if err != nil {
		a.sendError(chatID, err)
		return
	}
	a.sendHTML(chatID, "✅ "+success, nil)
}

func mainKeyboard(admin bool) premium.ReplyKeyboard {
	rows := [][]premium.KeyboardButton{
		{{Text: "/services", Style: "primary", IconCustomEmojiID: premium.ID("phone")}, {Text: "/balance", Style: "success", IconCustomEmojiID: premium.ID("money")}},
		{{Text: "/top", Style: "primary", IconCustomEmojiID: premium.ID("gold")}, {Text: "/referral", Style: "success", IconCustomEmojiID: premium.ID("link")}},
	}
	if admin {
		rows = append(rows, []premium.KeyboardButton{
			{Text: "/groups", Style: "primary", IconCustomEmojiID: premium.ID("channel")},
			{Text: "/panels", Style: "primary", IconCustomEmojiID: premium.ID("chart")},
			{Text: "/rewards", Style: "danger", IconCustomEmojiID: premium.ID("celebrate")},
		})
	}
	return premium.ReplyKeyboard{Keyboard: rows, ResizeKeyboard: true, IsPersistent: true}
}

func splitExact(value, separator string, max int) []string {
	raw := strings.SplitN(value, separator, max)
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		parts = append(parts, strings.TrimSpace(part))
	}
	return parts
}

func onOff(value bool) string {
	if value {
		return "ON"
	}
	return "OFF"
}

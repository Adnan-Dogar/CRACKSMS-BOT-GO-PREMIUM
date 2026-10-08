package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	countryinfo "github.com/adnan-dogar/cracksms-vnext/internal/country"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
)

func importKeyboard(flow store.TelegramFlow, menu premium.InlineKeyboard) premium.InlineKeyboard {
	rows := make([][]premium.InlineButton, len(menu.InlineKeyboard))
	for i, row := range menu.InlineKeyboard {
		rows[i] = append([]premium.InlineButton(nil), row...)
		for k := range rows[i] {
			b := &rows[i][k]
			if b.CallbackData == "flow:cancel" && flow.Data["nonce"] != "" {
				b.CallbackData = "admin:upload:cancel"
			}
			if strings.HasPrefix(b.CallbackData, "admin:upload:") && flow.Data["nonce"] != "" {
				b.CallbackData += "~" + flow.Data["nonce"]
			}
		}
	}
	return premium.InlineKeyboard{InlineKeyboard: rows}
}
func (a *App) importScreen(ctx context.Context, chat, user int64, flow store.TelegramFlow, text string, menu premium.InlineKeyboard) {
	if id, _ := strconv.Atoi(flow.Data["screen_id"]); id != 0 {
		a.screenChatID = chat
		a.screenMessageID = id
	}
	if err := a.renderDocument(chat, interfaceDocument(text, importKeyboard(flow, menu))); err != nil {
		slog.Warn("import screen unavailable", "error_code", store.ImportErrorCode(err))
		return
	}
	if a.lastMessageID != 0 {
		flow.Data["screen_id"] = strconv.Itoa(a.lastMessageID)
	}
	flow.ExpiresAt = time.Now().Add(interactiveFlowLifetime)
	if err := a.store.SetTelegramFlow(ctx, a.botInstanceID, user, flow); err != nil {
		a.sendError(chat, err)
		return
	}
	if id, _ := strconv.ParseInt(flow.Data["import_id"], 10, 64); id != 0 {
		_ = a.store.SetImportMessage(ctx, a.botInstanceID, user, id, chat, a.lastMessageID)
	}
}
func (a *App) captureNumberFile(ctx context.Context, message *tgbotapi.Message, flow store.TelegramFlow) {
	body, err := a.downloadTelegramFile(ctx, message.Document.FileID)
	if err != nil {
		a.sendHTML(message.Chat.ID, "❌ The file could not be downloaded. Upload it again; no inventory was changed.", flowCancelMenu("admin:numbers"))
		return
	}
	phones, invalid, repeated := parseImportPhones(body)
	if len(phones) == 0 {
		a.sendHTML(message.Chat.ID, "❌ No valid phone numbers found. Send a TXT/CSV containing phone numbers.", flowCancelMenu("admin:numbers"))
		return
	}
	numbers := make([]store.ImportNumber, 0, len(phones))
	for _, phone := range phones {
		info := countryinfo.Detect(phone)
		if info.Code == "" {
			info = countryinfo.Info{Code: "UN", Name: "Unknown"}
		}
		if flow.Data["country"] != "" {
			info = countryinfo.Info{Name: flow.Data["country"], Code: flow.Data["country_code"]}
		}
		numbers = append(numbers, store.ImportNumber{Phone: phone, Country: info.Name, CountryCode: info.Code})
	}
	if flow.Data["nonce"] == "" {
		flow.Data["nonce"] = broadcastKey()
	}
	job, err := a.store.CreateImportDraft(ctx, a.botInstanceID, message.From.ID, message.Chat.ID, flow.Data["nonce"], message.Document.FileName, numbers, invalid, repeated)
	if err != nil {
		a.sendError(message.Chat.ID, err)
		return
	}
	flow.Data["import_id"] = fmt.Sprint(job.ID)
	flow.Data["file_name"] = job.FileName
	if flow.Data["service"] != "" {
		if flow.Data["emoji_id"] == "" {
			flow.Data["emoji_id"] = premium.AppEmojiID(flow.Data["service"])
		}
		if flow.Data["price_pkr"] != "" {
			a.confirmNumberImport(ctx, message.Chat.ID, message.From.ID, flow)
		} else {
			a.promptImportPricing(ctx, message.Chat.ID, message.From.ID, flow)
		}
		return
	}
	flow.Step = "multi_service"
	flow.Data["services"] = "[]"
	a.importScreen(ctx, message.Chat.ID, message.From.ID, flow, fmt.Sprintf("📤 <b>Select Import Apps</b>\n\nFile: <code>%s</code>\nUnique phones: %d\nSelected apps: 0 / 20\nChoose apps, then Continue. Each app receives independent inventory.", html.EscapeString(job.FileName), job.ValidCount), a.multiImportMenu(ctx, flow))
}
func (a *App) promptImportPricing(ctx context.Context, chat, user int64, flow store.TelegramFlow) {
	flow.Step = "pricing"
	names := strings.Join(importServices(flow), ", ")
	a.importScreen(ctx, chat, user, flow, fmt.Sprintf("💰 <b>Import Pricing</b>\n\nApps: %s\nSend <code>PricePKR|PriceUSD|NumbersPerCycle</code>, for example <code>1|0|3</code>. These settings apply to every selected app and imported country. Countries are detected from the numbers unless supplied in the caption.", html.EscapeString(names)), uploadPricingMenu())
}
func (a *App) importSettings(ctx context.Context, flow store.TelegramFlow) (store.ImportSettings, error) {
	pkr, e1 := strconv.ParseFloat(flow.Data["price_pkr"], 64)
	usd, e2 := strconv.ParseFloat(flow.Data["price_usd"], 64)
	cycle, e3 := strconv.Atoi(flow.Data["per_cycle"])
	if e1 != nil || e2 != nil || e3 != nil || pkr < 0 || usd < 0 || cycle < 1 || math.IsNaN(pkr) || math.IsNaN(usd) || math.IsInf(pkr, 0) || math.IsInf(usd, 0) {
		return store.ImportSettings{}, errors.New("invalid pricing")
	}
	settings := store.ImportSettings{}
	for _, name := range importServices(flow) {
		id, _ := a.store.ServiceEmojiID(ctx, a.botInstanceID, name)
		if id == "" {
			id = premium.AppEmojiID(name)
		}
		if name == flow.Data["service"] && flow.Data["emoji_id"] != "" {
			id = flow.Data["emoji_id"]
		}
		if custom := importCustomApps(flow)[name]; custom != "" {
			id = custom
		}
		settings.Services = append(settings.Services, store.ImportService{Name: name, EmojiID: id, PricePKR: pkr, PriceUSD: usd, PerCycle: cycle})
	}
	return settings, nil
}
func (a *App) confirmNumberImport(ctx context.Context, chat, user int64, flow store.TelegramFlow) {
	id, _ := strconv.ParseInt(flow.Data["import_id"], 10, 64)
	job, err := a.store.ImportJob(ctx, a.botInstanceID, user, id)
	if err != nil {
		a.sendError(chat, err)
		return
	}
	numbers, err := a.store.ImportNumbers(ctx, job)
	if err != nil {
		a.sendHTML(chat, "⏳ This upload expired. Upload the file again.", adminNumbersMenu())
		return
	}
	settings, err := a.importSettings(ctx, flow)
	if err != nil {
		a.sendHTML(chat, "❌ Pricing must be finite, non-negative, with at least one number per request.", uploadPricingMenu())
		return
	}
	phones := make([]string, 0, len(numbers))
	countries := map[string]int{}
	for _, n := range numbers {
		phones = append(phones, n.Phone)
		countries[n.Country]++
	}
	text := fmt.Sprintf("📋 <b>Confirm Number Import</b>\n\nFile: <code>%s</code>\nUnique phones: %d\nInvalid entries: %d\nRepeated in file: %d\nCountries: %d\nPrice: %s PKR / %s USD\nNumbers per request: %s\n\n", html.EscapeString(job.FileName), job.ValidCount, job.InvalidCount, job.DuplicateCount, len(countries), html.EscapeString(flow.Data["price_pkr"]), html.EscapeString(flow.Data["price_usd"]), html.EscapeString(flow.Data["per_cycle"]))
	total := 0
	for _, service := range settings.Services {
		existing, e := a.store.PreviewNumbers(ctx, service.Name, phones)
		if e != nil {
			a.sendError(chat, e)
			return
		}
		additions := len(phones) - existing
		total += additions
		text += fmt.Sprintf("%s %s: %d additions · %d already present\n", premium.CustomEmoji(service.EmojiID, "📱"), html.EscapeString(service.Name), additions, existing)
	}
	text += fmt.Sprintf("\nApp inventory additions: %d\nAll selected apps become available together. Existing stock and assignments are kept; catalog pricing for the imported countries uses these confirmed settings.", total)
	flow.Step = "confirm"
	a.importScreen(ctx, chat, user, flow, text, uploadConfirmMenu())
}
func (a *App) finishNumberImport(ctx context.Context, chat, user int64, flow store.TelegramFlow) {
	allowed, err := a.store.HasAdminPermission(ctx, a.botInstanceID, user, "manage_settings")
	if err != nil || !allowed {
		a.sendHTML(chat, "🔒 Inventory permission is required.", userBackMenu())
		return
	}
	settings, err := a.importSettings(ctx, flow)
	if err != nil {
		a.sendHTML(chat, "❌ Correct the import pricing before continuing.", adminNumbersMenu())
		return
	}
	id, _ := strconv.ParseInt(flow.Data["import_id"], 10, 64)
	job, err := a.store.QueueImport(ctx, a.botInstanceID, user, id, settings, flow)
	if err != nil {
		a.sendError(chat, err)
		return
	}
	if screen, _ := strconv.Atoi(flow.Data["screen_id"]); screen != 0 {
		job.MessageID = screen
	}
	if err := a.showImportJob(ctx, chat, user, job); err != nil {
		slog.Warn("import screen unavailable", "job_id", job.ID, "error_code", store.ImportErrorCode(err))
	}
}
func importStateText(job store.ImportJob) string {
	switch job.State {
	case "draft":
		return "Awaiting confirmation"
	case "queued":
		return "Queued"
	case "running":
		return "Preparing all apps"
	case "succeeded":
		return "Complete"
	case "failed":
		return "Failed · retry available"
	case "cancelled":
		return "Cancelled"
	}
	return job.State
}
func (a *App) showImportJob(ctx context.Context, chat, user int64, job store.ImportJob) error {
	text := fmt.Sprintf("📤 <b>Import #%d</b>\n\nFile: <code>%s</code>\nStatus: %s\nUnique phones: %d\nInvalid entries: %d\nRepeated in file: %d\nAttempts: %d\n", job.ID, html.EscapeString(job.FileName), importStateText(job), job.ValidCount, job.InvalidCount, job.DuplicateCount, job.Attempts)
	added := 0
	for _, r := range job.Results {
		added += r.Added
		text += fmt.Sprintf("\n%s %s: %d added · %d already present", premium.AppEmoji(r.Service, "📱"), html.EscapeString(r.Service), r.Added, r.Existing)
	}
	if job.State == "succeeded" {
		text += fmt.Sprintf("\n\n✅ %d app inventory entries published together.", added)
	}
	if job.State == "queued" || job.State == "running" {
		text += "\n\n⏳ Preparing the import. All selected apps will become available together."
	}
	if job.State == "failed" {
		text += "\n\n❌ No inventory was published. Retry this saved upload, or cancel it.\nError reference: <code>" + html.EscapeString(job.ErrorCode) + "</code>"
	}
	rows := [][]premium.InlineButton{}
	if job.State == "failed" && job.ExpiresAt.After(time.Now()) {
		rows = append(rows, []premium.InlineButton{premium.Button("Retry", fmt.Sprintf("admin:imports:retry:%d", job.ID), "success", "refresh")})
	}
	if job.State == "draft" || job.State == "queued" || job.State == "running" || job.State == "failed" {
		rows = append(rows, []premium.InlineButton{premium.Button("Cancel Import", fmt.Sprintf("admin:imports:cancel:%d", job.ID), "danger", "cancel")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Refresh", fmt.Sprintf("admin:imports:view:%d", job.ID), "primary", "refresh"), premium.Button("Import History", "admin:imports:list:0", "primary", "history")}, []premium.InlineButton{premium.Button("Numbers", "admin:numbers", "primary", "phone"), premium.Button("Home", "menu:admin", "primary", "home")})
	if job.MessageID != 0 {
		a.screenChatID = chat
		a.screenMessageID = job.MessageID
	}
	doc := interfaceDocument(text, premium.InlineKeyboard{InlineKeyboard: rows})
	if err := a.renderDocument(chat, doc); err != nil {
		return err
	}
	return a.store.SetImportMessage(ctx, a.botInstanceID, user, job.ID, chat, a.lastMessageID)
}
func (a *App) showImportHistory(ctx context.Context, chat, user int64, offset int) {
	jobs, err := a.store.ListImports(ctx, a.botInstanceID, user, offset)
	if err != nil {
		a.sendError(chat, err)
		return
	}
	text := "📤 <b>Import History</b>\n\nSelect an import to view its result or retry it.\n"
	rows := [][]premium.InlineButton{}
	for _, job := range jobs {
		text += fmt.Sprintf("\n#%d: %s · %d phones", job.ID, importStateText(job), job.ValidCount)
		rows = append(rows, []premium.InlineButton{premium.Button(fmt.Sprintf("#%d · %s", job.ID, job.FileName), fmt.Sprintf("admin:imports:view:%d", job.ID), "primary", "document")})
	}
	if len(jobs) == 0 {
		text += "\nNo imports yet."
	}
	nav := []premium.InlineButton{}
	if offset > 0 {
		nav = append(nav, premium.Button("Previous", fmt.Sprintf("admin:imports:list:%d", max(0, offset-20)), "primary", "back"))
	}
	if len(jobs) == 20 {
		nav = append(nav, premium.Button("Next", fmt.Sprintf("admin:imports:list:%d", offset+20), "primary", "list"))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Upload Numbers", "admin:numbers:upload", "success", "upload"), premium.Button("Back", "admin:numbers", "primary", "back")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) handleImportJobCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) bool {
	if !strings.HasPrefix(cb.Data, "admin:imports:") {
		return false
	}
	allowed, err := a.store.HasAdminPermission(ctx, a.botInstanceID, cb.From.ID, "manage_settings")
	if err != nil || !allowed {
		a.sendHTML(cb.Message.Chat.ID, "🔒 Inventory permission is required.", userBackMenu())
		return true
	}
	p := strings.Split(cb.Data, ":")
	if len(p) != 4 {
		return true
	}
	id, err := strconv.ParseInt(p[3], 10, 64)
	if err != nil || id < 0 {
		return true
	}
	a.screenChatID = cb.Message.Chat.ID
	a.screenMessageID = cb.Message.MessageID
	if p[2] == "list" {
		a.showImportHistory(ctx, cb.Message.Chat.ID, cb.From.ID, int(id))
		return true
	}
	var job store.ImportJob
	if p[2] == "retry" || p[2] == "cancel" {
		job, err = a.store.ChangeImportState(ctx, a.botInstanceID, cb.From.ID, id, p[2])
	} else {
		job, err = a.store.ImportJob(ctx, a.botInstanceID, cb.From.ID, id)
	}
	if err != nil {
		a.sendError(cb.Message.Chat.ID, err)
		return true
	}
	job.MessageID = cb.Message.MessageID
	if err = a.showImportJob(ctx, cb.Message.Chat.ID, cb.From.ID, job); err != nil {
		slog.Warn("import screen unavailable", "job_id", id, "error_code", store.ImportErrorCode(err))
	}
	return true
}
func (a *App) importBackgroundScreen(ctx context.Context, id, user int64) error {
	lock := a.userLock(user)
	lock.Lock()
	defer lock.Unlock()
	job, err := a.store.ImportJob(ctx, a.botInstanceID, user, id)
	if err != nil {
		return err
	}
	if job.MessageID == 0 {
		return nil
	}
	request := *a
	request.group = nil
	request.screenMessageID = 0
	request.lastMessageID = 0
	request.updatePreferences(ctx, user)
	return request.showImportJob(ctx, job.ChatID, user, job)
}

func (a *App) runImports(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastCleanup := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Since(lastCleanup) > time.Minute {
			_ = a.store.CleanupImports(ctx, a.botInstanceID)
			lastCleanup = time.Now()
		}
		job, err := a.store.ClaimImport(ctx, a.botInstanceID)
		if err == nil {
			runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			leaseDone := make(chan struct{})
			go func() {
				defer close(leaseDone)
				renew := time.NewTicker(20 * time.Second)
				defer renew.Stop()
				for {
					select {
					case <-runCtx.Done():
						return
					case <-renew.C:
						ok, e := a.store.RenewImportLease(runCtx, job)
						if e != nil || !ok {
							cancel()
							return
						}
					}
				}
			}()
			_, err = a.store.ExecuteImport(runCtx, job)
			cancel()
			<-leaseDone
			if err != nil && ctx.Err() == nil {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				_ = a.store.FailImport(cleanup, job, err)
				stop()
				slog.Error("number import failed", "job_id", job.ID, "stage", "publication", "error_code", store.ImportErrorCode(err))
			}
		} else if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
			slog.Warn("import queue unavailable", "error_code", store.ImportErrorCode(err))
		}
		notices, e := a.store.PendingImportNotices(ctx, a.botInstanceID)
		if e != nil {
			continue
		}
		for _, notice := range notices {
			e = a.importBackgroundScreen(ctx, notice.ID, notice.UserID)
			// A timeout is uncertain. Keep the receipt accessible through /imports,
			// rather than repeatedly replacing a possibly delivered message.
			if e == nil || ctx.Err() == nil {
				_ = a.store.ImportNotified(ctx, notice.ID)
			}
		}
	}
}

// Used by legacy caption imports and provider-page imports as well as uploads.
func importServiceJSON(names []string) string { raw, _ := json.Marshal(names); return string(raw) }

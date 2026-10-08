package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
)

func (a *App) showSavedSelections(ctx context.Context, chat, user int64, watches bool, page int) {
	if watches && !a.ui.alertsEnabled {
		a.sendHTML(chat, "Availability alerts are temporarily paused.", userBackMenu())
		return
	}
	items, err := a.store.SavedSelections(ctx, a.botInstanceID, user, watches)
	if err != nil {
		a.sendError(chat, err)
		return
	}
	title, kind, icon := "Favorites", "favorites", "favorite"
	limit := 20
	if watches {
		title, kind, icon, limit = "Number Availability Alerts", "alerts", "bell", 10
	}
	text := premium.Emoji(icon, "⭐") + " <b>" + title + "</b>\n\n"
	if watches {
		text += "Receive an alert when eligible inventory becomes available. Alerts do not reserve numbers.\n\n"
	}
	if len(items) == 0 {
		text += "No saved selections yet. Choose an application and country below.\n"
	}
	start, end, p := pageBounds(page, len(items))
	rows := [][]premium.InlineButton{}
	for _, v := range items[start:end] {
		text += fmt.Sprintf("%s <b>%s</b> · %s\n", premium.AppEmoji(v.Service, "📱"), html.EscapeString(v.Service), html.EscapeString(v.Country))
		action := "use"
		if watches {
			action = "usewatch"
		}
		rows = append(rows, []premium.InlineButton{premium.Button(short(v.Service+" · "+v.Country, 50), fmt.Sprintf("tools:%s:%d", action, v.ID), "success", "phone"), premium.Button("Remove", fmt.Sprintf("tools:remove:%s:%d", kind, v.ID), "danger", "trash")})
	}
	fmtString := fmt.Sprintf("\n%d/%d saved · Page %d/%d", len(items), limit, p+1, max(1, (len(items)+9)/10))
	text += fmtString
	pager := []premium.InlineButton{}
	if p > 0 {
		pager = append(pager, premium.Button("Previous", fmt.Sprintf("tools:%s:%d", kind, p-1), "", "back"))
	}
	if end < len(items) {
		pager = append(pager, premium.Button("Next", fmt.Sprintf("tools:%s:%d", kind, p+1), "primary", "play"))
	}
	if len(pager) > 0 {
		rows = append(rows, pager)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Add Selection", "tools:pick:"+kind, "success", "add")}, []premium.InlineButton{premium.Button("Home", "menu:home", "", "home")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) selectionFromHashes(ctx context.Context, serviceKey, countryKey string) (store.SavedSelection, bool) {
	catalog, err := a.store.CatalogForInstance(ctx, a.botInstanceID)
	if err != nil {
		return store.SavedSelection{}, false
	}
	for _, service := range store.SortedServices(catalog) {
		if selectionKey(service) != serviceKey {
			continue
		}
		for _, country := range catalog[service] {
			if selectionKey(country.Country) == countryKey {
				return store.SavedSelection{Service: service, Country: country.Country}, true
			}
		}
	}
	return store.SavedSelection{}, false
}
func (a *App) showRepeatSelection(ctx context.Context, chat, user int64) {
	v, err := a.store.LastSelection(ctx, a.botInstanceID, user)
	if errors.Is(err, pgx.ErrNoRows) {
		a.sendHTML(chat, "Choose a number first to save your last selection.", servicesBackMenu())
		return
	}
	if err != nil {
		a.sendError(chat, err)
		return
	}
	if _, ok := a.selectionFromHashes(ctx, selectionKey(v.Service), selectionKey(v.Country)); !ok {
		a.sendHTML(chat, "Your last selection is no longer configured. Choose another application.", servicesBackMenu())
		return
	}
	a.sendHTML(chat, fmt.Sprintf("%s <b>Repeat Last Selection</b>\n\n%s · %s\n\nAcquire eligible numbers using the current inventory and cooldown rules?", premium.Emoji("refresh", "🔄"), html.EscapeString(v.Service), html.EscapeString(v.Country)), premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button("Get Number", "buy:country:"+selectionKey(v.Service)+":"+selectionKey(v.Country), "success", "phone")}, {premium.Button("Home", "menu:home", "", "home")}}})
}
func servicesBackMenu() premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button("Choose Application", "menu:services", "primary", "app")}, {premium.Button("Home", "menu:home", "", "home")}}}
}
func (a *App) handleUserToolsCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) bool {
	if a.handleDailyCallback(ctx, cb) {
		return true
	}
	if !strings.HasPrefix(cb.Data, "tools:") {
		return false
	}
	chat, user := cb.Message.Chat.ID, cb.From.ID
	p := strings.Split(cb.Data, ":")
	if len(p) < 2 {
		return true
	}
	if p[1] == "last" {
		a.showRepeatSelection(ctx, chat, user)
		return true
	}
	if (p[1] == "favorites" || p[1] == "alerts") && len(p) == 3 {
		page, _ := strconv.Atoi(p[2])
		a.showSavedSelections(ctx, chat, user, p[1] == "alerts", page)
		return true
	}
	if p[1] == "remove" && len(p) == 4 {
		id, err := strconv.ParseInt(p[3], 10, 64)
		if err == nil {
			err = a.store.RemoveSavedSelection(ctx, a.botInstanceID, user, id, p[2] == "alerts")
		}
		if err != nil {
			a.sendError(chat, err)
		} else {
			a.showSavedSelections(ctx, chat, user, p[2] == "alerts", 0)
		}
		return true
	}
	if (p[1] == "use" || p[1] == "usewatch") && len(p) == 3 {
		id, err := strconv.ParseInt(p[2], 10, 64)
		if err != nil {
			return true
		}
		items, err := a.store.SavedSelections(ctx, a.botInstanceID, user, p[1] == "usewatch")
		if err != nil {
			a.sendError(chat, err)
			return true
		}
		for _, v := range items {
			if v.ID == id {
				if _, ok := a.selectionFromHashes(ctx, selectionKey(v.Service), selectionKey(v.Country)); !ok {
					a.sendHTML(chat, "This selection is no longer configured.", servicesBackMenu())
					return true
				}
				a.handleGetNumber(ctx, &tgbotapi.Message{Chat: cb.Message.Chat, From: cb.From}, v.Service+"|"+v.Country)
				return true
			}
		}
		a.sendHTML(chat, "This saved selection has been removed.", userBackMenu())
		return true
	}
	if p[1] == "pick" && (len(p) == 3 || len(p) == 4) {
		kind := p[2]
		if kind != "favorites" && kind != "alerts" {
			return true
		}
		if kind == "alerts" && !a.ui.alertsEnabled {
			a.sendHTML(chat, "Alerts are temporarily paused.", userBackMenu())
			return true
		}
		catalog, err := a.store.CatalogForInstance(ctx, a.botInstanceID)
		if err != nil {
			a.sendError(chat, err)
			return true
		}
		rows := [][]premium.InlineButton{}
		for _, service := range store.SortedServices(catalog) {
			if len(p) == 3 {
				rows = append(rows, []premium.InlineButton{premium.Button(service, "tools:pick:"+kind+":"+selectionKey(service), "primary", "app")})
				continue
			}
			if selectionKey(service) == p[3] {
				for _, v := range catalog[service] {
					action := "add"
					if kind == "alerts" {
						action = "watch"
					}
					rows = append(rows, []premium.InlineButton{premium.Button(v.Country, "tools:"+action+":"+selectionKey(service)+":"+selectionKey(v.Country), "primary", "globe")})
				}
			}
		}
		rows = append(rows, []premium.InlineButton{premium.Button("Back", "tools:"+kind+":0", "", "back")})
		a.sendHTML(chat, "Choose a selection to save.", premium.InlineKeyboard{InlineKeyboard: rows})
		return true
	}
	if (p[1] == "add" || p[1] == "watch") && len(p) == 4 {
		watches := p[1] == "watch"
		if watches && !a.ui.alertsEnabled {
			a.sendHTML(chat, "Alerts are temporarily paused.", userBackMenu())
			return true
		}
		v, ok := a.selectionFromHashes(ctx, p[2], p[3])
		if !ok {
			a.sendHTML(chat, "Inventory changed. Choose an application again.", servicesBackMenu())
			return true
		}
		if _, err := a.store.AddSavedSelection(ctx, a.botInstanceID, user, v.Service, v.Country, watches); err != nil {
			a.sendHTML(chat, html.EscapeString(err.Error()), userBackMenu())
			return true
		}
		a.showSavedSelections(ctx, chat, user, watches, 0)
		return true
	}
	return true
}

func (a *App) deliverAvailabilityNotification(ctx context.Context) {
	v, err := a.store.ClaimAvailabilityNotification(ctx, a.botInstanceID)
	if errors.Is(err, pgx.ErrNoRows) || ctx.Err() != nil {
		return
	}
	if err != nil {
		slog.Warn("claim availability alert failed", "error_type", fmt.Sprintf("%T", err))
		return
	}
	ok, err := a.store.AvailabilityNotificationEligible(ctx, a.botInstanceID, v)
	if err != nil {
		_ = a.store.FinishAvailabilityNotification(ctx, v.ID, "pending", time.Minute)
		return
	}
	if !ok {
		_ = a.store.FinishAvailabilityNotification(ctx, v.ID, "skipped", 0)
		return
	}
	prefs, prefErr := a.store.NotificationPreferences(ctx, a.botInstanceID, v.UserID)
	if prefErr != nil || !prefs.Allowed(time.Now()) {
		state := "pending"
		if prefErr == nil && !prefs.Enabled {
			state = "skipped"
		}
		_ = a.store.FinishAvailabilityNotification(ctx, v.ID, state, time.Minute)
		return
	}
	missing, err := a.missingRequiredChats(ctx, v.UserID)
	if err != nil {
		_ = a.store.FinishAvailabilityNotification(ctx, v.ID, "pending", time.Minute)
		return
	}
	if len(missing) > 0 {
		_ = a.store.FinishAvailabilityNotification(ctx, v.ID, "skipped", 0)
		return
	}
	text := premium.AnimateHTML(fmt.Sprintf("🔔 <b>Numbers available</b>\n\n%s · %s\nEligible inventory is available now. Acquire a number to confirm availability.", html.EscapeString(v.Service), html.EscapeString(v.Country)))
	p := tgbotapi.Params{"chat_id": fmt.Sprint(v.UserID), "text": text, "parse_mode": "HTML"}
	_ = p.AddInterface("reply_markup", premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button("Get Number", "buy:country:"+selectionKey(v.Service)+":"+selectionKey(v.Country), "success", "phone")}, {premium.Button("Manage Alerts", "tools:alerts:0", "primary", "bell")}}})
	_, err = a.bot.MakeRequest("sendMessage", p)
	state, delay := "sent", time.Duration(0)
	if err != nil {
		state = "uncertain"
		if api, ok := tgtransport.APIError(err); ok {
			state = "failed"
			if api.Code == 403 {
				state = "skipped"
				_ = a.store.RemoveSavedSelection(ctx, a.botInstanceID, v.UserID, v.WatchID, true)
			}
			if api.Code == 429 {
				state = "pending"
				delay = time.Duration(max(1, api.RetryAfter)) * time.Second
			}
		}
	}
	_ = a.store.FinishAvailabilityNotification(ctx, v.ID, state, delay)
}

package telegram

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (a *App) handleDailyCommand(ctx context.Context, m *tgbotapi.Message, command, args string) bool {
	switch command {
	case "mynumbers":
		a.showOwnedNumbers(ctx, m.Chat.ID, m.From.ID, 0)
	case "search":
		a.searchCatalog(ctx, m.Chat.ID, args)
	case "notifications":
		if fields := strings.Fields(args); len(fields) == 3 && fields[0] == "quiet" {
			start, e := strconv.Atoi(fields[1])
			end, e2 := strconv.Atoi(fields[2])
			pref, e3 := a.store.NotificationPreferences(ctx, a.botInstanceID, m.From.ID)
			if e == nil && e2 == nil && e3 == nil {
				e = a.store.SetNotificationPreferences(ctx, a.botInstanceID, m.From.ID, pref.Enabled, &start, &end)
			} else {
				e = fmt.Errorf("use /notifications quiet 22 8")
			}
			if e != nil {
				a.sendHTML(m.Chat.ID, html.EscapeString(e.Error()), userBackMenu())
				return true
			}
		}
		a.showNotifications(ctx, m.Chat.ID, m.From.ID)
	default:
		return false
	}
	return true
}
func (a *App) showOwnedNumbers(ctx context.Context, chat, user int64, page int) {
	items, e := a.store.OwnedNumbers(ctx, a.botInstanceID, user)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	start, end, page := pageBounds(page, len(items))
	text := "📱 <b>My Numbers</b>\n\n"
	rows := [][]premium.InlineButton{}
	if len(items) == 0 {
		text += "No recent assignments. Choose an app to get started.\n"
	}
	for _, v := range items[start:end] {
		text += fmt.Sprintf("%s <b>%s</b> · %s\n<code>%s</code> · %s\nExpires: %s\n\n", premium.AppEmoji(v.Service, "📱"), html.EscapeString(v.Service), html.EscapeString(v.Country), html.EscapeString(v.Phone), v.State, v.ExpiresAt.In(a.location).Format("15:04:05"))
		if v.State == "pending" {
			rows = append(rows, []premium.InlineButton{premium.Button("Release "+v.Service+" · "+v.Phone, fmt.Sprintf("tools:release:%d:%s", v.ID, selectionKey(v.AssignmentID)), "danger", "trash")})
		}
	}
	pager := []premium.InlineButton{}
	if page > 0 {
		pager = append(pager, premium.Button("Previous", fmt.Sprintf("tools:numbers:%d", page-1), "primary", "back"))
	}
	if end < len(items) {
		pager = append(pager, premium.Button("Next", fmt.Sprintf("tools:numbers:%d", page+1), "primary", "play"))
	}
	if len(pager) > 0 {
		rows = append(rows, pager)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Get Number", "menu:services", "success", "phone"), premium.Button("OTP History", "tools:history:24h:all:0", "primary", "history")}, []premium.InlineButton{premium.Button("Home", "menu:home", "primary", "home")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) searchCatalog(ctx context.Context, chat int64, query string) {
	query = strings.TrimSpace(query)
	catalog, e := a.store.CatalogForInstance(ctx, a.botInstanceID)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	text := "🔎 <b>Search Apps &amp; Countries</b>\n\n"
	if query == "" {
		text += "Use <code>/search WhatsApp</code> or <code>/search Pakistan</code>.\n\n"
	}
	needle := strings.ToLower(query)
	rows := [][]premium.InlineButton{}
	matches := 0
	for _, service := range store.SortedServices(catalog) {
		for _, c := range catalog[service] {
			if needle != "" && !strings.Contains(strings.ToLower(service), needle) && !strings.Contains(strings.ToLower(c.Country), needle) {
				continue
			}
			matches++
			if len(rows) >= 20 {
				continue
			}
			rows = append(rows, []premium.InlineButton{{Text: short(fmt.Sprintf("%s · %s · %d available", service, c.Country, c.Available), 60), CallbackData: "buy:country:" + selectionKey(service) + ":" + selectionKey(c.Country), Style: "success", IconCustomEmojiID: catalogServiceEmojiID(service, catalog[service])}})
		}
	}
	text += fmt.Sprintf("%d matching selections. Showing up to 20; refine your search for more.\n", matches)
	rows = append(rows, []premium.InlineButton{premium.Button("All Apps", "menu:services", "primary", "app"), premium.Button("Home", "menu:home", "primary", "home")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) showNotifications(ctx context.Context, chat, user int64) {
	pref, e := a.store.NotificationPreferences(ctx, a.botInstanceID, user)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	quiet := "Off"
	if pref.Start != nil {
		quiet = fmt.Sprintf("%02d:00–%02d:00", *pref.Start, *pref.End)
	}
	toggle := "1"
	label := "Enable Availability Alerts"
	style := "success"
	if pref.Enabled {
		toggle = "0"
		label = "Pause Availability Alerts"
		style = "danger"
	}
	text := fmt.Sprintf("🔔 <b>Notification Preferences</b>\n\nAvailability alerts: <b>%s</b>\nQuiet hours: <b>%s</b>\nTimezone: <b>%s</b>\n\nOTP delivery stays immediate in private chat. Quiet hours apply to availability alerts.\nCustom hours: <code>/notifications quiet 22 8</code>", onOff(pref.Enabled), quiet, html.EscapeString(pref.Zone))
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button(label, "tools:notify:toggle:"+toggle, style, "bell")}, {premium.Button("Quiet 22:00–08:00", "tools:notify:quiet:22:8", "primary", "clock"), premium.Button("Quiet Hours Off", "tools:notify:off", "primary", "settings")}, {premium.Button("Manage Alerts", "tools:alerts:0", "primary", "bell"), premium.Button("Home", "menu:home", "primary", "home")}}})
}
func (a *App) showFilteredHistory(ctx context.Context, chat, user int64, period, key string, page int) {
	hours := 24
	if period == "7d" {
		hours = 24 * 7
	} else if period == "30d" {
		hours = 24 * 30
	} else {
		period = "24h"
	}
	service := ""
	catalog, e := a.store.CatalogForInstance(ctx, a.botInstanceID)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	if key != "all" {
		for _, v := range store.SortedServices(catalog) {
			if selectionKey(v) == key {
				service = v
			}
		}
		if service == "" {
			a.sendHTML(chat, "This application is no longer configured.", userBackMenu())
			return
		}
	}
	page = max(page, 0)
	items, e := a.store.FilteredHistory(ctx, a.botInstanceID, user, service, time.Now().Add(-time.Duration(hours)*time.Hour), 10, page*10)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	text := "🕓 <b>Filtered OTP History</b>\n\nPeriod: " + period + " · App: " + html.EscapeString(service) + "\n\n"
	rows := [][]premium.InlineButton{}
	for _, v := range items {
		text += fmt.Sprintf("%s <b>%s</b> · %s\n<code>%s</code> · <code>%s</code>\n%s\n\n", premium.AppEmoji(v.Service, "📱"), html.EscapeString(v.Service), html.EscapeString(v.Country), html.EscapeString(v.Phone), html.EscapeString(v.Code), v.ReceivedAt.In(a.location).Format("02 Jan 15:04"))
	}
	if len(items) == 0 {
		text += "No matching received OTPs.\n"
	}
	row := []premium.InlineButton{}
	for _, p := range []string{"24h", "7d", "30d"} {
		row = append(row, premium.Button(p, "tools:history:"+p+":"+key+":0", activeStyle(p == period), "calendar"))
	}
	rows = append(rows, row)
	rows = append(rows, []premium.InlineButton{premium.Button("All Apps", "tools:history:"+period+":all:0", "primary", "app")})
	for _, v := range store.SortedServices(catalog) {
		if len(rows) > 12 {
			break
		}
		rows = append(rows, []premium.InlineButton{premium.Button(v, "tools:history:"+period+":"+selectionKey(v)+":0", activeStyle(v == service), "app")})
	}
	row = nil
	if page > 0 {
		row = append(row, premium.Button("Previous", fmt.Sprintf("tools:history:%s:%s:%d", period, key, page-1), "primary", "back"))
	}
	if len(items) == 10 {
		row = append(row, premium.Button("Next", fmt.Sprintf("tools:history:%s:%s:%d", period, key, page+1), "primary", "play"))
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Home", "menu:home", "primary", "home")})
	a.sendHTML(chat, text, premium.InlineKeyboard{InlineKeyboard: rows})
}
func (a *App) handleDailyCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) bool {
	p := strings.Split(cb.Data, ":")
	chat, user := cb.Message.Chat.ID, cb.From.ID
	if len(p) < 2 || p[0] != "tools" {
		return false
	}
	switch p[1] {
	case "search":
		a.searchCatalog(ctx, chat, "")
	case "numbers":
		page := 0
		if len(p) == 3 {
			page, _ = strconv.Atoi(p[2])
		}
		a.showOwnedNumbers(ctx, chat, user, page)
	case "notifications":
		a.showNotifications(ctx, chat, user)
	case "notify":
		pref, e := a.store.NotificationPreferences(ctx, a.botInstanceID, user)
		if e != nil {
			a.sendError(chat, e)
			return true
		}
		if len(p) == 4 && p[2] == "toggle" {
			pref.Enabled = p[3] == "1"
		} else if len(p) == 5 && p[2] == "quiet" {
			start, e := strconv.Atoi(p[3])
			end, e2 := strconv.Atoi(p[4])
			if e != nil || e2 != nil {
				return true
			}
			pref.Start, pref.End = &start, &end
		} else if len(p) == 3 && p[2] == "off" {
			pref.Start, pref.End = nil, nil
		} else {
			return true
		}
		if e = a.store.SetNotificationPreferences(ctx, a.botInstanceID, user, pref.Enabled, pref.Start, pref.End); e != nil {
			a.sendError(chat, e)
		} else {
			a.showNotifications(ctx, chat, user)
		}
	case "release", "releaseyes":
		if len(p) != 4 {
			return true
		}
		id, e := strconv.ParseInt(p[2], 10, 64)
		if e != nil {
			return true
		}
		items, e := a.store.OwnedNumbers(ctx, a.botInstanceID, user)
		if e != nil {
			a.sendError(chat, e)
			return true
		}
		var selected *store.OwnedNumber
		for i := range items {
			if items[i].ID == id && selectionKey(items[i].AssignmentID) == p[3] {
				selected = &items[i]
				break
			}
		}
		if selected == nil {
			a.sendHTML(chat, "This assignment is no longer available.", userBackMenu())
			return true
		}
		if p[1] == "release" {
			a.sendHTML(chat, fmt.Sprintf("Release <code>%s</code> for <b>%s</b>?\n\nOnly this app's allocation will be released. Your reuse cooldown still applies.", selected.Phone, html.EscapeString(selected.Service)), premium.InlineKeyboard{InlineKeyboard: [][]premium.InlineButton{{premium.Button("Release Number", "tools:releaseyes:"+p[2]+":"+p[3], "danger", "trash"), premium.Button("Back", "tools:numbers:0", "primary", "back")}}})
			return true
		}
		if e = a.store.ReleaseNumber(ctx, a.botInstanceID, user, selected.AssignmentID, id, a.reuseCooldown); e != nil {
			a.sendHTML(chat, html.EscapeString(e.Error()), userBackMenu())
		} else {
			a.showOwnedNumbers(ctx, chat, user, 0)
		}
	case "history":
		if len(p) != 5 {
			return true
		}
		page, _ := strconv.Atoi(p[4])
		a.showFilteredHistory(ctx, chat, user, p[2], p[3], page)
	default:
		return false
	}
	return true
}

package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/country"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type liveSession struct {
	User, Chat             int64
	Message                int
	View, Period           string
	Page                   int
	Paused                 bool
	Expires, CheckedAccess time.Time
	Hash                   string
}
type cachedActivity struct {
	Snapshot store.ActivitySnapshot
	Expires  time.Time
}
type uiRuntime struct {
	mu                                      sync.Mutex
	users                                   sync.Map
	sessions                                map[int64]liveSession
	activity                                map[string]cachedActivity
	commandHashes                           map[int64]string
	groupCommandHashes                      map[string]string
	richUnsupported                         bool
	richEnabled, liveEnabled, alertsEnabled bool
}

func newUIRuntime() *uiRuntime {
	return &uiRuntime{
		sessions: map[int64]liveSession{}, activity: map[string]cachedActivity{}, commandHashes: map[int64]string{},
		richEnabled: os.Getenv("TELEGRAM_RICH_SCREENS") != "false", liveEnabled: os.Getenv("TELEGRAM_LIVE_REFRESH") != "false", alertsEnabled: os.Getenv("TELEGRAM_AVAILABILITY_ALERTS") != "false",
	}
}
func (a *App) userLock(user int64) *sync.Mutex {
	v, _ := a.ui.users.LoadOrStore(user, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func updateUser(update tgbotapi.Update) int64 {
	if update.CallbackQuery != nil && update.CallbackQuery.From != nil {
		return update.CallbackQuery.From.ID
	}
	if update.Message != nil && update.Message.From != nil {
		return update.Message.From.ID
	}
	return 0
}
func normalizePeriod(period string) string {
	switch period {
	case "1h", "24h", "7d", "30d":
		return period
	}
	return "24h"
}
func (a *App) activitySnapshot(ctx context.Context, period string) (store.ActivitySnapshot, error) {
	period = normalizePeriod(period)
	a.ui.mu.Lock()
	defer a.ui.mu.Unlock()
	if cached, ok := a.ui.activity[period]; ok && time.Now().Before(cached.Expires) {
		return cached.Snapshot, nil
	}
	v, err := a.store.Activity(ctx, a.botInstanceID, period)
	if err == nil {
		a.ui.activity[period] = cachedActivity{v, time.Now().Add(5 * time.Second)}
	}
	return v, err
}
func (a *App) pauseLive(ctx context.Context, user int64) {
	a.ui.mu.Lock()
	session, ok := a.ui.sessions[user]
	if ok {
		session.Paused = true
		a.ui.sessions[user] = session
	}
	a.ui.mu.Unlock()
	if ok {
		_ = a.store.SaveLiveScreen(ctx, a.botInstanceID, user, session.Message, session.View, session.Period, session.Page, true, session.Expires)
	}
}
func (a *App) updatePreferences(ctx context.Context, user int64) {
	if a.store == nil {
		return
	}
	pref, err := a.store.Preference(ctx, a.botInstanceID, user)
	if err == nil {
		a.displayFormat = pref.DisplayFormat
		if loc, e := time.LoadLocation(pref.Timezone); e == nil {
			a.location = loc
		}
	}
}
func (a *App) startLive(ctx context.Context, chat, user int64, view, period string, page int, paused bool) {
	if a.group != nil {
		if view == "private" {
			a.privateHandoff(chat, "liveotp")
			return
		}
		s := liveSession{User: user, Chat: chat, View: view, Period: normalizePeriod(period), Page: page, Paused: true}
		doc, e := a.liveDocument(ctx, s)
		if e != nil {
			a.sendError(chat, e)
		} else {
			rows := [][]premium.InlineButton{}
			for _, row := range doc.Keyboard.InlineKeyboard {
				buttons := []premium.InlineButton{}
				for _, b := range row {
					if b.CallbackData == "live:pause" || b.CallbackData == "live:resume" {
						continue
					}
					if b.CallbackData == "live:refresh" {
						b.CallbackData = fmt.Sprintf("activity:%s:%s:%d", view, s.Period, page)
					}
					buttons = append(buttons, b)
				}
				if len(buttons) > 0 {
					rows = append(rows, buttons)
				}
			}
			doc.Keyboard.InlineKeyboard = rows
			a.sendDocument(chat, doc)
		}
		return
	}
	if page < 0 {
		page = 0
	}
	session := liveSession{User: user, Chat: chat, View: view, Period: normalizePeriod(period), Page: page, Paused: paused || !a.ui.liveEnabled, Expires: time.Now().Add(10 * time.Minute), CheckedAccess: time.Now()}
	if err := a.refreshLive(ctx, &session, true); err != nil {
		a.sendError(chat, err)
		return
	}
	a.ui.mu.Lock()
	a.ui.sessions[user] = session
	a.ui.mu.Unlock()
}
func (a *App) refreshLive(ctx context.Context, session *liveSession, force bool) error {
	doc, err := a.liveDocument(ctx, *session)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(doc)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if !force && hash == session.Hash {
		return nil
	}
	if session.Message != 0 {
		a.screenChatID, a.screenMessageID = session.Chat, session.Message
	}
	stamp := time.Now().In(a.location).Format("02 Jan 15:04:05 MST")
	doc.ClassicHTML += "\n\nUpdated: " + stamp
	doc.RichHTML += "<footer>Updated: " + stamp + "</footer>"
	if err = a.renderDocument(session.Chat, doc); err != nil {
		return err
	}
	if a.lastMessageID != 0 {
		session.Message = a.lastMessageID
	}
	session.Hash = hash
	return a.store.SaveLiveScreen(ctx, a.botInstanceID, session.User, session.Message, session.View, session.Period, session.Page, session.Paused, session.Expires)
}
func (a *App) handleActivityCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) bool {
	d := cb.Data
	if d == "menu:liveotp" || d == "menu:myotps" {
		a.startLive(ctx, cb.Message.Chat.ID, cb.From.ID, "private", "24h", 0, false)
		return true
	}
	if !strings.HasPrefix(d, "live:") && !strings.HasPrefix(d, "activity:") {
		return false
	}
	parts := strings.Split(d, ":")
	if parts[0] == "activity" && len(parts) == 4 {
		page, _ := strconv.Atoi(parts[3])
		a.startLive(ctx, cb.Message.Chat.ID, cb.From.ID, parts[1], parts[2], page, false)
		return true
	}
	if len(parts) == 2 {
		a.ui.mu.Lock()
		session, ok := a.ui.sessions[cb.From.ID]
		a.ui.mu.Unlock()
		if !ok {
			if stored, err := a.store.LiveScreen(ctx, a.botInstanceID, cb.From.ID); err == nil {
				session = liveSession{User: cb.From.ID, Chat: cb.Message.Chat.ID, Message: stored.Message, View: stored.View, Period: stored.Period, Page: stored.Page, Expires: stored.Expires, Paused: true, CheckedAccess: time.Now()}
				ok = true
			}
		}
		if !ok || session.Message != cb.Message.MessageID {
			a.sendHTML(cb.Message.Chat.ID, "This live screen expired. Open Live OTP to start again.", userBackMenu())
			return true
		}
		switch parts[1] {
		case "pause":
			session.Paused = true
		case "resume":
			session.Paused = !a.ui.liveEnabled
			session.Expires = time.Now().Add(10 * time.Minute)
		case "refresh":
			session.Expires = time.Now().Add(10 * time.Minute)
		default:
			return true
		}
		if err := a.refreshLive(ctx, &session, true); err != nil {
			a.sendError(session.Chat, err)
			return true
		}
		a.ui.mu.Lock()
		a.ui.sessions[session.User] = session
		a.ui.mu.Unlock()
		return true
	}
	if len(parts) == 4 {
		page, err := strconv.Atoi(parts[3])
		if err != nil || page < 0 {
			return true
		}
		view := parts[1]
		if view != "private" && view != "countries" && view != "apps" && view != "topcountries" && !strings.HasPrefix(view, "c") {
			return true
		}
		a.startLive(ctx, cb.Message.Chat.ID, cb.From.ID, view, parts[2], page, false)
	}
	return true
}
func liveCallback(view, period string, page int) string {
	return fmt.Sprintf("live:%s:%s:%d", view, period, page)
}
func pageBounds(page, total int) (int, int, int) {
	pages := max(1, (total+9)/10)
	page = max(0, min(page, pages-1))
	return page * 10, min(total, (page+1)*10), page
}

func (a *App) liveDocument(ctx context.Context, session liveSession) (screenDocument, error) {
	snapshot, err := a.activitySnapshot(ctx, session.Period)
	if err != nil {
		return screenDocument{}, err
	}
	appIcon := func(service string) string {
		for _, app := range snapshot.Apps {
			if app.Key == premium.AppKey(service) {
				return premium.CustomEmoji(app.EmojiID, "📱")
			}
		}
		return premium.AppEmoji(service, "📱")
	}
	var classic, rich strings.Builder
	title := "Live OTP"
	icon := "live"
	switch session.View {
	case "apps":
		title = "Top Apps"
		icon = "ranking"
	case "countries":
		title = "All Countries"
		icon = "globe"
	case "topcountries":
		title = "Top Countries"
		icon = "ranking"
	}
	fmt.Fprintf(&classic, "%s <b>%s</b>\n\nPeriod: <b>%s</b> · Received OTPs: <b>%d</b>\n", premium.Emoji(icon, "📊"), title, session.Period, snapshot.Total)
	fmt.Fprintf(&rich, "<h2>%s %s</h2><p>Period: <b>%s</b> · Received OTPs: <b>%d</b></p>", premium.Emoji(icon, "📊"), title, session.Period, snapshot.Total)
	rows := [][]premium.InlineButton{}
	pages, total, page := 1, 0, session.Page
	if session.View == "private" {
		assignments, e := a.store.LiveAssignments(ctx, a.botInstanceID, session.User)
		if e != nil {
			return screenDocument{}, e
		}
		history, e := a.store.OTPHistory(ctx, a.botInstanceID, session.User, 5, 0)
		if e != nil {
			return screenDocument{}, e
		}
		classic.WriteString("\n<b>Your assignments</b>\n")
		rich.WriteString("<h3>Your assignments</h3>")
		if len(assignments) == 0 {
			classic.WriteString("No recent assignments. Get a number to begin.\n")
			rich.WriteString("<p>No recent assignments. Get a number to begin.</p>")
		}
		for _, v := range assignments {
			line := fmt.Sprintf("%s %s · <code>+%s</code> · <b>%s</b> · %s", appIcon(v.Service), html.EscapeString(v.Service), html.EscapeString(v.Phone), v.State, v.ExpiresAt.In(a.location).Format("15:04 MST"))
			classic.WriteString(line + "\n")
			rich.WriteString("<p>" + line + "</p>")
		}
		classic.WriteString("\n<b>Your recent OTPs</b>\n")
		rich.WriteString("<h3>Your recent OTPs</h3>")
		if len(history) == 0 {
			classic.WriteString("Waiting for your first OTP.\n")
			rich.WriteString("<p>Waiting for your first OTP.</p>")
		}
		for _, v := range history {
			line := fmt.Sprintf("%s %s · <code>+%s</code>\nOTP: <code>%s</code> · %s", appIcon(v.Service), html.EscapeString(v.Service), html.EscapeString(v.Phone), html.EscapeString(v.Code), v.ReceivedAt.In(a.location).Format("02 Jan 15:04"))
			classic.WriteString(line + "\n")
			rich.WriteString("<p>" + strings.ReplaceAll(line, "\n", "<br>") + "</p>")
			if v.Code != "" && len(v.Code) <= 256 {
				rows = append(rows, []premium.InlineButton{{Text: "Copy " + v.Service + " OTP", CopyText: &premium.CopyText{Text: v.Code}, Style: "success", IconCustomEmojiID: premium.ID("copy")}})
			}
		}
		rows = append(rows, []premium.InlineButton{premium.Button("Get Number", "menu:services", "success", "phone")})
	} else {
		countryView := strings.HasPrefix(session.View, "c") && session.View != "countries"
		var selected *store.ActivityCountry
		if countryView {
			for i := range snapshot.Countries {
				if "c"+selectionKey(snapshot.Countries[i].Key) == session.View {
					selected = &snapshot.Countries[i]
					break
				}
			}
			if selected == nil {
				return screenDocument{}, errors.New("country no longer exists")
			}
		}
		if session.View == "apps" || countryView {
			apps := append([]store.ActivityApp(nil), snapshot.Apps...)
			if selected != nil {
				classic.WriteString("\n<b>" + html.EscapeString(selected.Name) + " · All Apps</b>\n")
				rich.WriteString("<h3>" + html.EscapeString(selected.Name) + " · All Apps</h3>")
				for i := range apps {
					apps[i].Count = selected.Apps[apps[i].Key]
				}
			}
			sort.SliceStable(apps, func(i, j int) bool { return apps[i].Count > apps[j].Count })
			total = len(apps)
			start, end, p := pageBounds(page, total)
			page = p
			rich.WriteString("<table striped compact><tr><th>Rank</th><th>Application</th><th>OTPs</th></tr>")
			for i := start; i < end; i++ {
				v := apps[i]
				label := premium.CustomEmoji(v.EmojiID, "📱") + " " + html.EscapeString(v.Name)
				fmt.Fprintf(&classic, "\n%d. %s — <b>%d</b>", i+1, label, v.Count)
				fmt.Fprintf(&rich, "<tr><td>%d</td><td>%s</td><td>%d</td></tr>", i+1, label, v.Count)
			}
			rich.WriteString("</table>")
		} else {
			countries := append([]store.ActivityCountry(nil), snapshot.Countries...)
			if session.View == "topcountries" {
				sort.SliceStable(countries, func(i, j int) bool { return countries[i].Count > countries[j].Count })
			}
			total = len(countries)
			start, end, p := pageBounds(page, total)
			page = p
			rich.WriteString("<table striped compact><tr><th>Country</th><th>OTPs</th><th>Apps</th></tr>")
			for i := start; i < end; i++ {
				v := countries[i]
				label := premium.CountryFlag(v.Code, country.Flag(v.Code)) + " " + html.EscapeString(v.Name)
				preview := countryAppPreview(v, snapshot.Apps)
				fmt.Fprintf(&classic, "\n%s — <b>%d</b>\n%s\n", label, v.Count, preview)
				fmt.Fprintf(&rich, "<tr><td>%s</td><td>%d</td><td>%s</td></tr>", label, v.Count, preview)
				rows = append(rows, []premium.InlineButton{premium.Button(v.Name+" · Apps", liveCallback("c"+selectionKey(v.Key), session.Period, 0), "primary", "app")})
			}
			rich.WriteString("</table>")
		}
		if total == 0 {
			classic.WriteString("\nNo configured or received entries yet.")
			rich.WriteString("<p>No configured or received entries yet.</p>")
		}
		pages = max(1, (total+9)/10)
		classic.WriteString(fmt.Sprintf("\n\nPage %d/%d · %d entries", page+1, pages, total))
		fmt.Fprintf(&rich, "<p>Page %d/%d · %d entries</p>", page+1, pages, total)
		pager := []premium.InlineButton{}
		if page > 0 {
			pager = append(pager, premium.Button("Previous", liveCallback(session.View, session.Period, page-1), "", "back"))
		}
		if page+1 < pages {
			pager = append(pager, premium.Button("Next", liveCallback(session.View, session.Period, page+1), "primary", "play"))
		}
		if len(pager) > 0 {
			rows = append(rows, pager)
		}
	}
	rows = append(rows,
		[]premium.InlineButton{premium.Button("My OTPs", liveCallback("private", session.Period, 0), activeStyle(session.View == "private"), "otp"), premium.Button("All Countries", liveCallback("countries", session.Period, 0), activeStyle(session.View == "countries"), "globe")},
		[]premium.InlineButton{premium.Button("Top Apps", liveCallback("apps", session.Period, 0), activeStyle(session.View == "apps"), "app"), premium.Button("Top Countries", liveCallback("topcountries", session.Period, 0), activeStyle(session.View == "topcountries"), "ranking")})
	for _, periods := range [][]string{{"1h", "24h"}, {"7d", "30d"}} {
		row := []premium.InlineButton{}
		for _, period := range periods {
			label := period
			if period == session.Period {
				label = "Selected · " + period
			}
			row = append(row, premium.Button(label, liveCallback(session.View, period, 0), activeStyle(period == session.Period), "clock"))
		}
		rows = append(rows, row)
	}
	control := premium.Button("Pause", "live:pause", "danger", "stop")
	status := "Auto refresh · pauses after 10 minutes"
	if session.Paused {
		control = premium.Button("Resume", "live:resume", "success", "play")
		status = "Paused · tap Resume or Refresh"
	}
	if a.group != nil {
		status = "Private group view · tap Refresh to update"
	}
	if !a.ui.liveEnabled {
		status = "Manual refresh"
		rows = append(rows, []premium.InlineButton{premium.Button("Refresh", "live:refresh", "primary", "refresh")})
	} else {
		rows = append(rows, []premium.InlineButton{premium.Button("Refresh", "live:refresh", "primary", "refresh"), control})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Home", "menu:home", "", "home")})
	classic.WriteString("\n\n" + status + "\nActivity is anonymous; personal OTPs are private.")
	rich.WriteString("<footer>" + status + ". Activity is anonymous; personal OTPs are private.</footer>")
	return screenDocument{RichHTML: rich.String(), ClassicHTML: classic.String(), Keyboard: premium.InlineKeyboard{InlineKeyboard: rows}}, nil
}
func countryAppPreview(c store.ActivityCountry, apps []store.ActivityApp) string {
	items := append([]store.ActivityApp(nil), apps...)
	sort.SliceStable(items, func(i, j int) bool { return c.Apps[items[i].Key] > c.Apps[items[j].Key] })
	var labels []string
	count := 0
	for _, v := range items {
		if c.Apps[v.Key] == 0 {
			continue
		}
		count++
		if len(labels) < 3 {
			labels = append(labels, premium.CustomEmoji(v.EmojiID, "📱")+" "+html.EscapeString(v.Name))
		}
	}
	if count == 0 {
		return "No OTPs"
	}
	if count > 3 {
		labels = append(labels, fmt.Sprintf("+%d", count-3))
	}
	return strings.Join(labels, " · ")
}

func (a *App) runLiveScreens(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	commandsAt := time.Now()
	alertsAt := time.Now().Add(-time.Minute)
	for {
		select {
		case <-ctx.Done():
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = a.store.PauseLiveScreens(cleanup, a.botInstanceID)
			cancel()
			return
		case <-ticker.C:
		}
		if time.Since(commandsAt) >= time.Minute {
			_ = a.registerCommands(ctx)
			commandsAt = time.Now()
		}
		if a.ui.alertsEnabled && time.Since(alertsAt) >= 30*time.Second {
			if err := a.store.CheckAvailabilityWatches(ctx, a.botInstanceID); err != nil {
				slog.Warn("availability check failed", "error_type", fmt.Sprintf("%T", err))
			}
			alertsAt = time.Now()
		}
		if a.ui.alertsEnabled {
			a.deliverAvailabilityNotification(ctx)
		}
		a.ui.mu.Lock()
		sessions := make([]liveSession, 0, len(a.ui.sessions))
		for user, session := range a.ui.sessions {
			if session.Paused && time.Now().After(session.Expires.Add(time.Hour)) {
				delete(a.ui.sessions, user)
				continue
			}
			if !session.Paused {
				sessions = append(sessions, session)
			}
		}
		a.ui.mu.Unlock()
		for _, session := range sessions {
			lock := a.userLock(session.User)
			lock.Lock()
			a.ui.mu.Lock()
			current := a.ui.sessions[session.User]
			a.ui.mu.Unlock()
			if current.Paused || current.Message != session.Message {
				lock.Unlock()
				continue
			}
			session = current
			request := *a
			request.updatePreferences(ctx, session.User)
			banned, err := a.store.UserBlocked(ctx, session.User)
			if err != nil || banned {
				a.pauseLive(ctx, session.User)
				lock.Unlock()
				continue
			}
			if time.Since(session.CheckedAccess) >= 30*time.Second {
				missing, e := a.missingRequiredChats(ctx, session.User)
				if e != nil || len(missing) > 0 {
					a.pauseLive(ctx, session.User)
					lock.Unlock()
					continue
				}
				session.CheckedAccess = time.Now()
			}
			if time.Now().After(session.Expires) {
				session.Paused = true
			}
			if err = request.refreshLive(ctx, &session, false); err != nil {
				if api, ok := tgtransport.APIError(err); ok && (api.Code == 403 || api.Code == 400) {
					session.Paused = true
				}
				slog.Warn("live screen refresh failed", "bot_instance", a.botInstanceID, "error_type", fmt.Sprintf("%T", err))
			}
			a.ui.mu.Lock()
			a.ui.sessions[session.User] = session
			a.ui.mu.Unlock()
			lock.Unlock()
		}
	}
}

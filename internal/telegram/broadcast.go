package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/adnan-dogar/cracksms-vnext/internal/broadcast"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"strconv"
	"strings"
)

func broadcastKey() string { var n [8]byte; _, _ = rand.Read(n[:]); return hex.EncodeToString(n[:]) }
func (a *App) handleBroadcastMessage(ctx context.Context, m *tgbotapi.Message) bool {
	if m.From == nil || m.IsCommand() {
		return false
	}
	f, e := a.store.TelegramFlow(ctx, a.botInstanceID, m.From.ID)
	if e != nil || f.Kind != "broadcast" || f.Step != "content" {
		return false
	}
	ok, e := a.store.HasAdminPermission(ctx, a.botInstanceID, m.From.ID, "broadcast")
	if e != nil || !ok {
		return true
	}
	b := store.Broadcast{Kind: "text", Body: m.Text}
	entities := originalEntities(ctx, m, false)
	if len(m.Photo) > 0 {
		b.Kind = "photo"
		b.FileID = m.Photo[len(m.Photo)-1].FileID
		b.Body = m.Caption
		entities = originalEntities(ctx, m, true)
	} else if m.Video != nil {
		b.Kind = "video"
		b.FileID = m.Video.FileID
		b.Body = m.Caption
		entities = originalEntities(ctx, m, true)
	} else if m.Text == "" {
		a.sendHTML(m.Chat.ID, "Send text, a photo, or a video.", flowCancelMenu("menu:admin"))
		return true
	}
	b.Entities = entities
	if string(b.Entities) == "null" {
		b.Entities = json.RawMessage(`[]`)
	}
	if e = validateBroadcastEntities(b.Body, b.Entities); e != nil {
		a.sendHTML(m.Chat.ID, "The original formatting could not be preserved. Send the content again using Telegram's emoji picker.", flowCancelMenu("menu:admin"))
		return true
	}
	if e = broadcast.Send(a.bot, b, m.Chat.ID); e != nil {
		a.sendHTML(m.Chat.ID, "Preview could not be sent. Check the content and try again.", flowCancelMenu("menu:admin"))
		return true
	}
	raw, _ := json.Marshal(b)
	f.Data["content"] = string(raw)
	f.Step = "audience"
	_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, m.From.ID, f)
	rows := [][]premium.InlineButton{}
	for _, v := range []string{"all", "active", "free", "pro", "enterprise"} {
		rows = append(rows, []premium.InlineButton{premium.Button(v, "admin:broadcast:audience:"+f.Data["nonce"]+":"+v, "primary", "people")})
	}
	rows = append(rows, []premium.InlineButton{premium.Button("Cancel", "flow:cancel", "danger", "cancel")})
	a.sendHTML(m.Chat.ID, "Preview sent. Choose the audience (active means the last 7 days).", broadcastMarkup(rows...))
	return true
}
func (a *App) broadcastStatus(ctx context.Context, chat, id int64) {
	b, e := a.store.BroadcastStatus(ctx, a.botInstanceID, id)
	if e != nil {
		a.sendError(chat, e)
		return
	}
	note := ""
	if b.State == "preparing" {
		note = "\n\nPreparing the recipient list in the background. You can keep using the bot."
	}
	a.sendHTML(chat, fmt.Sprintf("📢 <b>Broadcast #%d</b> · %s\nRecipients: %d\nSent: %d · Failed: %d · Skipped: %d\nUnconfirmed: %d · Remaining: %d", id, b.State, b.Total, b.Sent, b.Failed, b.Skipped, b.Uncertain, b.Pending)+note, broadcastMarkup([]premium.InlineButton{premium.Button("Refresh", fmt.Sprintf("admin:broadcast:status:%d", id), "primary", "refresh"), premium.Button("Cancel delivery", fmt.Sprintf("admin:broadcast:cancel:%d", id), "danger", "cancel")}, []premium.InlineButton{premium.Button("Admin Home", "menu:admin", "primary", "home")}))
}
func (a *App) handleBroadcastCallback(ctx context.Context, c *tgbotapi.CallbackQuery) bool {
	if c.Data != "admin:broadcast" && !strings.HasPrefix(c.Data, "admin:broadcast:") {
		return false
	}
	ok, e := a.store.HasAdminPermission(ctx, a.botInstanceID, c.From.ID, "broadcast")
	if e != nil || !ok {
		a.sendHTML(c.Message.Chat.ID, "You do not have broadcast permission.", userBackMenu())
		return true
	}
	p := strings.Split(c.Data, ":")
	if len(p) == 2 {
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, c.From.ID, store.TelegramFlow{Kind: "broadcast", Step: "content", Data: map[string]string{"nonce": broadcastKey()}})
		a.sendHTML(c.Message.Chat.ID, "📢 <b>New Broadcast</b>\nSend formatted text, a photo, or a video. You’ll see a preview before confirming delivery.", broadcastMarkup([]premium.InlineButton{premium.Button("Recent Broadcasts", "admin:broadcast:recent", "primary", "history"), premium.Button("Cancel", "flow:cancel", "danger", "cancel")}))
		return true
	}
	if p[2] == "recent" {
		items, e := a.store.RecentBroadcasts(ctx, a.botInstanceID)
		if e != nil {
			a.sendError(c.Message.Chat.ID, e)
			return true
		}
		rows := [][]premium.InlineButton{}
		for _, b := range items {
			rows = append(rows, []premium.InlineButton{premium.Button(fmt.Sprintf("#%d · %s · %s", b.ID, b.State, b.Kind), fmt.Sprintf("admin:broadcast:status:%d", b.ID), "primary", "megaphone")})
		}
		rows = append(rows, []premium.InlineButton{premium.Button("New Broadcast", "admin:broadcast", "success", "add"), premium.Button("Admin Home", "menu:admin", "primary", "home")})
		a.sendHTML(c.Message.Chat.ID, "📢 <b>Recent Broadcasts</b>\nSelect a broadcast to view delivery counts.", broadcastMarkup(rows...))
		return true
	}
	if p[2] == "status" || p[2] == "cancel" {
		id, _ := strconv.ParseInt(p[len(p)-1], 10, 64)
		if p[2] == "cancel" {
			_ = a.store.CancelBroadcast(ctx, a.botInstanceID, id)
		}
		a.broadcastStatus(ctx, c.Message.Chat.ID, id)
		return true
	}
	f, e := a.store.TelegramFlow(ctx, a.botInstanceID, c.From.ID)
	if e != nil || f.Kind != "broadcast" || len(p) < 4 || p[3] != f.Data["nonce"] {
		a.sendHTML(c.Message.Chat.ID, "This broadcast form expired. Start a new broadcast.", userBackMenu())
		return true
	}
	if p[2] == "audience" && f.Step == "audience" && len(p) == 5 {
		f.Data["audience"] = p[4]
		n, e := a.store.BroadcastAudienceCount(ctx, a.botInstanceID, p[4])
		if e != nil {
			a.sendError(c.Message.Chat.ID, e)
			return true
		}
		f.Step = "confirm"
		_ = a.store.SetTelegramFlow(ctx, a.botInstanceID, c.From.ID, f)
		a.sendHTML(c.Message.Chat.ID, fmt.Sprintf("Send the preview to <b>%d</b> %s users?", n, p[4]), confirmationMenu("admin:broadcast:confirm:"+f.Data["nonce"], "flow:cancel"))
		return true
	}
	if p[2] == "confirm" && f.Step == "confirm" {
		var b store.Broadcast
		if json.Unmarshal([]byte(f.Data["content"]), &b) != nil {
			return true
		}
		b.Instance = a.botInstanceID
		b.Creator = c.From.ID
		b.Audience = f.Data["audience"]
		id, e := a.store.CreateBroadcast(ctx, b, f.Data["nonce"])
		if e != nil {
			a.sendError(c.Message.Chat.ID, e)
			return true
		}
		_ = a.store.ClearTelegramFlow(ctx, a.botInstanceID, c.From.ID)
		a.broadcastStatus(ctx, c.Message.Chat.ID, id)
	}
	return true
}

func broadcastMarkup(rows ...[]premium.InlineButton) premium.InlineKeyboard {
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

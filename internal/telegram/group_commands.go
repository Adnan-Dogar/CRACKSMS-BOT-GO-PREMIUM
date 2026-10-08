package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (a *App) syncGroupCommands(ctx context.Context, chat, user int64) error {
	commands, e := a.commandMenu(ctx, user)
	if e != nil {
		return e
	}
	items := []map[string]any{}
	for _, c := range commands {
		items = append(items, map[string]any{"command": c.Command, "description": c.Description, "is_ephemeral": true})
	}
	raw, _ := json.Marshal(items)
	key := fmt.Sprintf("%d:%d", chat, user)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	a.ui.mu.Lock()
	old := a.ui.groupCommandHashes[key]
	a.ui.mu.Unlock()
	if old == hash {
		return nil
	}
	scope := map[string]any{"type": "all_group_chats"}
	if chat != 0 && user != 0 {
		scope = map[string]any{"type": "chat_member", "chat_id": chat, "user_id": user}
	}
	params := tgbotapi.Params{"commands": string(raw)}
	_ = params.AddInterface("scope", scope)
	if _, e = a.bot.MakeRequest("setMyCommands", params); e != nil {
		return e
	}
	a.ui.mu.Lock()
	if a.ui.groupCommandHashes == nil {
		a.ui.groupCommandHashes = map[string]string{}
	}
	a.ui.groupCommandHashes[key] = hash
	a.ui.mu.Unlock()
	return nil
}

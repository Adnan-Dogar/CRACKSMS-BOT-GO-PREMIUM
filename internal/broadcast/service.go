package broadcast

import (
	"context"
	"errors"
	"fmt"
	"github.com/adnan-dogar/cracksms-vnext/internal/delivery"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"strconv"
	"time"
)

func Send(bot *tgbotapi.BotAPI, b store.Broadcast, chat int64) error {
	p := tgbotapi.Params{"chat_id": strconv.FormatInt(chat, 10)}
	method := "sendMessage"
	switch b.Kind {
	case "photo", "video":
		method = "sendPhoto"
		if b.Kind == "video" {
			method = "sendVideo"
		}
		p[b.Kind] = b.FileID
		p["caption"] = b.Body
		p["caption_entities"] = string(b.Entities)
	default:
		p["text"] = b.Body
		p["entities"] = string(b.Entities)
	}
	_, e := bot.MakeRequest(method, p)
	return e
}
func Run(ctx context.Context, repo *store.Store, bots delivery.BotProvider) {
	go prepare(ctx, repo)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		b, e := repo.ClaimBroadcast(ctx)
		if e != nil {
			if !errors.Is(e, pgx.ErrNoRows) && ctx.Err() == nil {
				slog.Error("claim broadcast", "error_type", fmt.Sprintf("%T", e))
			}
			continue
		}
		bot, ok := bots.Bot(b.Instance)
		if !ok {
			_ = repo.FinishBroadcastRecipient(ctx, b, "pending", time.Minute)
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		e = Send(tgtransport.BulkBot(sendCtx, bot), b, b.Recipient)
		cancel()
		state := "sent"
		delay := time.Duration(0)
		if e != nil {
			state = "uncertain"
			if api, ok := tgtransport.APIError(e); ok {
				state = "failed"
				if api.Code == 403 {
					state = "skipped"
				}
				if api.Code == 429 {
					state = "pending"
					delay = time.Duration(api.RetryAfter) * time.Second
					if delay < time.Second {
						delay = time.Second
					}
				}
			}
		}
		if e := repo.FinishBroadcastRecipient(ctx, b, state, delay); e != nil && ctx.Err() == nil {
			slog.Error("complete broadcast", "broadcast_id", b.ID, "error_type", fmt.Sprintf("%T", e))
		}
	}
}

func prepare(ctx context.Context, repo *store.Store) {
	for ctx.Err() == nil {
		batchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		more, err := repo.PrepareBroadcastBatch(batchCtx)
		cancel()
		if more && err == nil {
			continue
		}
		timer := time.NewTimer(300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

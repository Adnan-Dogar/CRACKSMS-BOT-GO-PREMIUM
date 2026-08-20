package scheduled

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/delivery"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
)

type Service struct {
	store *store.Store
	bots  delivery.BotProvider
}

func New(repo *store.Store, bots delivery.BotProvider) *Service {
	return &Service{store: repo, bots: bots}
}

func (s *Service) Run(ctx context.Context) {
	go func() {
		for {
			item, err := s.store.ClaimScheduledMessage(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				if !sleep(ctx, 500*time.Millisecond) {
					return
				}
				continue
			}
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("claim scheduled message", "error", err)
				if !sleep(ctx, time.Second) {
					return
				}
				continue
			}
			sendErr := s.send(ctx, item)
			if err := s.store.CompleteScheduledMessage(ctx, item, sendErr); err != nil {
				slog.Error("complete scheduled message", "id", item.ID, "error", err)
			}
		}
	}()
}

func (s *Service) send(ctx context.Context, item domain.ScheduledMessage) error {
	bot, ok := s.bots.Bot(item.BotInstanceID)
	if !ok {
		return fmt.Errorf("bot instance %d is not running", item.BotInstanceID)
	}
	var targets []int64
	switch item.TargetKind {
	case "all_users":
		ids, err := s.store.AllUserIDsForInstance(ctx, item.BotInstanceID)
		if err != nil {
			return err
		}
		targets = ids
	case "user", "group":
		if item.TargetID == nil {
			return errors.New("scheduled target ID is missing")
		}
		targets = []int64{*item.TargetID}
	default:
		return errors.New("unsupported scheduled target")
	}
	failed := 0
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := tgbotapi.NewMessage(target, item.Body)
		message.ParseMode = item.ParseMode
		message.DisableWebPagePreview = true
		if _, err := bot.Send(message); err != nil {
			failed++
		}
		if !sleep(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d scheduled deliveries failed", failed, len(targets))
	}
	return nil
}

func sleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

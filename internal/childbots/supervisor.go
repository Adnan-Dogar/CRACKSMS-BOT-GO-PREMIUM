package childbots

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/botregistry"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	telegramapp "github.com/adnan-dogar/cracksms-vnext/internal/telegram"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Supervisor struct {
	store         *store.Store
	registry      *botregistry.Registry
	refresh       time.Duration
	holdDuration  time.Duration
	reuseCooldown time.Duration
	location      *time.Location
	links         themes.Links
	mu            sync.Mutex
	workers       map[int64]context.CancelFunc
}

func New(repo *store.Store, registry *botregistry.Registry, refresh, holdDuration time.Duration, location *time.Location, links themes.Links) *Supervisor {
	return &Supervisor{store: repo, registry: registry, refresh: refresh, holdDuration: holdDuration,
		location: location, links: links, workers: map[int64]context.CancelFunc{}}
}
func (s *Supervisor) SetReuseCooldown(d time.Duration) *Supervisor { s.reuseCooldown = d; return s }

func (s *Supervisor) Run(ctx context.Context) {
	s.reconcile(ctx)
	ticker := time.NewTicker(s.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			return
		case <-ticker.C:
			s.reconcile(ctx)
		}
	}
}

func (s *Supervisor) reconcile(ctx context.Context) {
	instances, err := s.store.ListBotInstances(ctx, true)
	if err != nil {
		slog.Error("load child bots", "error", err)
		return
	}
	desired := make(map[int64]domain.BotInstance, len(instances))
	for _, instance := range instances {
		desired[instance.ID] = instance
	}
	s.mu.Lock()
	for id, cancel := range s.workers {
		if _, ok := desired[id]; !ok {
			cancel()
			delete(s.workers, id)
			s.registry.Unregister(id)
		}
	}
	s.mu.Unlock()
	for id, instance := range desired {
		s.mu.Lock()
		_, running := s.workers[id]
		s.mu.Unlock()
		if !running {
			s.start(ctx, instance)
		}
	}
}

func (s *Supervisor) start(parent context.Context, instance domain.BotInstance) {
	token, err := s.store.ChildBotToken(parent, instance.ID)
	if err != nil {
		_ = s.store.UpdateBotRuntime(parent, instance.ID, "error", "", safeRuntimeError(err))
		return
	}
	bot, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, &http.Client{Timeout: 40 * time.Second})
	if err != nil {
		_ = s.store.UpdateBotRuntime(parent, instance.ID, "error", "", safeRuntimeError(err))
		return
	}
	bot.Client = tgtransport.New(bot.Client, 25)
	_, _ = bot.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: false})
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	if _, exists := s.workers[instance.ID]; exists {
		s.mu.Unlock()
		cancel()
		return
	}
	s.workers[instance.ID] = cancel
	s.mu.Unlock()
	s.registry.Register(instance.ID, bot)
	_ = s.store.UpdateBotRuntime(parent, instance.ID, "running", bot.Self.UserName, nil)
	slog.Info("child bot started", "instance_id", instance.ID, "bot", bot.Self.UserName, "tier", instance.Tier)
	go func() {
		app := telegramapp.NewForInstance(bot, s.store, instance.ID, false, s.holdDuration, s.location)
		app.SetLinks(s.links)
		if s.reuseCooldown > 0 {
			app.SetReuseCooldown(s.reuseCooldown)
		}
		runErr := app.Run(ctx)
		s.registry.Unregister(instance.ID)
		s.mu.Lock()
		delete(s.workers, instance.ID)
		s.mu.Unlock()
		if parent.Err() == nil {
			status := "stopped"
			if runErr != nil {
				status = "error"
			}
			_ = s.store.UpdateBotRuntime(context.Background(), instance.ID, status, bot.Self.UserName, safeRuntimeError(runErr))
		}
		slog.Info("child bot stopped", "instance_id", instance.ID, "error_type", fmt.Sprintf("%T", runErr))
	}()
}

func (s *Supervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.workers {
		cancel()
		s.registry.Unregister(id)
		delete(s.workers, id)
	}
}

func safeRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	if api, ok := tgtransport.APIError(err); ok {
		return fmt.Errorf("Telegram rejected this connection (HTTP %d); check the token and retry", api.Code)
	}
	return errors.New("Telegram connection unavailable; check the connection and retry")
}

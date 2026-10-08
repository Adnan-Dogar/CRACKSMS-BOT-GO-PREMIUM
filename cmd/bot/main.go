package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/botregistry"
	"github.com/adnan-dogar/cracksms-vnext/internal/broadcast"
	"github.com/adnan-dogar/cracksms-vnext/internal/childbots"
	"github.com/adnan-dogar/cracksms-vnext/internal/config"
	"github.com/adnan-dogar/cracksms-vnext/internal/db"
	"github.com/adnan-dogar/cracksms-vnext/internal/delivery"
	"github.com/adnan-dogar/cracksms-vnext/internal/monitor"
	"github.com/adnan-dogar/cracksms-vnext/internal/otp"
	"github.com/adnan-dogar/cracksms-vnext/internal/panels"
	scheduledservice "github.com/adnan-dogar/cracksms-vnext/internal/scheduled"
	"github.com/adnan-dogar/cracksms-vnext/internal/scheduler"
	"github.com/adnan-dogar/cracksms-vnext/internal/secure"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	telegramapp "github.com/adnan-dogar/cracksms-vnext/internal/telegram"
	"github.com/adnan-dogar/cracksms-vnext/internal/tgtransport"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	webhookservice "github.com/adnan-dogar/cracksms-vnext/internal/webhook"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	if err := run(); err != nil {
		slog.Error("bot stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logLevel, err := cfg.SlogLevel()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	cipher, err := secure.NewCipher(cfg.PanelConfigKey)
	if err != nil {
		return err
	}
	repo := store.New(pool, cfg.Timezone, cipher)
	if err := repo.HydratePanelSourceLinks(ctx); err != nil {
		return err
	}
	if err := repo.SeedAdmins(ctx, cfg.AdminIDs); err != nil {
		return err
	}
	if err := repo.SetMainBotTokenHash(ctx, cfg.BotToken); err != nil {
		return err
	}

	bot, err := tgbotapi.NewBotAPIWithClient(cfg.BotToken, tgbotapi.APIEndpoint, &http.Client{Timeout: 40 * time.Second})
	if err != nil {
		return err
	}
	bot.Client = tgtransport.New(bot.Client, cfg.TelegramSendRate)
	bot.Debug = false
	if _, err := bot.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: false}); err != nil {
		slog.Warn("delete webhook", "error", err)
	}

	otpMetrics := &otp.Metrics{}
	panelMetrics := &panels.Metrics{}
	deliveryMetrics := &delivery.Metrics{}
	webhookMetrics := &webhookservice.Metrics{}
	registry := botregistry.New()
	registry.Register(store.MainBotInstanceID, bot)
	go broadcast.Run(ctx, repo, registry)
	links := themes.Links{Group: cfg.GroupURL, Channel: cfg.ChannelURL, NumberBot: cfg.NumberBotURL, Developer: cfg.DeveloperURL, Support: cfg.SupportURL}

	otpService := otp.NewService(repo, otpMetrics)
	otpService.Run(ctx, 8)
	panelManager := panels.NewManager(repo, cfg.PanelRefreshInterval, cfg.PanelWorkersLimit, panelMetrics)
	go panelManager.Run(ctx)
	deliveryService := delivery.NewRouted(repo, registry, cfg.TelegramSendRate, cfg.GroupURL, cfg.ChannelURL, cfg.NumberBotURL,
		cfg.DeveloperURL, cfg.SupportURL, deliveryMetrics)
	deliveryService.Run(ctx, cfg.DeliveryWorkers)
	webhookservice.New(repo, webhookMetrics).Run(ctx, cfg.WebhookWorkers)
	scheduledservice.New(repo, registry).Run(ctx)
	scheduler.New(repo, bot, cfg.Timezone, cfg.ReuseCooldown).Run(ctx)
	go childbots.New(repo, registry, cfg.ChildRefreshInterval, cfg.HoldDuration, cfg.Timezone, links).SetReuseCooldown(cfg.ReuseCooldown).Run(ctx)

	monitorServer := monitor.New(cfg.HTTPAddr, cfg.MetricsToken, repo, otpMetrics, panelMetrics, deliveryMetrics)
	monitorErr := make(chan error, 1)
	go func() { monitorErr <- monitorServer.Run() }()

	telegramApp := telegramapp.New(bot, repo, cfg.HoldDuration, cfg.Timezone)
	telegramApp.SetLinks(links)
	telegramApp.SetReuseCooldown(cfg.ReuseCooldown)
	telegramErr := make(chan error, 1)
	go func() { telegramErr <- telegramApp.Run(ctx) }()

	slog.Info("CrackSMS vNext started", "bot", bot.Self.UserName, "http_addr", cfg.HTTPAddr)
	select {
	case <-ctx.Done():
	case err := <-monitorErr:
		if err != nil {
			stop()
			return err
		}
	case err := <-telegramErr:
		if err != nil {
			stop()
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := monitorServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	slog.Info("graceful shutdown complete")
	return nil
}

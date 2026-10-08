package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BotToken             string
	DatabaseURL          string
	AdminIDs             []int64
	HTTPAddr             string
	MetricsToken         string
	PanelConfigKey       []byte
	Timezone             *time.Location
	HoldDuration         time.Duration
	ReuseCooldown        time.Duration
	PanelRefreshInterval time.Duration
	DeliveryWorkers      int
	PanelWorkersLimit    int
	TelegramSendRate     int
	LogLevel             string
	ChannelURL           string
	GroupURL             string
	NumberBotURL         string
	DeveloperURL         string
	SupportURL           string
	ChildRefreshInterval time.Duration
	WebhookWorkers       int
}

func (c Config) SlogLevel() (slog.Level, error) {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
}

func Load() (Config, error) {
	var c Config
	c.BotToken = strings.TrimSpace(os.Getenv("BOT_TOKEN"))
	c.DatabaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	c.HTTPAddr = env("HTTP_ADDR", ":8080")
	c.MetricsToken = strings.TrimSpace(os.Getenv("METRICS_TOKEN"))
	c.LogLevel = env("LOG_LEVEL", "info")
	c.ChannelURL = strings.TrimSpace(os.Getenv("CHANNEL_URL"))
	c.GroupURL = strings.TrimSpace(os.Getenv("GROUP_URL"))
	c.NumberBotURL = strings.TrimSpace(os.Getenv("NUMBER_BOT_URL"))
	c.DeveloperURL = strings.TrimSpace(os.Getenv("DEVELOPER_URL"))
	c.SupportURL = strings.TrimSpace(os.Getenv("SUPPORT_URL"))

	var err error
	if c.AdminIDs, err = int64List(os.Getenv("INITIAL_ADMIN_IDS")); err != nil {
		return c, fmt.Errorf("INITIAL_ADMIN_IDS: %w", err)
	}
	if c.Timezone, err = time.LoadLocation(env("TIMEZONE", "Asia/Karachi")); err != nil {
		return c, fmt.Errorf("TIMEZONE: %w", err)
	}
	if c.HoldDuration, err = duration("HOLD_DURATION", 20*time.Minute); err != nil {
		return c, err
	}
	if c.ReuseCooldown, err = duration("SAME_USER_REUSE_COOLDOWN", 24*time.Hour); err != nil {
		return c, err
	}
	if c.PanelRefreshInterval, err = duration("PANEL_REFRESH_INTERVAL", 30*time.Second); err != nil {
		return c, err
	}
	if c.ChildRefreshInterval, err = duration("CHILD_REFRESH_INTERVAL", 15*time.Second); err != nil {
		return c, err
	}
	if c.WebhookWorkers, err = positiveInt("WEBHOOK_WORKERS", 4); err != nil {
		return c, err
	}
	if c.DeliveryWorkers, err = positiveInt("DELIVERY_WORKERS", 8); err != nil {
		return c, err
	}
	if c.PanelWorkersLimit, err = positiveInt("PANEL_WORKERS_LIMIT", 50); err != nil {
		return c, err
	}
	if c.TelegramSendRate, err = positiveInt("TELEGRAM_SEND_RATE", 25); err != nil {
		return c, err
	}
	if c.BotToken == "" {
		return c, errors.New("BOT_TOKEN is required")
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	keyText := strings.TrimSpace(os.Getenv("PANEL_CONFIG_KEY"))
	key, keyErr := base64.StdEncoding.DecodeString(keyText)
	if keyErr != nil || len(key) != 32 {
		return c, errors.New("PANEL_CONFIG_KEY must be a base64-encoded 32-byte key")
	}
	c.PanelConfigKey = key
	if len(c.AdminIDs) == 0 {
		return c, errors.New("INITIAL_ADMIN_IDS must contain at least one ID")
	}
	return c, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	value := env(key, fallback.String())
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return d, nil
}

func positiveInt(key string, fallback int) (int, error) {
	value, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func int64List(raw string) ([]int64, error) {
	var out []int64
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		value, err := strconv.ParseInt(item, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid ID %q", item)
		}
		out = append(out, value)
	}
	return out, nil
}

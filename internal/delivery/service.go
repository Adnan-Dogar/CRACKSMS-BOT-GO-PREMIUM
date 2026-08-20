package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
)

type Metrics struct {
	Sent       atomic.Uint64
	Retried    atomic.Uint64
	Failed     atomic.Uint64
	RateLimits atomic.Uint64
}

type Service struct {
	store        *store.Store
	bots         BotProvider
	limiter      *time.Ticker
	metrics      *Metrics
	channelURL   string
	numberBotURL string
	developerURL string
	supportURL   string
	chatMu       sync.Mutex
	chatAt       map[int64]time.Time
}

type BotProvider interface {
	Bot(instanceID int64) (*tgbotapi.BotAPI, bool)
}

type staticBotProvider struct{ bot *tgbotapi.BotAPI }

func (p staticBotProvider) Bot(instanceID int64) (*tgbotapi.BotAPI, bool) {
	return p.bot, instanceID == store.MainBotInstanceID
}

func New(repo *store.Store, bot *tgbotapi.BotAPI, messagesPerSecond int, channelURL, numberBotURL string, metrics *Metrics) *Service {
	return NewRouted(repo, staticBotProvider{bot: bot}, messagesPerSecond, channelURL, numberBotURL, "", "", metrics)
}

func NewRouted(repo *store.Store, bots BotProvider, messagesPerSecond int, channelURL, numberBotURL, developerURL, supportURL string, metrics *Metrics) *Service {
	if messagesPerSecond <= 0 {
		messagesPerSecond = 25
	}
	return &Service{
		store: repo, bots: bots, limiter: time.NewTicker(time.Second / time.Duration(messagesPerSecond)),
		metrics: metrics, channelURL: channelURL, numberBotURL: numberBotURL, developerURL: developerURL,
		supportURL: supportURL, chatAt: map[int64]time.Time{},
	}
}

func (s *Service) Run(ctx context.Context, workers int) {
	if workers <= 0 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		go s.worker(ctx, i)
	}
	go func() {
		<-ctx.Done()
		s.limiter.Stop()
	}()
}

func (s *Service) worker(ctx context.Context, workerID int) {
	for {
		job, err := s.store.ClaimDeliveryJob(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			if !sleep(ctx, 250*time.Millisecond) {
				return
			}
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("claim delivery", "worker", workerID, "error", err)
			if !sleep(ctx, time.Second) {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.limiter.C:
		}
		s.waitPerChat(ctx, job.TargetID)
		err = s.send(job)
		retryAfter, permanent := classifyTelegramError(err)
		if err == nil {
			s.metrics.Sent.Add(1)
		} else if permanent || job.Attempts >= 8 {
			s.metrics.Failed.Add(1)
		} else {
			s.metrics.Retried.Add(1)
			if retryAfter > 0 {
				s.metrics.RateLimits.Add(1)
			}
		}
		if completeErr := s.store.CompleteDeliveryJob(ctx, job, err, retryAfter, permanent); completeErr != nil {
			slog.Error("complete delivery", "job_id", job.ID, "error", completeErr)
		}
		if job.TargetKind == "group" {
			_ = s.store.MarkOTPGroupDeliveryForInstance(ctx, job.BotInstanceID, job.TargetID, err)
		}
	}
}

func (s *Service) send(job domain.DeliveryJob) error {
	bot, ok := s.bots.Bot(job.BotInstanceID)
	if !ok {
		return fmt.Errorf("bot instance %d is not running", job.BotInstanceID)
	}
	forUser := job.TargetKind == "user"
	body := themes.Format(job.Event, job.ThemeID, forUser, job.OTPVisibility)
	message := tgbotapi.NewMessage(job.TargetID, body)
	message.ParseMode = tgbotapi.ModeHTML
	message.DisableWebPagePreview = true
	if job.ButtonsEnabled {
		exposeOTP := forUser || job.OTPVisibility == "visible"
		message.ReplyMarkup = themes.Keyboard(job.Event, job.ThemeID, themes.Links{
			Channel: s.channelURL, NumberBot: s.numberBotURL, Developer: s.developerURL, Support: s.supportURL,
		}, exposeOTP)
	}
	_, err := bot.Send(message)
	return err
}

func FormatMessage(event domain.OTPEvent, forUser bool) string {
	return themes.Format(event, 0, forUser, "visible")
}

func OTPKeyboard(code, channelURL, numberBotURL string) premium.InlineKeyboard {
	if code == "" {
		return premium.InlineKeyboard{}
	}
	rows := [][]premium.InlineButton{{
		{Text: "Copy OTP", CopyText: &premium.CopyText{Text: code}, Style: "danger", IconCustomEmojiID: premium.ID("otp")},
	}}
	var links []premium.InlineButton
	if channelURL != "" {
		links = append(links, premium.InlineButton{Text: "Channel", URL: channelURL, Style: "success", IconCustomEmojiID: premium.ID("channel")})
	}
	if numberBotURL != "" {
		links = append(links, premium.InlineButton{Text: "Get Number", URL: numberBotURL, Style: "primary", IconCustomEmojiID: premium.ID("number")})
	}
	if len(links) > 0 {
		rows = append(rows, links)
	}
	return premium.InlineKeyboard{InlineKeyboard: rows}
}

func MaskPhone(phone string) string {
	if len(phone) <= 5 {
		return phone
	}
	visiblePrefix := 3
	if len(phone) < 8 {
		visiblePrefix = 1
	}
	return "+" + phone[:visiblePrefix] + strings.Repeat("•", len(phone)-visiblePrefix-4) + phone[len(phone)-4:]
}

func (s *Service) waitPerChat(ctx context.Context, chatID int64) {
	s.chatMu.Lock()
	next := s.chatAt[chatID]
	now := time.Now()
	delay := next.Sub(now)
	if delay < 0 {
		delay = 0
	}
	s.chatAt[chatID] = now.Add(delay + 50*time.Millisecond)
	s.chatMu.Unlock()
	if delay > 0 {
		_ = sleep(ctx, delay)
	}
}

var retryAfterPattern = regexp.MustCompile(`(?i)retry after\s+(\d+)`)

func classifyTelegramError(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	text := strings.ToLower(err.Error())
	if match := retryAfterPattern.FindStringSubmatch(text); len(match) == 2 {
		var seconds int
		_, _ = fmt.Sscanf(match[1], "%d", &seconds)
		return time.Duration(seconds+1) * time.Second, false
	}
	permanent := strings.Contains(text, "bot was blocked") || strings.Contains(text, "chat not found") ||
		strings.Contains(text, "not enough rights") || strings.Contains(text, "forbidden")
	return 0, permanent
}

func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

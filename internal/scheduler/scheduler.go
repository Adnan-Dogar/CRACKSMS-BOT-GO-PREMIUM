package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Scheduler struct {
	store         *store.Store
	bot           *tgbotapi.BotAPI
	location      *time.Location
	reuseCooldown time.Duration
}

func New(repo *store.Store, bot *tgbotapi.BotAPI, location *time.Location, reuseCooldown time.Duration) *Scheduler {
	return &Scheduler{store: repo, bot: bot, location: location, reuseCooldown: reuseCooldown}
}

func (s *Scheduler) Run(ctx context.Context) {
	go s.runExpiry(ctx)
	go s.runDailySummaries(ctx)
}

func (s *Scheduler) runExpiry(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				result, err := s.store.RecycleExpiredAssignments(ctx, s.reuseCooldown, 200)
				if err != nil {
					slog.Error("recycle assignments", "error", err)
					break
				}
				if result.Assignments > 0 {
					slog.Info("assignments recycled", "assignments", result.Assignments,
						"returned", result.Returned, "consumed", result.Consumed)
				}
				if result.Assignments < 200 {
					break
				}
			}
		}
	}
}

func (s *Scheduler) runDailySummaries(ctx context.Context) {
	for {
		now := time.Now().In(s.location)
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 5, 0, s.location)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		date := next.AddDate(0, 0, -1)
		summaries, err := s.store.DailySummaries(ctx, date)
		if err != nil {
			slog.Error("daily summaries", "error", err)
			continue
		}
		for _, summary := range summaries {
			message := tgbotapi.NewMessage(summary.UserID, premium.AnimateHTML(DailySummaryText(date, summary)))
			message.ParseMode = tgbotapi.ModeHTML
			if _, err := s.bot.Send(message); err != nil {
				slog.Warn("daily summary delivery failed", "user_id", summary.UserID, "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

// DailySummaryText renders the end-of-day report, including USD rewards when
// a limited USD milestone paid out that day.
func DailySummaryText(date time.Time, summary store.DailySummary) string {
	var text strings.Builder
	fmt.Fprintf(&text, "📊 <b>Daily Summary</b>\n\n📅 Date: <b>%s</b>\n🔐 OTPs: <b>%d</b>\n💵 OTP earnings: <b>%.2f PKR</b>\n🎁 Rewards: <b>%.2f PKR</b>",
		date.Format("02 January 2006"), summary.OTPCount, summary.BasePKR, summary.RewardPKR)
	if summary.RewardUSD > 0 {
		fmt.Fprintf(&text, "\n💲 USD rewards: <b>%.4f USD</b>", summary.RewardUSD)
	}
	fmt.Fprintf(&text, "\n💰 Total: <b>%.2f PKR</b>", summary.BasePKR+summary.RewardPKR)
	if summary.RewardUSD > 0 {
		fmt.Fprintf(&text, " + <b>%.4f USD</b>", summary.RewardUSD)
	}
	text.WriteString("\n\nKeep going — a new day of milestones has started!")
	return text.String()
}

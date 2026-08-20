package otp

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

type Metrics struct {
	Received   atomic.Uint64
	Accepted   atomic.Uint64
	Duplicates atomic.Uint64
	Failed     atomic.Uint64
}

type Service struct {
	store   *store.Store
	metrics *Metrics
}

func NewService(repo *store.Store, metrics *Metrics) *Service {
	return &Service{store: repo, metrics: metrics}
}

func (s *Service) Run(ctx context.Context, workers int) {
	if workers <= 0 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		go s.worker(ctx, i)
	}
}

func (s *Service) worker(ctx context.Context, workerID int) {
	for {
		job, err := s.store.ClaimIngestJob(ctx)
		if err != nil {
			if store.IsNoDeliveryJob(err) {
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
					continue
				}
			}
			if ctx.Err() != nil {
				return
			}
			slog.Error("claim OTP ingest job", "worker", workerID, "error", err)
			time.Sleep(time.Second)
			continue
		}
		event := job.Event
		s.metrics.Received.Add(1)
		if event.NormalizedPhone == "" {
			event.NormalizedPhone = store.NormalizePhone(event.Phone)
		}
		if event.Code == "" {
			event.Code = Extract(event.Message)
			if event.Code == "" {
				if custom, customErr := s.store.ListCustomOTPPatterns(ctx, event.BotInstanceID); customErr == nil {
					event.Code = ExtractWithCustom(event.Message, custom)
				}
			}
		}
		if event.DedupKey == "" {
			event.DedupKey = store.DedupKey(event.PanelName, event.NormalizedPhone, event.Message)
		}
		result, err := s.store.AcceptOTP(ctx, event)
		if err != nil {
			s.metrics.Failed.Add(1)
			slog.Error("OTP processing failed", "worker", workerID, "panel", event.PanelName, "error", err)
			_ = s.store.CompleteIngestJob(ctx, job, err)
			continue
		}
		_ = s.store.CompleteIngestJob(ctx, job, nil)
		if result.Duplicate {
			s.metrics.Duplicates.Add(1)
			continue
		}
		s.metrics.Accepted.Add(1)
		slog.Info("OTP accepted", "panel", event.PanelName, "assigned_user", result.AssignedUserID,
			"counted", result.Counted, "daily_count", result.DailyCount, "reward_pkr", result.RewardCreditPKR)
	}
}

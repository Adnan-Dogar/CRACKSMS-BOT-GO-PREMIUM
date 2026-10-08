package panels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

type Metrics struct {
	Polls       atomic.Uint64
	Failures    atomic.Uint64
	Events      atomic.Uint64
	QueueBlocks atomic.Uint64
}

type Manager struct {
	store     *store.Store
	refresh   time.Duration
	workerCap int
	metrics   *Metrics
	mu        sync.Mutex
	workers   map[int64]runningWorker
}

type runningWorker struct {
	fingerprint string
	cancel      context.CancelFunc
}

func NewManager(repo *store.Store, refresh time.Duration, workerCap int, metrics *Metrics) *Manager {
	return &Manager{store: repo, refresh: refresh, workerCap: workerCap, metrics: metrics, workers: map[int64]runningWorker{}}
}

func (m *Manager) Run(ctx context.Context) {
	go m.runConnectionTests(ctx)
	m.reconcile(ctx)
	ticker := time.NewTicker(m.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-ticker.C:
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) runConnectionTests(ctx context.Context) {
	for ctx.Err() == nil {
		job, e := m.store.ClaimPanelTest(ctx)
		if e != nil {
			if !sleepContext(ctx, 3*time.Second) {
				return
			}
			continue
		}
		panel, e := m.store.PanelForInstance(ctx, job.Instance, job.PanelID)
		if e == nil {
			if job.Enable {
				// Reconnect must replace an existing worker even when credentials
				// are unchanged. Disable it while its new connection is tested.
				_ = m.store.SetPanelEnabledForInstance(ctx, job.Instance, job.PanelID, false)
				m.reconcile(ctx)
			}
			adapter, err := NewAdapterWithGate(panel, m.store)
			e = err
			if e == nil {
				testCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				e = adapter.Test(testCtx)
				cancel()
				_ = adapter.Close()
			}
		}
		status := "Connection verified"
		retry := time.Time{}
		if e != nil {
			status = SafeError(e)
			var provider *ProviderError
			if errors.As(e, &provider) && provider.Status == 429 {
				retry = provider.RetryAt
			}
		}
		_ = m.store.FinishPanelTest(ctx, job, e == nil, status, retry)
		if e == nil {
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	configured, err := m.store.ListEnabledPanels(ctx)
	if err != nil {
		slog.Error("load panels", "error", err)
		return
	}
	if len(configured) > m.workerCap {
		configured = configured[:m.workerCap]
	}
	desired := map[int64]domain.Panel{}
	for _, panel := range configured {
		desired[panel.ID] = panel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, worker := range m.workers {
		panel, exists := desired[id]
		if !exists || fingerprint(panel) != worker.fingerprint {
			worker.cancel()
			delete(m.workers, id)
		}
	}
	for id, panel := range desired {
		if _, exists := m.workers[id]; exists {
			continue
		}
		workerCtx, cancel := context.WithCancel(ctx)
		m.workers[id] = runningWorker{fingerprint: fingerprint(panel), cancel: cancel}
		go m.runPanel(workerCtx, panel)
	}
}

func (m *Manager) runPanel(ctx context.Context, panel domain.Panel) {
	adapter, err := NewAdapterWithGate(panel, m.store)
	if err != nil {
		_ = m.store.UpdatePanelHealth(ctx, panel.ID, panel.LastCursor, errors.New(SafeError(err)))
		return
	}
	defer adapter.Close()
	cursor := panel.LastCursor
	failures := 0
	for {
		pollCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		events, nextCursor, err := adapter.Poll(pollCtx, cursor)
		cancel()
		m.metrics.Polls.Add(1)
		if err != nil {
			failures++
			m.metrics.Failures.Add(1)
			_ = m.store.UpdatePanelHealth(ctx, panel.ID, cursor, errors.New(SafeError(err)))
			if errors.Is(err, ErrProviderAccessRequired) {
				_ = m.store.UpdateProviderState(ctx, panel.ID, "Session renewal required", time.Time{})
				_ = m.store.SetPanelEnabledForInstance(ctx, panel.BotInstanceID, panel.ID, false)
				return
			}
			delay := time.Duration(1<<min(failures, 6)) * time.Second
			if delay > time.Minute {
				delay = time.Minute
			}
			var provider *ProviderError
			if errors.As(err, &provider) {
				if provider.RetryAt.After(time.Now()) {
					delay = time.Until(provider.RetryAt)
				}
				if provider.Status == 401 || provider.Status == 403 {
					_ = m.store.UpdateProviderState(ctx, panel.ID, "Credentials or access required", time.Time{})
					_ = m.store.SetPanelEnabledForInstance(ctx, panel.BotInstanceID, panel.ID, false)
					return
				}
			}
			_ = m.store.UpdateProviderState(ctx, panel.ID, "Retry scheduled", time.Now().Add(delay))
			if !sleepContext(ctx, delay) {
				return
			}
			continue
		}
		failures = 0
		for {
			if err := m.store.EnqueuePanelEvents(ctx, panel.ID, events, nextCursor); err != nil {
				m.metrics.QueueBlocks.Add(1)
				_ = m.store.UpdatePanelHealth(ctx, panel.ID, cursor, errors.New(SafeError(err)))
				if !sleepContext(ctx, time.Second) {
					return
				}
				continue // Retain this batch until the durable queue commits.
			}
			break
		}
		cursor = nextCursor
		status := "API connected"
		if reporter, ok := adapter.(ConnectionReporter); ok {
			status = reporter.ConnectionStatus()
		}
		_ = m.store.UpdateProviderState(ctx, panel.ID, status, time.Time{})
		m.metrics.Events.Add(uint64(len(events)))
		interval := panel.PollInterval
		if panel.Kind == "socketio" || panel.Kind == "ivas" {
			interval = time.Millisecond * 100
		}
		if (panel.Kind == "augestel" || panel.Kind == "axon_asp") && interval < 15*time.Second {
			interval = 15 * time.Second
		}
		jitter := time.Duration(rand.Int63n(int64(max(interval/5, time.Millisecond))))
		if !sleepContext(ctx, interval+jitter) {
			return
		}
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, worker := range m.workers {
		worker.cancel()
		delete(m.workers, id)
	}
}

func fingerprint(panel domain.Panel) string {
	panel.LastCursor = ""
	panel.Healthy = false
	panel.ConsecutiveFailures = 0
	data, _ := json.Marshal(panel)
	return fmt.Sprintf("%x", data)
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

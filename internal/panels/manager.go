package panels

import (
	"context"
	"encoding/json"
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
	adapter, err := NewAdapter(panel)
	if err != nil {
		_ = m.store.UpdatePanelHealth(ctx, panel.ID, panel.LastCursor, err)
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
			_ = m.store.UpdatePanelHealth(ctx, panel.ID, cursor, err)
			delay := time.Duration(1<<min(failures, 6)) * time.Second
			if delay > time.Minute {
				delay = time.Minute
			}
			if !sleepContext(ctx, delay) {
				return
			}
			continue
		}
		failures = 0
		if err := m.store.EnqueuePanelEvents(ctx, panel.ID, events, nextCursor); err != nil {
			m.metrics.QueueBlocks.Add(1)
			_ = m.store.UpdatePanelHealth(ctx, panel.ID, cursor, err)
			if !sleepContext(ctx, time.Second) {
				return
			}
			continue
		}
		cursor = nextCursor
		m.metrics.Events.Add(uint64(len(events)))
		jitter := time.Duration(rand.Int63n(int64(max(panel.PollInterval/5, time.Millisecond))))
		if !sleepContext(ctx, panel.PollInterval+jitter) {
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

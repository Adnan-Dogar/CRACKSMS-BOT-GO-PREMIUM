package monitor

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/delivery"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/otp"
	"github.com/adnan-dogar/cracksms-vnext/internal/panels"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

type Server struct {
	http      *http.Server
	store     *store.Store
	token     string
	otp       *otp.Metrics
	panels    *panels.Metrics
	delivery  *delivery.Metrics
	startedAt time.Time
	rateMu    sync.Mutex
	rate      map[string][]time.Time
}

func New(addr, token string, repo *store.Store, otpMetrics *otp.Metrics, panelMetrics *panels.Metrics, deliveryMetrics *delivery.Metrics) *Server {
	s := &Server{store: repo, token: token, otp: otpMetrics, panels: panelMetrics, delivery: deliveryMetrics, startedAt: time.Now(), rate: map[string][]time.Time{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/metrics", s.authorized(s.metrics))
	mux.HandleFunc("/api/v1/me", s.api(s.apiMe, "profile:read"))
	mux.HandleFunc("/api/v1/analytics", s.api(s.apiAnalytics, "analytics:read"))
	mux.HandleFunc("/api/v1/otp/history", s.api(s.apiHistory, "history:read"))
	s.http = &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	return s
}

type apiContextKey struct{}

func (s *Server) api(next http.HandlerFunc, requiredScope string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		raw := strings.TrimSpace(stringTrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if raw == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		key, err := s.store.AuthenticateAPIKey(r.Context(), raw)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if !containsScope(key.Scopes, requiredScope) {
			http.Error(w, `{"error":"insufficient_scope"}`, http.StatusForbidden)
			return
		}
		if s.apiRateLimited(key.Prefix) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
			return
		}
		ctx := context.WithValue(r.Context(), apiContextKey{}, key)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	key := r.Context().Value(apiContextKey{}).(domain.APIKey)
	tier, _ := s.store.UserTier(r.Context(), key.BotInstanceID, key.UserID)
	writeJSON(w, map[string]any{"user_id": key.UserID, "bot_instance_id": key.BotInstanceID,
		"tier": tier, "api_key": key.Prefix, "scopes": key.Scopes})
}

func (s *Server) apiAnalytics(w http.ResponseWriter, r *http.Request) {
	key := r.Context().Value(apiContextKey{}).(domain.APIKey)
	analytics, err := s.store.Analytics(r.Context(), key.BotInstanceID)
	if err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, analytics)
}

func (s *Server) apiHistory(w http.ResponseWriter, r *http.Request) {
	key := r.Context().Value(apiContextKey{}).(domain.APIKey)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	history, err := s.store.OTPHistory(r.Context(), key.BotInstanceID, key.UserID, limit, offset)
	if err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"items": history, "offset": offset, "count": len(history)})
}

func (s *Server) apiRateLimited(prefix string) bool {
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	items := s.rate[prefix][:0]
	for _, item := range s.rate[prefix] {
		if item.After(cutoff) {
			items = append(items, item)
		}
	}
	if len(items) >= 60 {
		s.rate[prefix] = items
		return true
	}
	s.rate[prefix] = append(items, now)
	return false
}

func containsScope(scopes []string, required string) bool {
	if required == "profile:read" {
		return true
	}
	for _, scope := range scopes {
		if scope == "*" || scope == required {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) Run() error {
	err := s.http.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "uptime_seconds": int(time.Since(s.startedAt).Seconds())})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if !s.store.DatabaseReady(r.Context()) {
		http.Error(w, `{"status":"not_ready"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	pending, _ := s.store.PendingDeliveryCount(r.Context())
	ingestPending, _ := s.store.PendingIngestCount(r.Context())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "cracksms_uptime_seconds %d\n", int(time.Since(s.startedAt).Seconds()))
	fmt.Fprintf(w, "cracksms_otp_received_total %d\n", s.otp.Received.Load())
	fmt.Fprintf(w, "cracksms_otp_accepted_total %d\n", s.otp.Accepted.Load())
	fmt.Fprintf(w, "cracksms_otp_duplicates_total %d\n", s.otp.Duplicates.Load())
	fmt.Fprintf(w, "cracksms_otp_failed_total %d\n", s.otp.Failed.Load())
	fmt.Fprintf(w, "cracksms_panel_polls_total %d\n", s.panels.Polls.Load())
	fmt.Fprintf(w, "cracksms_panel_failures_total %d\n", s.panels.Failures.Load())
	fmt.Fprintf(w, "cracksms_panel_events_total %d\n", s.panels.Events.Load())
	fmt.Fprintf(w, "cracksms_panel_queue_blocks_total %d\n", s.panels.QueueBlocks.Load())
	fmt.Fprintf(w, "cracksms_delivery_sent_total %d\n", s.delivery.Sent.Load())
	fmt.Fprintf(w, "cracksms_delivery_retried_total %d\n", s.delivery.Retried.Load())
	fmt.Fprintf(w, "cracksms_delivery_failed_total %d\n", s.delivery.Failed.Load())
	fmt.Fprintf(w, "cracksms_delivery_pending %d\n", pending)
	fmt.Fprintf(w, "cracksms_ingest_pending %d\n", ingestPending)
}

func (s *Server) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("Authorization")
		provided = stringTrimPrefix(provided, "Bearer ")
		if s.token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func stringTrimPrefix(value, prefix string) string {
	if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
		return value[len(prefix):]
	}
	return value
}

package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/jackc/pgx/v5"
)

type Metrics struct {
	Sent    atomic.Uint64
	Retried atomic.Uint64
	Failed  atomic.Uint64
}

type Service struct {
	store   *store.Store
	client  *http.Client
	metrics *Metrics
}

func New(repo *store.Store, metrics *Metrics) *Service {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, candidate := range ips {
				if safePublicIP(candidate.IP) {
					return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
				}
			}
			return nil, errors.New("webhook host does not resolve to a public IP")
		},
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many webhook redirects")
		}
		return ValidateURL(req.URL.String())
	}
	return &Service{store: repo, client: client, metrics: metrics}
}

func ValidateURL(raw string) error {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return errors.New("webhook URL must be a valid HTTPS URL")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("webhook URL cannot contain credentials or fragments")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("webhook URL must use a public host")
	}
	if ip := net.ParseIP(host); ip != nil && !safePublicIP(ip) {
		return errors.New("webhook URL cannot target a private network")
	}
	return nil
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
		delivery, err := s.store.ClaimWebhookDelivery(ctx)
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
			slog.Error("claim webhook", "worker", workerID, "error", err)
			if !sleep(ctx, time.Second) {
				return
			}
			continue
		}
		status, sendErr := s.send(ctx, delivery)
		if err := s.store.CompleteWebhookDelivery(ctx, delivery, status, sendErr); err != nil {
			slog.Error("complete webhook", "delivery_id", delivery.ID, "error", err)
		}
		if sendErr == nil && status >= 200 && status < 300 {
			s.metrics.Sent.Add(1)
		} else if delivery.Attempts >= 8 {
			s.metrics.Failed.Add(1)
		} else {
			s.metrics.Retried.Add(1)
		}
	}
}

func (s *Service) send(ctx context.Context, delivery domain.WebhookDelivery) (int, error) {
	if err := ValidateURL(delivery.Endpoint.URL); err != nil {
		return 0, err
	}
	payload := map[string]any{
		"id": delivery.Event.ID, "event": delivery.EventName, "created_at": time.Now().UTC(),
		"data": map[string]any{
			"panel": delivery.Event.PanelName, "phone": delivery.Event.NormalizedPhone,
			"service": delivery.Event.Service, "country": delivery.Event.Country, "code": delivery.Event.Code, "message": delivery.Event.Message,
			"received_at": delivery.Event.ReceivedAt.UTC(),
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(delivery.Endpoint.Secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.Endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "CrackSMS-vNext-Webhook/1.0")
	req.Header.Set("X-CrackSMS-Event", delivery.EventName)
	req.Header.Set("X-CrackSMS-Delivery", fmt.Sprint(delivery.ID))
	req.Header.Set("X-CrackSMS-Timestamp", timestamp)
	req.Header.Set("X-CrackSMS-Signature", "sha256="+signature)
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// Special-purpose ranges that the net.IP helpers do not cover, including
// carrier-grade NAT (used by some cloud metadata services) and NAT64, which
// can translate to private IPv4 destinations.
var reservedNetworks = func() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48"} {
		_, network, _ := net.ParseCIDR(cidr)
		out = append(out, network)
	}
	return out
}()

func safePublicIP(ip net.IP) bool {
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, network := range reservedNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
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

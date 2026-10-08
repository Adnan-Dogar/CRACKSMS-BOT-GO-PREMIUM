package tgtransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestPriorityReservationAndCancellation(t *testing.T) {
	c := New(http.DefaultClient, 25)
	c.mu.Lock()
	c.tokens = 5
	c.at = time.Now()
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := c.wait(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("normal traffic consumed reserved OTP capacity", err)
	}
	ctx, cancel = context.WithTimeout(context.WithValue(context.Background(), priorityKey{}, true), time.Second)
	defer cancel()
	if err := c.wait(ctx, ""); err != nil {
		t.Fatal("OTP could not use reserved capacity", err)
	}
}
func TestTelegramJSONRateLimitAndErrorShapes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":10}}`))
	}))
	defer server.Close()
	c := New(server.Client(), 25)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/bottest/sendMessage", nil)
	response, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	c.mu.Lock()
	blocked := c.blockedUntil.After(time.Now().Add(9 * time.Second))
	c.mu.Unlock()
	if !blocked {
		t.Fatal("retry_after did not stop traffic")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err = c.wait(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("blocked request ignored cancellation", err)
	}
	for _, err := range []error{tgbotapi.Error{Code: 403}, &tgbotapi.Error{Code: 403}} {
		if api, ok := APIError(err); !ok || api.Code != 403 {
			t.Fatal("error representation lost", err)
		}
	}
}

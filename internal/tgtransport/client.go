package tgtransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type priorityKey struct{}
type interactiveKey struct{}
type bulkKey struct{}
type Client struct {
	base             tgbotapi.HTTPClient
	mu               sync.Mutex
	rate, tokens     float64
	at, blockedUntil time.Time
	chatAt           map[string]time.Time
}

func New(base tgbotapi.HTTPClient, rate int) *Client {
	if rate <= 0 {
		rate = 25
	}
	rate = min(rate, 25)
	return &Client{base: base, rate: float64(rate), tokens: float64(rate), at: time.Now(), chatAt: map[string]time.Time{}}
}
func (c *Client) wait(ctx context.Context, chat string) error {
	high, _ := ctx.Value(priorityKey{}).(bool)
	for {
		c.mu.Lock()
		now := time.Now()
		c.tokens = min(c.rate, c.tokens+now.Sub(c.at).Seconds()*c.rate)
		c.at = now
		reserve := min(5.0, c.rate/3)
		if interactive, _ := ctx.Value(interactiveKey{}).(bool); interactive {
			reserve = min(2.0, c.rate/4)
		}
		if bulk, _ := ctx.Value(bulkKey{}).(bool); bulk {
			reserve = min(8.0, c.rate/2)
		}
		if high {
			reserve = 0
		}
		ready := now.After(c.blockedUntil) && (chat == "" || !now.Before(c.chatAt[chat])) && c.tokens >= 1+reserve
		if ready {
			c.tokens--
			if chat != "" {
				c.chatAt[chat] = now.Add(time.Second)
			}
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		timer := time.NewTimer(40 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	method := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
	limited := strings.HasPrefix(method, "send") || strings.HasPrefix(method, "editMessage")
	if !limited {
		return c.base.Do(req)
	}
	chat := ""
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil {
			copyReq := req.Clone(req.Context())
			copyReq.Body = body
			_ = copyReq.ParseForm()
			chat = copyReq.Form.Get("chat_id")
			body.Close()
		}
	}
	if err := c.wait(req.Context(), chat); err != nil {
		return nil, err
	}
	response, err := c.base.Do(req)
	if err != nil {
		return response, err
	}
	// Telegram may return 429 in JSON even when the HTTP response is 200.
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	var result struct {
		ErrorCode  int `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(body, &result) == nil && result.ErrorCode == 429 {
		c.mu.Lock()
		until := time.Now().Add(time.Duration(max(1, result.Parameters.RetryAfter)) * time.Second)
		if until.After(c.blockedUntil) {
			c.blockedUntil = until
		}
		c.mu.Unlock()
	}
	return response, nil
}

type priorityClient struct {
	base tgbotapi.HTTPClient
	ctx  context.Context
}

func (c priorityClient) Do(req *http.Request) (*http.Response, error) {
	ctx := requestContext(req, c.ctx)
	return c.base.Do(req.WithContext(context.WithValue(ctx, priorityKey{}, true)))
}
func PriorityBot(ctx context.Context, bot *tgbotapi.BotAPI) *tgbotapi.BotAPI {
	clone := *bot
	clone.Client = priorityClient{bot.Client, ctx}
	return &clone
}

func APIError(err error) (*tgbotapi.Error, bool) {
	var pointer *tgbotapi.Error
	if errors.As(err, &pointer) {
		return pointer, true
	}
	var value tgbotapi.Error
	if errors.As(err, &value) {
		return &value, true
	}
	return nil, false
}

type trafficClient struct {
	base tgbotapi.HTTPClient
	ctx  context.Context
	bulk bool
}

func (c trafficClient) Do(req *http.Request) (*http.Response, error) {
	ctx := requestContext(req, c.ctx)
	if c.bulk {
		ctx = context.WithValue(ctx, bulkKey{}, true)
	} else {
		ctx = context.WithValue(ctx, interactiveKey{}, true)
	}
	return c.base.Do(req.WithContext(ctx))
}
func InteractiveBot(ctx context.Context, bot *tgbotapi.BotAPI) *tgbotapi.BotAPI {
	clone := *bot
	clone.Client = trafficClient{bot.Client, ctx, false}
	return &clone
}
func BulkBot(ctx context.Context, bot *tgbotapi.BotAPI) *tgbotapi.BotAPI {
	clone := *bot
	clone.Client = trafficClient{bot.Client, ctx, true}
	return &clone
}

func requestContext(req *http.Request, fallback context.Context) context.Context {
	if req.Context() == context.Background() || req.Context() == context.TODO() {
		return fallback
	}
	return req.Context()
}

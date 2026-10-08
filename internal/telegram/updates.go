package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Keep original entities alongside the legacy SDK types. In particular, the
// SDK predates custom_emoji_id and cannot round-trip a premium emoji.
type incomingUpdate struct {
	Update                    tgbotapi.Update
	Entities, CaptionEntities json.RawMessage
	MessageMeta, CallbackMeta messageMetadata
}

type messageMetadata struct {
	EphemeralID int            `json:"ephemeral_message_id"`
	Receiver    *tgbotapi.User `json:"receiver_user"`
	ThreadID    int            `json:"message_thread_id"`
}

type entityContextKey struct{}

type contextClient struct {
	ctx    context.Context
	client tgbotapi.HTTPClient
}

func (c contextClient) Do(req *http.Request) (*http.Response, error) {
	if req.Context() == context.Background() || req.Context() == context.TODO() {
		return c.client.Do(req.WithContext(c.ctx))
	}
	return c.client.Do(req)
}

func decodeUpdates(raw json.RawMessage) ([]incomingUpdate, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]incomingUpdate, 0, len(items))
	for _, item := range items {
		var update incomingUpdate
		if err := json.Unmarshal(item, &update.Update); err != nil {
			return nil, err
		}
		var envelope struct {
			Message struct {
				messageMetadata
				Entities        json.RawMessage `json:"entities"`
				CaptionEntities json.RawMessage `json:"caption_entities"`
			} `json:"message"`
			Callback struct {
				Message messageMetadata `json:"message"`
			} `json:"callback_query"`
		}
		if err := json.Unmarshal(item, &envelope); err != nil {
			return nil, err
		}
		update.Entities, update.CaptionEntities = envelope.Message.Entities, envelope.Message.CaptionEntities
		update.MessageMeta, update.CallbackMeta = envelope.Message.messageMetadata, envelope.Callback.Message
		out = append(out, update)
	}
	return out, nil
}

func (a *App) pollUpdates(ctx context.Context, offset int) ([]incomingUpdate, error) {
	bot := *a.bot
	bot.Client = contextClient{ctx: ctx, client: a.bot.Client}
	response, err := bot.MakeRequest("getUpdates", tgbotapi.Params{
		"offset": strconv.Itoa(offset), "timeout": "30", "limit": "100",
		"allowed_updates": `["message","callback_query"]`,
	})
	if err != nil {
		return nil, err
	}
	return decodeUpdates(response.Result)
}

func originalEntities(ctx context.Context, message *tgbotapi.Message, caption bool) json.RawMessage {
	if incoming, ok := ctx.Value(entityContextKey{}).(incomingUpdate); ok {
		raw := incoming.Entities
		if caption {
			raw = incoming.CaptionEntities
		}
		if len(raw) != 0 && string(raw) != "null" {
			return raw
		}
	}
	entities := message.Entities
	if caption {
		entities = message.CaptionEntities
	}
	raw, _ := json.Marshal(entities)
	if string(raw) == "null" {
		return json.RawMessage(`[]`)
	}
	return raw
}

// A command's prefix is measured in UTF-16 code units, just like Telegram's
// entity offsets. Preserve every unknown field while adjusting those offsets.
func commandContent(text string, raw json.RawMessage) (string, json.RawMessage, error) {
	end := strings.IndexAny(text, " \n\t\r")
	if end < 0 {
		return "", json.RawMessage(`[]`), nil
	}
	prefix := end + len(text[end:]) - len(strings.TrimLeft(text[end:], " \n\t\r"))
	units := len(utf16.Encode([]rune(text[:prefix])))
	var entities []map[string]json.RawMessage
	if len(raw) > 0 && json.Unmarshal(raw, &entities) != nil {
		return "", nil, errors.New("invalid entities")
	}
	out := make([]map[string]json.RawMessage, 0, len(entities))
	for _, entity := range entities {
		var offset, length int
		if json.Unmarshal(entity["offset"], &offset) != nil || json.Unmarshal(entity["length"], &length) != nil {
			return "", nil, errors.New("invalid entity bounds")
		}
		if offset+length <= units {
			continue
		}
		if offset < units {
			return "", nil, errors.New("formatting crosses the command prefix; send the content separately")
		}
		entity["offset"], _ = json.Marshal(offset - units)
		out = append(out, entity)
	}
	adjusted, err := json.Marshal(out)
	return text[prefix:], adjusted, err
}

func validateBroadcastEntities(body string, raw json.RawMessage) error {
	var entities []struct {
		Type          string `json:"type"`
		Offset        int    `json:"offset"`
		Length        int    `json:"length"`
		CustomEmojiID string `json:"custom_emoji_id"`
	}
	if err := json.Unmarshal(raw, &entities); err != nil {
		return err
	}
	units := len(utf16.Encode([]rune(body)))
	for _, e := range entities {
		if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > units {
			return errors.New("invalid entity bounds")
		}
		if e.Type == "custom_emoji" {
			if _, err := strconv.ParseUint(e.CustomEmojiID, 10, 64); err != nil {
				return errors.New("missing custom emoji identifier")
			}
		}
	}
	return nil
}

package broadcast

import (
	"encoding/json"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSendPreservesMediaAndFormatting(t *testing.T) {
	var method string
	var params url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.URL.Path == "/bottest/getMe" {
			w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`))
			return
		}
		method = r.URL.Path
		params = r.Form
		w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer server.Close()
	bot, e := tgbotapi.NewBotAPIWithClient("test", server.URL+"/bot%s/%s", server.Client())
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"text", "photo", "video"} {
		b := store.Broadcast{Kind: kind, Body: "Caption", FileID: "file-fixture", Entities: json.RawMessage(`[{"type":"custom_emoji","offset":0,"length":2,"custom_emoji_id":"5334544901428229844"}]`)}
		if e = Send(bot, b, 123); e != nil {
			t.Fatal(e)
		}
		field := "entities"
		want := "/bottest/sendMessage"
		if kind != "text" {
			field = "caption_entities"
			want = "/bottest/sendPhoto"
			if kind == "video" {
				want = "/bottest/sendVideo"
			}
			if params.Get(kind) != b.FileID {
				t.Fatal("media file lost")
			}
		}
		if method != want || params.Get(field) != string(b.Entities) || params.Get("chat_id") != "123" {
			t.Fatal("media/formatting routing changed", method, params)
		}
	}
}

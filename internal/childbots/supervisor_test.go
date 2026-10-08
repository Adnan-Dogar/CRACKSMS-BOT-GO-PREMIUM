package childbots

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"strings"
	"testing"
)

func TestRuntimeErrorsNeverExposeBotTokens(t *testing.T) {
	err := fmt.Errorf("Post https://api.telegram.org/botfixture-secret/getMe: timeout")
	if strings.Contains(safeRuntimeError(err).Error(), "fixture-secret") {
		t.Fatal("bot token exposed")
	}
	if !strings.Contains(safeRuntimeError(tgbotapi.Error{Code: 401, Message: "Unauthorized"}).Error(), "401") {
		t.Fatal("authentication status lost")
	}
	if safeRuntimeError(nil) != nil {
		t.Fatal("healthy runtime marked failed")
	}
}

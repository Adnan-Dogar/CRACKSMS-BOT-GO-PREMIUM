package botregistry

import (
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Registry struct {
	mu   sync.RWMutex
	bots map[int64]*tgbotapi.BotAPI
}

func New() *Registry { return &Registry{bots: map[int64]*tgbotapi.BotAPI{}} }

func (r *Registry) Register(instanceID int64, bot *tgbotapi.BotAPI) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bots[instanceID] = bot
}

func (r *Registry) Unregister(instanceID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bots, instanceID)
}

func (r *Registry) Bot(instanceID int64) (*tgbotapi.BotAPI, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bot, ok := r.bots[instanceID]
	return bot, ok
}

package store_test

import (
	"context"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
)

func TestGroupLinkOverridesBotDefault(t *testing.T) {
	repo, _ := providerStore(t)
	ctx := context.Background()
	defaults := themes.Links{Group: "https://t.me/default_group", Channel: "https://t.me/default_channel", NumberBot: "https://t.me/numbers"}
	links, err := repo.EffectiveLinks(ctx, 1, defaults)
	if err != nil || links != defaults {
		t.Fatal("default links changed", links, err)
	}
	if err := repo.SetInstanceSetting(ctx, 1, "group_url", "https://t.me/own_group"); err != nil {
		t.Fatal(err)
	}
	links, err = repo.EffectiveLinks(ctx, 1, defaults)
	if err != nil || links.Group != "https://t.me/own_group" || links.Channel != defaults.Channel || links.NumberBot != defaults.NumberBot {
		t.Fatal("group link override changed other links", links, err)
	}
}

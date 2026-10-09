package telegram

import (
	"context"
	"strings"
	"testing"
)

func screenText(t *testing.T, f *fakeTelegram) string {
	t.Helper()
	return lastScreen(t, f).Params.Get("text")
}

func TestAdminUserManagerIntegration(t *testing.T) {
	a, f, _ := newIntegrationApp(t)
	ctx := context.Background()

	testCallback(a, 1, "admin:user:find")
	testText(a, 1, "2")
	if text := screenText(t, f); !strings.Contains(text, "User Profile") || !strings.Contains(text, "2") {
		t.Fatalf("profile screen = %q", text)
	}
	if markup := lastScreen(t, f).Params.Get("reply_markup"); !strings.Contains(markup, "admin:user:balance:2") || !strings.Contains(markup, "admin:user:ban:2:1") {
		t.Fatalf("profile actions missing: %s", markup)
	}

	// An admin without manage_users cannot adjust balances.
	testCallback(a, 3, "admin:user:balance:2")
	if text := screenText(t, f); !strings.Contains(text, "permission") {
		t.Fatalf("unauthorized adjust screen = %q", text)
	}

	f.reset()
	testCallback(a, 1, "admin:user:balance:2")
	testText(a, 1, "25 PKR welcome bonus")
	if pkr, _, _, _ := a.store.UserBalance(ctx, 2); pkr != 25 {
		t.Fatalf("balance after credit = %v", pkr)
	}
	notified := false
	for _, call := range f.snapshot() {
		if call.Params.Get("chat_id") == "2" && strings.Contains(call.Params.Get("text")+call.Params.Get("rich_message"), "Balance updated") {
			notified = true
		}
	}
	if !notified {
		t.Fatal("user was not notified about the balance change")
	}

	testCallback(a, 2, "menu:ledger:0")
	if text := screenText(t, f); !strings.Contains(text, "Admin adjustment") || !strings.Contains(text, "25.00 PKR") {
		t.Fatalf("transactions screen = %q", text)
	}

	testCallback(a, 1, "admin:user:banyes:2:1")
	if banned, _ := a.store.UserBlocked(ctx, 2); !banned {
		t.Fatal("ban did not apply")
	}
	testText(a, 2, "/start")
	if text := screenText(t, f); !strings.Contains(text, "suspended") {
		t.Fatalf("banned /start = %q", text)
	}
	testText(a, 1, "/unban 2")
	if banned, _ := a.store.UserBlocked(ctx, 2); banned {
		t.Fatal("unban command did not apply")
	}

	testCallback(a, 1, "admin:setting:maintenance")
	testText(a, 2, "/start")
	if text := screenText(t, f); !strings.Contains(text, "maintenance") {
		t.Fatalf("maintenance screen = %q", text)
	}
	testText(a, 1, "/start")
	if text := screenText(t, f); strings.Contains(text, "maintenance") {
		t.Fatal("admins must bypass maintenance mode")
	}
	testCallback(a, 1, "admin:setting:maintenance")

	testCallback(a, 1, "admin:setting:min_withdraw_pkr")
	testText(a, 1, "100")
	if pkr, _, err := a.store.MinimumWithdrawal(ctx, 1); err != nil || pkr != 100 {
		t.Fatalf("minimum = %v, %v", pkr, err)
	}
	testText(a, 2, "/withdraw 20|03001234567")
	if text := screenText(t, f); !strings.Contains(text, "minimum withdrawal") {
		t.Fatalf("minimum not enforced: %q", text)
	}
	if pkr, _, _, _ := a.store.UserBalance(ctx, 2); pkr != 25 {
		t.Fatalf("balance changed despite minimum: %v", pkr)
	}
}

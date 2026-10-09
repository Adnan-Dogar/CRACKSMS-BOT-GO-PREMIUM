package store_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

func TestAdminUserManagement(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	const admin, member = int64(9001), int64(9002)
	for id, name := range map[int64]string{admin: "boss", member: "Member_One"} {
		if err := repo.EnsureUserForInstance(ctx, store.MainBotInstanceID, id, name, "Name", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SeedAdmins(ctx, []int64{admin}); err != nil {
		t.Fatal(err)
	}

	byName, err := repo.FindUser(ctx, store.MainBotInstanceID, "@member_one")
	if err != nil || byName.UserID != member {
		t.Fatalf("lookup by username = %+v, %v", byName, err)
	}
	if _, err := repo.FindUser(ctx, store.MainBotInstanceID, "424242"); !store.IsMissing(err) {
		t.Fatalf("unknown user error = %v", err)
	}

	var input *store.InputError
	if err := repo.SetUserBanned(ctx, store.MainBotInstanceID, admin, admin, true, ""); !errors.As(err, &input) {
		t.Fatalf("self-ban error = %v", err)
	}
	if err := repo.SetUserBanned(ctx, store.MainBotInstanceID, member, admin, true, ""); !errors.As(err, &input) {
		t.Fatalf("banning an administrator must be refused, got %v", err)
	}
	if err := repo.SetUserBanned(ctx, store.MainBotInstanceID, admin, member, true, "spam"); err != nil {
		t.Fatal(err)
	}
	if banned, _ := repo.UserBlocked(ctx, member); !banned {
		t.Fatal("member was not banned")
	}
	if err := repo.SetUserBanned(ctx, store.MainBotInstanceID, admin, member, false, ""); err != nil {
		t.Fatal(err)
	}

	if balance, err := repo.AdjustBalance(ctx, store.MainBotInstanceID, admin, member, "pkr", 50, "bonus"); err != nil || balance != 50 {
		t.Fatalf("credit = %v, %v", balance, err)
	}
	if _, err := repo.AdjustBalance(ctx, store.MainBotInstanceID, admin, member, "PKR", -60, "too much"); !errors.As(err, &input) {
		t.Fatalf("overdraft error = %v", err)
	}
	for _, bad := range []float64{0, math.NaN(), math.Inf(1)} {
		if _, err := repo.AdjustBalance(ctx, store.MainBotInstanceID, admin, member, "PKR", bad, "x"); err == nil {
			t.Fatalf("amount %v accepted", bad)
		}
	}
	if balance, err := repo.AdjustBalance(ctx, store.MainBotInstanceID, admin, member, "PKR", -20, "correction"); err != nil || balance != 30 {
		t.Fatalf("debit = %v, %v", balance, err)
	}
	ledger, err := repo.UserLedger(ctx, member, 10, 0)
	if err != nil || len(ledger) != 2 || ledger[0].Amount != -20 || ledger[0].EntryType != "admin_adjustment" {
		t.Fatalf("ledger = %+v, %v", ledger, err)
	}

	if _, err := repo.CreateWithdrawalForInstance(ctx, store.MainBotInstanceID, member, "jazzcash", 10, 0, "03001234567"); err != nil {
		t.Fatal(err)
	}
	withdrawals, err := repo.UserWithdrawals(ctx, store.MainBotInstanceID, member, 10)
	if err != nil || len(withdrawals) != 1 || withdrawals[0].State != "pending" || withdrawals[0].Hint == "03001234567" {
		t.Fatalf("withdrawals = %+v, %v (details must stay masked)", withdrawals, err)
	}
	if profile, _ := repo.FindUser(ctx, store.MainBotInstanceID, "9002"); profile.PendingWithdrawals != 1 || profile.BalancePKR != 20 {
		t.Fatalf("profile = %+v", profile)
	}

	if err := repo.SetInstanceSetting(ctx, store.MainBotInstanceID, "min_withdraw_pkr", 100); err != nil {
		t.Fatal(err)
	}
	if pkr, usd, err := repo.MinimumWithdrawal(ctx, store.MainBotInstanceID); err != nil || pkr != 100 || usd != 0 {
		t.Fatalf("minimum = %v %v %v", pkr, usd, err)
	}
	if err := repo.SetInstanceSetting(ctx, store.MainBotInstanceID, "maintenance", true); err != nil {
		t.Fatal(err)
	}
	if on, _, err := repo.Maintenance(ctx, store.MainBotInstanceID); err != nil || !on {
		t.Fatalf("maintenance = %v %v", on, err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE target_id='9002'`).Scan(&audits); err != nil || audits < 4 {
		t.Fatalf("audit entries = %d, %v", audits, err)
	}
}

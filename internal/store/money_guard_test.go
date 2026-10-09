package store_test

import (
	"context"
	"math"
	"testing"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

func TestWithdrawalsRejectNonFiniteAndNegativeAmounts(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	if err := repo.EnsureUser(ctx, 7001, "payer", "Payer", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET balance_pkr=100,balance_usd=1 WHERE id=7001`); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ pkr, usd float64 }{
		{math.NaN(), 0}, {0, math.NaN()}, {math.Inf(1), 0}, {10, -5}, {-1, 0.5}, {0, 0},
	}
	for _, c := range cases {
		if _, err := repo.CreateWithdrawalForInstance(ctx, store.MainBotInstanceID, 7001, "PKR", c.pkr, c.usd, "03001234567"); err == nil {
			t.Fatalf("withdrawal pkr=%v usd=%v was accepted", c.pkr, c.usd)
		}
	}
	pkr, usd, _, err := repo.UserBalance(ctx, 7001)
	if err != nil || pkr != 100 || usd != 1 {
		t.Fatalf("balance changed: pkr=%v usd=%v err=%v", pkr, usd, err)
	}
	if _, err := repo.ReplaceRewardSchedule(ctx, "bad", nil, []domain.RewardRule{{Threshold: 5, AmountPKR: math.NaN()}}, 7001); err == nil {
		t.Fatal("NaN reward rule was accepted")
	}
}

func TestReferralOnlyAppliesToFreshAccounts(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	for _, id := range []int64{8001, 8002, 8003} {
		if err := repo.EnsureUser(ctx, id, "", "User", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET total_otps=50 WHERE id=8002`); err != nil {
		t.Fatal(err)
	}
	if err := repo.RegisterReferral(ctx, 8002, 8001); err != nil {
		t.Fatal(err)
	}
	if err := repo.RegisterReferral(ctx, 8003, 8001); err != nil {
		t.Fatal(err)
	}
	// A referred user cannot refer their own referrer back.
	if err := repo.RegisterReferral(ctx, 8001, 8003); err != nil {
		t.Fatal(err)
	}
	total, _, _, err := repo.ReferralStats(ctx, 8001)
	if err != nil || total != 1 {
		t.Fatalf("referrals for 8001 = %d, err=%v; want only the fresh account", total, err)
	}
	if total, _, _, _ = repo.ReferralStats(ctx, 8003); total != 0 {
		t.Fatalf("circular referral recorded")
	}
}

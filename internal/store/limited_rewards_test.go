package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
)

func TestLimitedUSDRewardPaysOnlyFirstTwoUsersOnce(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	backupAdmin(t, repo)
	if _, err := repo.ReplaceRewardSchedule(ctx, "First two", nil, []domain.RewardRule{{Threshold: 1, AmountUSD: 2, MaxUsers: 2}}, 1); err != nil {
		t.Fatal(err)
	}
	phones := make([]string, 6)
	for i := range phones {
		phones[i] = fmt.Sprintf("+12025550%03d", i+100)
	}
	if _, err := repo.AddNumbers(ctx, "WhatsApp", "United States", "US", 0, 0, 1, phones); err != nil {
		t.Fatal(err)
	}
	assigned := make([]domain.Assignment, 5)
	for i := range assigned {
		userID := int64(501 + i)
		if err := repo.EnsureUser(ctx, userID, "", "User", ""); err != nil {
			t.Fatal(err)
		}
		var err error
		assigned[i], err = repo.AssignNumbers(ctx, userID, "WhatsApp", "United States", 1, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan domain.AcceptedOTP, len(assigned))
	errors := make(chan error, len(assigned))
	for i, assignment := range assigned {
		wg.Add(1)
		go func(i int, assignment domain.Assignment) {
			defer wg.Done()
			result, err := repo.AcceptOTP(ctx, domain.OTPEvent{BotInstanceID: 1, Phone: assignment.Numbers[0].Phone, NormalizedPhone: assignment.Numbers[0].NormalizedPhone, Service: "WhatsApp", Message: fmt.Sprintf("Code %06d", i+100000), Code: fmt.Sprintf("%06d", i+100000), DedupKey: fmt.Sprintf("limited-%d", i), ReceivedAt: time.Now()})
			results <- result
			errors <- err
		}(i, assignment)
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	for result := range results {
		if !result.Counted {
			t.Fatal("OTP was not counted")
		}
		if result.RewardCreditUSD == 2 {
			winners++
		} else if result.RewardCreditUSD != 0 {
			t.Fatal("unexpected USD award", result.RewardCreditUSD)
		}
	}
	if winners != 2 {
		t.Fatal("wrong number of winners", winners)
	}
	var awardCount, ledgerCount int
	var totalUSD float64
	if err := pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(amount_usd),0) FROM reward_awards`).Scan(&awardCount, &totalUSD); err != nil || awardCount != 2 || totalUSD != 4 {
		t.Fatal("awards did not match cap", awardCount, totalUSD, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM balance_ledger WHERE entry_type='milestone_reward' AND currency='USD'`).Scan(&ledgerCount); err != nil || ledgerCount != 2 {
		t.Fatal("USD ledger entries missing", ledgerCount, err)
	}
	var winner int64
	if err := pool.QueryRow(ctx, `UPDATE reward_awards SET local_date=current_date-1 WHERE id=(SELECT min(id) FROM reward_awards) RETURNING user_id`).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	second, err := repo.AssignNumbers(ctx, winner, "WhatsApp", "United States", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := repo.AcceptOTP(ctx, domain.OTPEvent{BotInstanceID: 1, Phone: second.Numbers[0].Phone, NormalizedPhone: second.Numbers[0].NormalizedPhone, Service: "WhatsApp", Message: "Another code 654321", Code: "654321", DedupKey: "limited-repeat", ReceivedAt: time.Now()})
	if err != nil || !repeat.Counted || repeat.RewardCreditUSD != 0 {
		t.Fatal("limited award repeated for earlier winner", repeat, err)
	}
}

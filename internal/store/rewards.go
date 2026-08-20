package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ReplaceRewardSchedule(ctx context.Context, name string, userID *int64, rules []domain.RewardRule, createdBy int64) (int64, error) {
	if len(rules) == 0 {
		return 0, errors.New("at least one reward rule is required")
	}
	seen := map[int]bool{}
	for _, rule := range rules {
		if rule.Threshold <= 0 || rule.AmountPKR <= 0 {
			return 0, errors.New("reward threshold and amount must be positive")
		}
		if seen[rule.Threshold] {
			return 0, fmt.Errorf("duplicate threshold %d", rule.Threshold)
		}
		seen[rule.Threshold] = true
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if userID == nil {
		_, err = tx.Exec(ctx, `UPDATE reward_schedules SET enabled=false WHERE enabled AND user_id IS NULL`)
	} else {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id) VALUES($1) ON CONFLICT DO NOTHING`, *userID); err == nil {
			_, err = tx.Exec(ctx, `UPDATE reward_schedules SET enabled=false WHERE enabled AND user_id=$1`, *userID)
		}
	}
	if err != nil {
		return 0, err
	}
	var scheduleID int64
	err = tx.QueryRow(ctx, `INSERT INTO reward_schedules(name,user_id,created_by,effective_from)
		VALUES($1,$2,$3,$4) RETURNING id`, name, userID, createdBy, time.Now().In(s.location).Format("2006-01-02")).Scan(&scheduleID)
	if err != nil {
		return 0, err
	}
	for _, rule := range rules {
		if _, err := tx.Exec(ctx, `INSERT INTO reward_rules(schedule_id,threshold,reward_pkr) VALUES($1,$2,$3)`,
			scheduleID, rule.Threshold, rule.AmountPKR); err != nil {
			return 0, err
		}
	}
	return scheduleID, tx.Commit(ctx)
}

func (s *Store) RemoveUserRewardOverride(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE reward_schedules SET enabled=false WHERE user_id=$1 AND enabled`, userID)
	return err
}

func (s *Store) ListRewardSchedules(ctx context.Context) ([]domain.RewardSchedule, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,user_id,enabled,effective_from
		FROM reward_schedules WHERE enabled ORDER BY user_id NULLS FIRST,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RewardSchedule
	for rows.Next() {
		var schedule domain.RewardSchedule
		if err := rows.Scan(&schedule.ID, &schedule.Name, &schedule.UserID, &schedule.Enabled, &schedule.EffectiveFrom); err != nil {
			return nil, err
		}
		ruleRows, err := s.pool.Query(ctx, `SELECT threshold,reward_pkr FROM reward_rules WHERE schedule_id=$1 ORDER BY threshold`, schedule.ID)
		if err != nil {
			return nil, err
		}
		for ruleRows.Next() {
			var rule domain.RewardRule
			if err := ruleRows.Scan(&rule.Threshold, &rule.AmountPKR); err != nil {
				ruleRows.Close()
				return nil, err
			}
			schedule.Rules = append(schedule.Rules, rule)
		}
		ruleRows.Close()
		out = append(out, schedule)
	}
	return out, rows.Err()
}

func (s *Store) DailySummaries(ctx context.Context, date time.Time) ([]DailySummary, error) {
	dateText := date.In(s.location).Format("2006-01-02")
	rows, err := s.pool.Query(ctx, `SELECT p.user_id,u.first_name,p.otp_count,p.base_earnings_pkr,p.reward_earnings_pkr
		FROM user_daily_progress p JOIN users u ON u.id=p.user_id
		WHERE p.local_date=$1 AND p.otp_count>0 ORDER BY p.otp_count DESC`, dateText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailySummary
	for rows.Next() {
		var item DailySummary
		if err := rows.Scan(&item.UserID, &item.Name, &item.OTPCount, &item.BasePKR, &item.RewardPKR); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type DailySummary struct {
	UserID    int64
	Name      string
	OTPCount  int
	BasePKR   float64
	RewardPKR float64
}

func IsMissing(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

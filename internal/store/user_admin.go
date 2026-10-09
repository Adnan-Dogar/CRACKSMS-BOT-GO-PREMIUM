package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// UserProfile is the admin view of one account.
type UserProfile struct {
	UserID             int64
	Username           string
	FirstName          string
	LastName           string
	Tier               string
	Banned             bool
	BalancePKR         float64
	BalanceUSD         float64
	TotalOTPs          int64
	Referrals          int
	PendingWithdrawals int
	JoinedAt           time.Time
	LastActiveAt       time.Time
}

// FindUser resolves a numeric ID or @username to a user of this bot instance.
func (s *Store) FindUser(ctx context.Context, botInstanceID int64, query string) (UserProfile, error) {
	botInstanceID = instanceID(botInstanceID)
	query = strings.TrimPrefix(strings.TrimSpace(query), "@")
	if query == "" {
		return UserProfile{}, invalidInput("send a Telegram user ID or @username")
	}
	condition, arg := "u.id=$2", any(nil)
	if id, err := strconv.ParseInt(query, 10, 64); err == nil {
		arg = id
	} else {
		condition, arg = "lower(u.username)=lower($2)", query
	}
	var p UserProfile
	err := s.pool.QueryRow(ctx, `SELECT u.id,u.username,u.first_name,u.last_name,COALESCE(us.tier,'free'),u.banned,
		u.balance_pkr,u.balance_usd,u.total_otps,u.joined_at,u.last_active_at,
		(SELECT count(*) FROM referrals r WHERE r.referrer_id=u.id),
		(SELECT count(*) FROM withdrawals w WHERE w.user_id=u.id AND w.bot_instance_id=$1 AND w.state='pending')
		FROM bot_instance_users iu JOIN users u ON u.id=iu.user_id
		LEFT JOIN user_subscriptions us ON us.bot_instance_id=iu.bot_instance_id AND us.user_id=u.id
			AND us.status='active' AND (us.expires_at IS NULL OR us.expires_at>now())
		WHERE iu.bot_instance_id=$1 AND `+condition+` LIMIT 1`, botInstanceID, arg).
		Scan(&p.UserID, &p.Username, &p.FirstName, &p.LastName, &p.Tier, &p.Banned, &p.BalancePKR, &p.BalanceUSD,
			&p.TotalOTPs, &p.JoinedAt, &p.LastActiveAt, &p.Referrals, &p.PendingWithdrawals)
	return p, err
}

// SetUserBanned blocks or restores an account and records who did it.
func (s *Store) SetUserBanned(ctx context.Context, botInstanceID, adminID, userID int64, banned bool, reason string) error {
	if userID == adminID {
		return invalidInput("you cannot ban your own account")
	}
	if banned {
		var isAdmin bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins WHERE user_id=$1)`, userID).Scan(&isAdmin); err != nil {
			return err
		}
		if isAdmin {
			return invalidInput("remove the administrator role before banning this account")
		}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE users SET banned=$2 WHERE id=$1`, userID, banned)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	action := "user.unban"
	if banned {
		action = "user.ban"
	}
	return s.Audit(ctx, botInstanceID, adminID, action, "user", fmt.Sprint(userID), map[string]string{"reason": reason})
}

// AdjustBalance credits (positive) or debits (negative) a balance with a
// ledger entry. Debits never take a balance below zero.
func (s *Store) AdjustBalance(ctx context.Context, botInstanceID, adminID, userID int64, currency string, amount float64, reason string) (float64, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency != "PKR" && currency != "USD" {
		return 0, invalidInput("currency must be PKR or USD")
	}
	if amount == 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || math.Abs(amount) > 1000000 {
		return 0, invalidInput("amount must be a non-zero number up to 1000000")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 200 {
		return 0, invalidInput("give a short reason (up to 200 characters)")
	}
	column := "balance_pkr"
	if currency == "USD" {
		column = "balance_usd"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var balance float64
	if err := tx.QueryRow(ctx, `SELECT `+column+` FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&balance); err != nil {
		return 0, err
	}
	if balance+amount < 0 {
		return 0, invalidInput(fmt.Sprintf("debit exceeds the current balance of %.4f %s", balance, currency))
	}
	if err := tx.QueryRow(ctx, `UPDATE users SET `+column+`=`+column+`+$2 WHERE id=$1 RETURNING `+column, userID, amount).Scan(&balance); err != nil {
		return 0, err
	}
	reference := newUUID()
	if _, err := tx.Exec(ctx, `INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id)
		VALUES($1,$2,$3,'admin_adjustment','admin',$4)`, userID, currency, amount, reference); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	_ = s.Audit(ctx, botInstanceID, adminID, "user.balance_adjust", "user", fmt.Sprint(userID),
		map[string]any{"currency": currency, "amount": amount, "reason": reason, "ledger_reference": reference})
	return balance, nil
}

// LedgerEntry is one balance movement.
type LedgerEntry struct {
	Currency  string
	Amount    float64
	EntryType string
	CreatedAt time.Time
}

func (s *Store) UserLedger(ctx context.Context, userID int64, limit, offset int) ([]LedgerEntry, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `SELECT currency,amount,entry_type,created_at FROM balance_ledger
		WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, userID, limit, max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		var item LedgerEntry
		if err := rows.Scan(&item.Currency, &item.Amount, &item.EntryType, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// UserWithdrawal is a user's own view of a request; payout details stay masked.
type UserWithdrawal struct {
	ID         int64
	Method     string
	AmountPKR  float64
	AmountUSD  float64
	Hint       string
	State      string
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

func (s *Store) UserWithdrawals(ctx context.Context, botInstanceID, userID int64, limit int) ([]UserWithdrawal, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `SELECT id,method,amount_pkr,amount_usd,details,state::text,created_at,resolved_at
		FROM withdrawals WHERE bot_instance_id=$1 AND user_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3`,
		instanceID(botInstanceID), userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserWithdrawal
	for rows.Next() {
		var item UserWithdrawal
		if err := rows.Scan(&item.ID, &item.Method, &item.AmountPKR, &item.AmountUSD, &item.Hint, &item.State, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// MinimumWithdrawal returns the configured minimum per currency (0 = none).
func (s *Store) MinimumWithdrawal(ctx context.Context, botInstanceID int64) (pkr, usd float64, err error) {
	values, err := s.InstanceSettings(ctx, botInstanceID)
	if err != nil {
		return 0, 0, err
	}
	if v, ok := values["min_withdraw_pkr"].(float64); ok && validAmount(v) {
		pkr = v
	}
	if v, ok := values["min_withdraw_usd"].(float64); ok && validAmount(v) {
		usd = v
	}
	return pkr, usd, nil
}

// Maintenance reports whether the bot is paused for regular users.
func (s *Store) Maintenance(ctx context.Context, botInstanceID int64) (bool, string, error) {
	values, err := s.InstanceSettings(ctx, botInstanceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return false, "", err
	}
	enabled, _ := values["maintenance"].(bool)
	message, _ := values["maintenance_message"].(string)
	return enabled, message, nil
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) AddNumbers(ctx context.Context, service, country, countryCode string, pricePKR, priceUSD float64, perCycle int, phones []string) (int, error) {
	service, country = strings.TrimSpace(service), strings.TrimSpace(country)
	if service == "" || country == "" {
		return 0, invalidInput("service and country are required")
	}
	if perCycle <= 0 {
		perCycle = 3
	}
	settings := ImportSettings{Services: []ImportService{{Name: service, PricePKR: pricePKR, PriceUSD: priceUSD, PerCycle: perCycle}}}
	if err := validateImportSettings(settings); err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, strings.ToLower(service)); err != nil {
		return 0, err
	}
	var canonical string
	if e := tx.QueryRow(ctx, `SELECT service FROM service_countries WHERE lower(btrim(service))=lower($1) ORDER BY service LIMIT 1`, service).Scan(&canonical); e == nil {
		service = canonical
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return 0, e
	}
	_, err = tx.Exec(ctx, `INSERT INTO service_countries(service,country,country_code,price_pkr,price_usd,numbers_per_cycle) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(service,country) DO UPDATE SET country_code=EXCLUDED.country_code,price_pkr=EXCLUDED.price_pkr,price_usd=EXCLUDED.price_usd,numbers_per_cycle=EXCLUDED.numbers_per_cycle,enabled=true`, service, country, countryCode, pricePKR, priceUSD, perCycle)
	if err != nil {
		return 0, err
	}
	if len(phones) == 0 {
		return 0, tx.Commit(ctx)
	}
	numbers := make([]ImportNumber, 0, len(phones))
	for _, phone := range phones {
		numbers = append(numbers, ImportNumber{Phone: phone, Country: country, CountryCode: countryCode})
	}
	settings.Services[0].Name = service
	results, err := s.bulkImportNumbers(ctx, tx, 0, 0, settings, numbers, true)
	if err != nil {
		return 0, err
	}
	return results[0].Added, tx.Commit(ctx)
}

func (s *Store) Catalog(ctx context.Context) (map[string][]CatalogCountry, error) {
	return s.CatalogForInstance(ctx, MainBotInstanceID)
}

func (s *Store) CatalogForInstance(ctx context.Context, botInstanceID int64) (map[string][]CatalogCountry, error) {
	rows, err := s.pool.Query(ctx, `SELECT sc.service,sc.country,sc.country_code,sc.price_pkr,sc.price_usd,sc.numbers_per_cycle,
		count(n.id),COALESCE(sp.custom_emoji_id,'')
		FROM service_countries sc LEFT JOIN numbers n ON n.service=sc.service AND n.country=sc.country AND n.state='available'
		LEFT JOIN service_profiles sp ON sp.bot_instance_id=$1 AND sp.service_key=lower(sc.service)
		WHERE sc.enabled GROUP BY sc.service,sc.country,sc.country_code,sc.price_pkr,sc.price_usd,sc.numbers_per_cycle,sp.custom_emoji_id
		ORDER BY sc.service,sc.country`, instanceID(botInstanceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]CatalogCountry{}
	for rows.Next() {
		var service string
		var item CatalogCountry
		if err := rows.Scan(&service, &item.Country, &item.CountryCode, &item.PricePKR, &item.PriceUSD, &item.PerCycle, &item.Available, &item.CustomEmojiID); err != nil {
			return nil, err
		}
		out[service] = append(out[service], item)
	}
	return out, rows.Err()
}

type CatalogCountry struct {
	Country       string
	CountryCode   string
	PricePKR      float64
	PriceUSD      float64
	PerCycle      int
	Available     int
	CustomEmojiID string
}

type Withdrawal struct {
	ID        int64
	UserID    int64
	AccountID int64
	Method    string
	AmountPKR float64
	AmountUSD float64
	Details   string
	State     string
}

func (s *Store) ListPendingWithdrawals(ctx context.Context, botInstanceID int64, limit int) ([]Withdrawal, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `SELECT w.id,w.user_id,COALESCE(w.account_id,0),w.method,w.amount_pkr,w.amount_usd,w.details,w.details_config,w.state
		FROM withdrawals w WHERE w.bot_instance_id=$1 AND w.state='pending' AND EXISTS(
			SELECT 1 FROM bot_instance_users iu WHERE iu.bot_instance_id=w.bot_instance_id AND iu.user_id=w.user_id)
		ORDER BY w.created_at,w.id LIMIT $2`, instanceID(botInstanceID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Withdrawal
	for rows.Next() {
		var item Withdrawal
		var encryptedDetails []byte
		if err := rows.Scan(&item.ID, &item.UserID, &item.AccountID, &item.Method, &item.AmountPKR, &item.AmountUSD, &item.Details, &encryptedDetails, &item.State); err != nil {
			return nil, err
		}
		if len(encryptedDetails) > 0 {
			item.Details, err = decryptEnvelope(encryptedDetails, s.cipher.Decrypt)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateWithdrawal(ctx context.Context, userID int64, method string, amountPKR, amountUSD float64, details string) (int64, error) {
	return s.CreateWithdrawalForInstance(ctx, MainBotInstanceID, userID, method, amountPKR, amountUSD, details)
}

func (s *Store) CreateWithdrawalForInstance(ctx context.Context, botInstanceID, userID int64, method string, amountPKR, amountUSD float64, details string) (int64, error) {
	return s.createWithdrawalForInstance(ctx, botInstanceID, userID, 0, method, amountPKR, amountUSD, maskStoredDetails(details), details)
}

func (s *Store) CreateWithdrawalForAccount(ctx context.Context, botInstanceID, userID, accountID int64, amountPKR, amountUSD float64) (int64, error) {
	account, err := s.WithdrawalAccount(ctx, botInstanceID, userID, accountID)
	if err != nil {
		return 0, err
	}
	return s.createWithdrawalForInstance(ctx, botInstanceID, userID, account.ID, account.Method, amountPKR, amountUSD, account.DisplayHint, account.Details)
}

func (s *Store) createWithdrawalForInstance(ctx context.Context, botInstanceID, userID, accountID int64, method string, amountPKR, amountUSD float64, detailsHint, details string) (int64, error) {
	if !validAmount(amountPKR) || !validAmount(amountUSD) || (amountPKR == 0 && amountUSD == 0) {
		return 0, invalidInput("withdrawal amount must be positive")
	}
	encrypted, err := s.cipher.Encrypt([]byte(details))
	if err != nil {
		return 0, err
	}
	detailsConfig, err := json.Marshal(map[string]string{"encrypted": encrypted})
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var pkr, usd float64
	if err := tx.QueryRow(ctx, `SELECT balance_pkr,balance_usd FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&pkr, &usd); err != nil {
		return 0, err
	}
	if amountPKR > pkr || amountUSD > usd {
		return 0, invalidInput("insufficient balance")
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO withdrawals(bot_instance_id,user_id,account_id,method,amount_pkr,amount_usd,details,details_config)
		VALUES($1,$2,NULLIF($3::bigint,0),$4,$5,$6,$7,$8) RETURNING id`, instanceID(botInstanceID), userID, accountID, method, amountPKR, amountUSD, detailsHint, detailsConfig).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET balance_pkr=balance_pkr-$2,balance_usd=balance_usd-$3 WHERE id=$1`, userID, amountPKR, amountUSD); err != nil {
		return 0, err
	}
	if amountPKR > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id)
			VALUES($1,'PKR',$2,'withdrawal_hold','withdrawal',$3)`, userID, -amountPKR, fmt.Sprint(id)); err != nil {
			return 0, err
		}
	}
	if amountUSD > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id)
			VALUES($1,'USD',$2,'withdrawal_hold','withdrawal',$3)`, userID, -amountUSD, fmt.Sprint(id)); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

func (s *Store) WithdrawalForInstance(ctx context.Context, botInstanceID, id int64) (Withdrawal, error) {
	var item Withdrawal
	var encryptedDetails []byte
	err := s.pool.QueryRow(ctx, `SELECT id,user_id,COALESCE(account_id,0),method,amount_pkr,amount_usd,details,details_config,state
		FROM withdrawals WHERE bot_instance_id=$1 AND id=$2`, instanceID(botInstanceID), id).
		Scan(&item.ID, &item.UserID, &item.AccountID, &item.Method, &item.AmountPKR, &item.AmountUSD, &item.Details, &encryptedDetails, &item.State)
	if err == nil && len(encryptedDetails) > 0 {
		item.Details, err = decryptEnvelope(encryptedDetails, s.cipher.Decrypt)
	}
	return item, err
}

func maskStoredDetails(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= 6 {
		return strings.Repeat("•", len(runes))
	}
	return string(runes[:3]) + "•••" + string(runes[len(runes)-3:])
}

func (s *Store) ResolveWithdrawal(ctx context.Context, id, adminID int64, approve bool) error {
	return s.ResolveWithdrawalForInstance(ctx, MainBotInstanceID, id, adminID, approve)
}

func (s *Store) ResolveWithdrawalForInstance(ctx context.Context, botInstanceID, id, adminID int64, approve bool) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var userID int64
	var pkr, usd float64
	if err := tx.QueryRow(ctx, `SELECT user_id,amount_pkr,amount_usd FROM withdrawals
		WHERE bot_instance_id=$1 AND id=$2 AND state='pending' FOR UPDATE`, instanceID(botInstanceID), id).Scan(&userID, &pkr, &usd); err != nil {
		return err
	}
	state := "approved"
	if !approve {
		state = "rejected"
		if _, err := tx.Exec(ctx, `UPDATE users SET balance_pkr=balance_pkr+$2,balance_usd=balance_usd+$3 WHERE id=$1`, userID, pkr, usd); err != nil {
			return err
		}
		if pkr > 0 {
			_, err = tx.Exec(ctx, `INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id)
				VALUES($1,'PKR',$2,'withdrawal_refund','withdrawal',$3)`, userID, pkr, fmt.Sprint(id))
		}
		if err == nil && usd > 0 {
			_, err = tx.Exec(ctx, `INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id)
				VALUES($1,'USD',$2,'withdrawal_refund','withdrawal',$3)`, userID, usd, fmt.Sprint(id))
		}
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE withdrawals SET state=$3,resolved_at=now(),resolved_by=$4 WHERE bot_instance_id=$1 AND id=$2`, instanceID(botInstanceID), id, state, adminID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) TopUsers(ctx context.Context, limit int) ([]TopUser, error) {
	return s.TopUsersForInstance(ctx, MainBotInstanceID, limit)
}

func (s *Store) TopUsersForInstance(ctx context.Context, botInstanceID int64, limit int) ([]TopUser, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id,u.username,u.first_name,count(e.id),u.balance_pkr
		FROM bot_instance_users iu JOIN users u ON u.id=iu.user_id
		LEFT JOIN otp_events e ON e.bot_instance_id=iu.bot_instance_id AND e.assigned_user_id=u.id AND e.counted
		WHERE iu.bot_instance_id=$1 AND NOT u.banned GROUP BY u.id,u.username,u.first_name,u.balance_pkr
		ORDER BY count(e.id) DESC,u.id LIMIT $2`, instanceID(botInstanceID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopUser
	for rows.Next() {
		var item TopUser
		if err := rows.Scan(&item.UserID, &item.Username, &item.FirstName, &item.TotalOTPs, &item.BalancePKR); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type TopUser struct {
	UserID     int64
	Username   string
	FirstName  string
	TotalOTPs  int64
	BalancePKR float64
}

func SortedServices(catalog map[string][]CatalogCountry) []string {
	services := make([]string, 0, len(catalog))
	for service := range catalog {
		services = append(services, service)
	}
	sort.Strings(services)
	return services
}

// TodayOTPCount returns the user's counted OTPs for the current local day.
func (s *Store) TodayOTPCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT otp_count FROM user_daily_progress WHERE user_id=$1 AND local_date=$2::date),0)`,
		userID, time.Now().In(s.location).Format("2006-01-02")).Scan(&count)
	return count, err
}

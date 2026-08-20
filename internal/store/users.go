package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) AddNumbers(ctx context.Context, service, country, countryCode string, pricePKR, priceUSD float64, perCycle int, phones []string) (int, error) {
	if service == "" || country == "" {
		return 0, errors.New("service and country are required")
	}
	if perCycle <= 0 {
		perCycle = 3
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO service_countries(service,country,country_code,price_pkr,price_usd,numbers_per_cycle)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(service,country) DO UPDATE SET country_code=EXCLUDED.country_code,
		price_pkr=EXCLUDED.price_pkr,price_usd=EXCLUDED.price_usd,numbers_per_cycle=EXCLUDED.numbers_per_cycle,enabled=true`,
		service, country, countryCode, pricePKR, priceUSD, perCycle)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, phone := range phones {
		phone = strings.TrimSpace(phone)
		normalized := NormalizePhone(phone)
		if len(normalized) < 5 || len(normalized) > 20 {
			continue
		}
		tag, err := tx.Exec(ctx, `INSERT INTO numbers(phone,normalized_phone,service,country)
			VALUES($1,$2,$3,$4) ON CONFLICT(normalized_phone) DO NOTHING`, phone, normalized, service, country)
		if err != nil {
			return 0, err
		}
		added += int(tag.RowsAffected())
	}
	return added, tx.Commit(ctx)
}

func (s *Store) Catalog(ctx context.Context) (map[string][]CatalogCountry, error) {
	rows, err := s.pool.Query(ctx, `SELECT sc.service,sc.country,sc.country_code,sc.price_pkr,sc.numbers_per_cycle,count(n.id)
		FROM service_countries sc LEFT JOIN numbers n ON n.service=sc.service AND n.country=sc.country AND n.state='available'
		WHERE sc.enabled GROUP BY sc.service,sc.country,sc.country_code,sc.price_pkr,sc.numbers_per_cycle
		ORDER BY sc.service,sc.country`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]CatalogCountry{}
	for rows.Next() {
		var service string
		var item CatalogCountry
		if err := rows.Scan(&service, &item.Country, &item.CountryCode, &item.PricePKR, &item.PerCycle, &item.Available); err != nil {
			return nil, err
		}
		out[service] = append(out[service], item)
	}
	return out, rows.Err()
}

type CatalogCountry struct {
	Country     string
	CountryCode string
	PricePKR    float64
	PerCycle    int
	Available   int
}

func (s *Store) CreateWithdrawal(ctx context.Context, userID int64, method string, amountPKR, amountUSD float64, details string) (int64, error) {
	if amountPKR <= 0 && amountUSD <= 0 {
		return 0, errors.New("withdrawal amount must be positive")
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
		return 0, errors.New("insufficient balance")
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO withdrawals(user_id,method,amount_pkr,amount_usd,details)
		VALUES($1,$2,$3,$4,$5) RETURNING id`, userID, method, amountPKR, amountUSD, details).Scan(&id); err != nil {
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

func (s *Store) ResolveWithdrawal(ctx context.Context, id, adminID int64, approve bool) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var userID int64
	var pkr, usd float64
	if err := tx.QueryRow(ctx, `SELECT user_id,amount_pkr,amount_usd FROM withdrawals
		WHERE id=$1 AND state='pending' FOR UPDATE`, id).Scan(&userID, &pkr, &usd); err != nil {
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
	if _, err := tx.Exec(ctx, `UPDATE withdrawals SET state=$2,resolved_at=now(),resolved_by=$3 WHERE id=$1`, id, state, adminID); err != nil {
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

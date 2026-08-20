package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/secure"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNoNumbers = errors.New("no numbers available")

type Store struct {
	pool     *pgxpool.Pool
	location *time.Location
	cipher   *secure.Cipher
}

func New(pool *pgxpool.Pool, location *time.Location, cipher *secure.Cipher) *Store {
	return &Store{pool: pool, location: location, cipher: cipher}
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) EnsureUser(ctx context.Context, id int64, username, firstName, lastName string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users(id,username,first_name,last_name,last_active_at)
		VALUES($1,$2,$3,$4,now())
		ON CONFLICT(id) DO UPDATE SET username=EXCLUDED.username,
		first_name=EXCLUDED.first_name,last_name=EXCLUDED.last_name,last_active_at=now()`,
		id, username, firstName, lastName)
	return err
}

func (s *Store) SeedAdmins(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if _, err := s.pool.Exec(ctx, `INSERT INTO users(id) VALUES($1) ON CONFLICT DO NOTHING`, id); err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO admins(user_id) VALUES($1) ON CONFLICT DO NOTHING`, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) IsAdmin(ctx context.Context, id int64, permission string) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM admins WHERE user_id=$1 AND ('*'=ANY(permissions) OR $2=ANY(permissions)))`, id, permission).Scan(&allowed)
	return allowed, err
}

func (s *Store) UserBalance(ctx context.Context, id int64) (float64, float64, int64, error) {
	var pkr, usd float64
	var total int64
	err := s.pool.QueryRow(ctx, `SELECT balance_pkr,balance_usd,total_otps FROM users WHERE id=$1`, id).Scan(&pkr, &usd, &total)
	return pkr, usd, total, err
}

func (s *Store) AssignNumbers(ctx context.Context, userID int64, service, country string, limit int, holdDuration time.Duration) (domain.Assignment, error) {
	return s.AssignNumbersForInstance(ctx, MainBotInstanceID, userID, service, country, limit, holdDuration)
}

func (s *Store) AssignNumbersForInstance(ctx context.Context, botInstanceID, userID int64, service, country string, limit int, holdDuration time.Duration) (domain.Assignment, error) {
	botInstanceID = instanceID(botInstanceID)
	if limit <= 0 {
		return domain.Assignment{}, errors.New("assignment limit must be positive")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.Assignment{}, err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `INSERT INTO users(id) VALUES($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return domain.Assignment{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT n.id,n.phone,n.normalized_phone,n.service,n.country
		FROM numbers n
		WHERE n.service=$1 AND n.country=$2 AND n.state='available'
		  AND NOT EXISTS (
		    SELECT 1 FROM number_user_exclusions e
		    WHERE e.number_id=n.id AND e.user_id=$3 AND e.expires_at>now())
		ORDER BY n.id
		FOR UPDATE SKIP LOCKED LIMIT $4`, service, country, userID, limit)
	if err != nil {
		return domain.Assignment{}, err
	}
	var numbers []domain.Number
	for rows.Next() {
		var n domain.Number
		if err := rows.Scan(&n.ID, &n.Phone, &n.NormalizedPhone, &n.Service, &n.Country); err != nil {
			rows.Close()
			return domain.Assignment{}, err
		}
		numbers = append(numbers, n)
	}
	rows.Close()
	if len(numbers) == 0 {
		return domain.Assignment{}, ErrNoNumbers
	}

	id := newUUID()
	expiresAt := time.Now().Add(holdDuration)
	if _, err = tx.Exec(ctx, `INSERT INTO assignments(id,bot_instance_id,user_id,service,country,expires_at)
		VALUES($1,$2,$3,$4,$5,$6)`, id, botInstanceID, userID, service, country, expiresAt); err != nil {
		return domain.Assignment{}, err
	}
	for _, number := range numbers {
		if _, err = tx.Exec(ctx, `UPDATE numbers SET state='assigned',updated_at=now() WHERE id=$1`, number.ID); err != nil {
			return domain.Assignment{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO assignment_numbers(assignment_id,number_id) VALUES($1,$2)`, id, number.ID); err != nil {
			return domain.Assignment{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Assignment{}, err
	}
	return domain.Assignment{ID: id, BotInstanceID: botInstanceID, UserID: userID, Service: service, Country: country, Numbers: numbers, ExpiresAt: expiresAt}, nil
}

func (s *Store) AcceptOTP(ctx context.Context, event domain.OTPEvent) (domain.AcceptedOTP, error) {
	event.BotInstanceID = instanceID(event.BotInstanceID)
	if event.ID == "" {
		event.ID = newUUID()
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now()
	}
	if event.DedupKey == "" || event.NormalizedPhone == "" || event.Message == "" {
		return domain.AcceptedOTP{}, errors.New("dedup key, normalized phone, and message are required")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.AcceptedOTP{}, err
	}
	defer tx.Rollback(ctx)

	var insertedID string
	err = tx.QueryRow(ctx, `INSERT INTO otp_events(
		id,dedup_key,bot_instance_id,panel_id,panel_name,phone,normalized_phone,service,country,message,code,provider_timestamp,received_at)
		VALUES($1,$2,$3,NULLIF($4,0),$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT(dedup_key) DO NOTHING RETURNING id`, event.ID, event.DedupKey, event.BotInstanceID, event.PanelID,
		event.PanelName, event.Phone, event.NormalizedPhone, event.Service, event.Country, event.Message, event.Code,
		event.ProviderTimestamp, event.ReceivedAt).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AcceptedOTP{Duplicate: true}, nil
	}
	if err != nil {
		return domain.AcceptedOTP{}, err
	}

	result := domain.AcceptedOTP{EventID: event.ID}
	var assignmentID string
	var numberID, userID int64
	var basePrice float64
	var assignedService, assignedCountry string
	err = tx.QueryRow(ctx, `
		SELECT a.id,n.id,a.user_id,sc.price_pkr,n.service,n.country
		FROM numbers n
		JOIN assignment_numbers an ON an.number_id=n.id
		JOIN assignments a ON a.id=an.assignment_id
		JOIN service_countries sc ON sc.service=n.service AND sc.country=n.country
		WHERE n.normalized_phone=$1 AND a.bot_instance_id=$2 AND n.state='assigned'
		  AND a.state='active' AND a.expires_at>now()
		  AND an.consumed_at IS NULL AND an.released_at IS NULL
		ORDER BY a.assigned_at DESC LIMIT 1
		FOR UPDATE OF n,an,a`, event.NormalizedPhone, event.BotInstanceID).Scan(&assignmentID, &numberID, &userID, &basePrice, &assignedService, &assignedCountry)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.AcceptedOTP{}, err
	}
	if err == nil {
		result.AssignedUserID = userID
		result.Counted = true
		result.BaseCreditPKR = basePrice
		event.Service, event.Country = assignedService, assignedCountry
		if _, err = tx.Exec(ctx, `UPDATE assignment_numbers SET consumed_at=now(),otp_event_id=$1
			WHERE assignment_id=$2 AND number_id=$3 AND consumed_at IS NULL`, event.ID, assignmentID, numberID); err != nil {
			return domain.AcceptedOTP{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE numbers SET state='consumed',updated_at=now() WHERE id=$1`, numberID); err != nil {
			return domain.AcceptedOTP{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE otp_events SET assigned_user_id=$1,counted=true,service=$3,country=$4 WHERE id=$2`,
			userID, event.ID, event.Service, event.Country); err != nil {
			return domain.AcceptedOTP{}, err
		}

		localDate := event.ReceivedAt.In(s.location).Format("2006-01-02")
		if err = tx.QueryRow(ctx, `
			INSERT INTO user_daily_progress(user_id,local_date,otp_count,base_earnings_pkr)
			VALUES($1,$2,1,$3)
			ON CONFLICT(user_id,local_date) DO UPDATE SET
			otp_count=user_daily_progress.otp_count+1,
			base_earnings_pkr=user_daily_progress.base_earnings_pkr+EXCLUDED.base_earnings_pkr
			RETURNING otp_count`, userID, localDate, basePrice).Scan(&result.DailyCount); err != nil {
			return domain.AcceptedOTP{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id)
			VALUES($1,'PKR',$2,'otp_earning','otp_event',$3)`, userID, basePrice, event.ID); err != nil {
			return domain.AcceptedOTP{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET balance_pkr=balance_pkr+$2,total_otps=total_otps+1 WHERE id=$1`, userID, basePrice); err != nil {
			return domain.AcceptedOTP{}, err
		}
		if err = s.awardReferralIfQualified(ctx, tx, userID); err != nil {
			return domain.AcceptedOTP{}, err
		}

		rewards, rewardTotal, err := s.awardReachedMilestones(ctx, tx, userID, localDate, result.DailyCount)
		if err != nil {
			return domain.AcceptedOTP{}, err
		}
		result.TriggeredRewards = rewards
		result.RewardCreditPKR = rewardTotal
		if _, err = tx.Exec(ctx, `INSERT INTO delivery_jobs(
			otp_event_id,bot_instance_id,target_kind,target_id,buttons_enabled,theme_id,otp_visibility)
			SELECT $1,$2,'user',$3,true,COALESCE(up.theme_id,bi.default_theme),'visible'
			FROM bot_instances bi LEFT JOIN user_preferences up
			  ON up.bot_instance_id=bi.id AND up.user_id=$3 WHERE bi.id=$2
			ON CONFLICT DO NOTHING`, event.ID, event.BotInstanceID, userID); err != nil {
			return domain.AcceptedOTP{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(endpoint_id,otp_event_id,event_name)
			SELECT id,$1,'otp.received' FROM webhook_endpoints
			WHERE bot_instance_id=$2 AND user_id=$3 AND enabled AND 'otp.received'=ANY(events)
			ON CONFLICT DO NOTHING`, event.ID, event.BotInstanceID, userID); err != nil {
			return domain.AcceptedOTP{}, err
		}
	}

	if _, err = tx.Exec(ctx, `INSERT INTO delivery_jobs(
		otp_event_id,bot_instance_id,target_kind,target_id,buttons_enabled,theme_id,otp_visibility)
		SELECT $1,g.bot_instance_id,'group',g.chat_id,g.buttons_enabled,
			COALESCE(g.theme_id,bi.default_theme),g.otp_visibility
		FROM otp_group_destinations g JOIN bot_instances bi ON bi.id=g.bot_instance_id
		WHERE g.enabled AND g.bot_instance_id=$2
		ON CONFLICT DO NOTHING`, event.ID, event.BotInstanceID); err != nil {
		return domain.AcceptedOTP{}, err
	}
	if event.PanelID != 0 {
		_, _ = tx.Exec(ctx, `UPDATE panels SET otp_count=otp_count+1 WHERE id=$1`, event.PanelID)
	}
	if userID != 0 {
		if err = tx.QueryRow(ctx, `SELECT balance_pkr FROM users WHERE id=$1`, userID).Scan(&result.NewBalancePKR); err != nil {
			return domain.AcceptedOTP{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AcceptedOTP{}, err
	}
	return result, nil
}

func (s *Store) awardReferralIfQualified(ctx context.Context, tx pgx.Tx, userID int64) error {
	var milestone int
	var reward float64
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT (value #>> '{}')::int FROM settings WHERE key='referral_milestone'),20),
		COALESCE((SELECT (value #>> '{}')::numeric FROM settings WHERE key='referral_reward_pkr'),10)`).Scan(&milestone, &reward); err != nil {
		return err
	}
	var referrerID int64
	err := tx.QueryRow(ctx, `UPDATE users SET referral_reward_given=true
		WHERE id=$1 AND referred_by IS NOT NULL AND NOT referral_reward_given AND total_otps>=$2
		RETURNING referred_by`, userID, milestone).Scan(&referrerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET balance_pkr=balance_pkr+$2 WHERE id=$1`, referrerID, reward); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE referrals SET qualified=true,reward_pkr=$2 WHERE user_id=$1`, userID, reward); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id)
		VALUES($1,'PKR',$2,'referral_reward','referred_user',$3) ON CONFLICT DO NOTHING`,
		referrerID, reward, fmt.Sprint(userID))
	return err
}

func (s *Store) awardReachedMilestones(ctx context.Context, tx pgx.Tx, userID int64, localDate string, count int) ([]domain.RewardAward, float64, error) {
	rows, err := tx.Query(ctx, `
		WITH selected AS (
		  SELECT id FROM reward_schedules
		  WHERE enabled AND effective_from<=$2::date AND (user_id=$1 OR user_id IS NULL)
		  ORDER BY (user_id IS NOT NULL) DESC, id DESC LIMIT 1)
		SELECT rr.id,rr.threshold,rr.reward_pkr
		FROM reward_rules rr JOIN selected s ON s.id=rr.schedule_id
		WHERE rr.threshold<=$3 ORDER BY rr.threshold`, userID, localDate, count)
	if err != nil {
		return nil, 0, err
	}
	type reachedRule struct {
		id        int64
		threshold int
		amount    float64
	}
	var rules []reachedRule
	for rows.Next() {
		var rule reachedRule
		if err := rows.Scan(&rule.id, &rule.threshold, &rule.amount); err != nil {
			rows.Close()
			return nil, 0, err
		}
		rules = append(rules, rule)
	}
	rows.Close()
	var awards []domain.RewardAward
	var total float64
	for _, rule := range rules {
		var awardID int64
		err := tx.QueryRow(ctx, `INSERT INTO reward_awards(user_id,local_date,rule_id,otp_count,amount_pkr)
			VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING id`,
			userID, localDate, rule.id, count, rule.amount).Scan(&awardID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO balance_ledger(user_id,currency,amount,entry_type,reference_type,reference_id)
			VALUES($1,'PKR',$2,'milestone_reward','reward_award',$3)`, userID, rule.amount, fmt.Sprint(awardID)); err != nil {
			return nil, 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET balance_pkr=balance_pkr+$2 WHERE id=$1`, userID, rule.amount); err != nil {
			return nil, 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE user_daily_progress SET reward_earnings_pkr=reward_earnings_pkr+$3
			WHERE user_id=$1 AND local_date=$2`, userID, localDate, rule.amount); err != nil {
			return nil, 0, err
		}
		awards = append(awards, domain.RewardAward{Threshold: rule.threshold, AmountPKR: rule.amount})
		total += rule.amount
	}
	return awards, total, nil
}

func (s *Store) RecycleExpiredAssignments(ctx context.Context, reuseCooldown time.Duration, batch int) (domain.RecycleResult, error) {
	if batch <= 0 {
		batch = 200
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.RecycleResult{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,user_id FROM assignments
		WHERE state='active' AND expires_at<=now() ORDER BY expires_at
		FOR UPDATE SKIP LOCKED LIMIT $1`, batch)
	if err != nil {
		return domain.RecycleResult{}, err
	}
	type expired struct {
		id     string
		userID int64
	}
	var assignments []expired
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.userID); err != nil {
			rows.Close()
			return domain.RecycleResult{}, err
		}
		assignments = append(assignments, item)
	}
	rows.Close()
	result := domain.RecycleResult{Assignments: len(assignments)}
	for _, assignment := range assignments {
		numberRows, err := tx.Query(ctx, `SELECT number_id,consumed_at IS NOT NULL
			FROM assignment_numbers WHERE assignment_id=$1 FOR UPDATE`, assignment.id)
		if err != nil {
			return result, err
		}
		type assignmentNumber struct {
			id       int64
			consumed bool
		}
		var assignmentNumbers []assignmentNumber
		for numberRows.Next() {
			var item assignmentNumber
			if err := numberRows.Scan(&item.id, &item.consumed); err != nil {
				numberRows.Close()
				return result, err
			}
			assignmentNumbers = append(assignmentNumbers, item)
		}
		numberRows.Close()
		var returned int
		for _, item := range assignmentNumbers {
			if item.consumed {
				result.Consumed++
				continue
			}
			if _, err = tx.Exec(ctx, `UPDATE assignment_numbers SET released_at=now()
				WHERE assignment_id=$1 AND number_id=$2 AND consumed_at IS NULL AND released_at IS NULL`, assignment.id, item.id); err != nil {
				return result, err
			}
			if _, err = tx.Exec(ctx, `UPDATE numbers SET state='available',updated_at=now() WHERE id=$1 AND state='assigned'`, item.id); err != nil {
				return result, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO number_user_exclusions(number_id,user_id,expires_at)
				VALUES($1,$2,now()+$3::interval)
				ON CONFLICT(number_id,user_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`,
				item.id, assignment.userID, postgresInterval(reuseCooldown)); err != nil {
				return result, err
			}
			returned++
			result.Returned++
		}
		state := "completed"
		if returned > 0 {
			state = "expired"
		}
		if _, err = tx.Exec(ctx, `UPDATE assignments SET state=$2,closed_at=now() WHERE id=$1`, assignment.id, state); err != nil {
			return result, err
		}
	}
	_, _ = tx.Exec(ctx, `DELETE FROM number_user_exclusions WHERE expires_at<=now()`)
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	buf := make([]byte, 36)
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf)
}

func postgresInterval(d time.Duration) string {
	seconds := int64(d / time.Second)
	return fmt.Sprintf("%d seconds", seconds)
}

func NormalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func DedupKey(panelName, normalizedPhone, message string) string {
	payload, _ := json.Marshal([]string{panelName, normalizedPhone, strings.TrimSpace(message)})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

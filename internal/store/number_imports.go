package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ImportNumber struct{ Phone, Country, CountryCode string }
type ImportService struct {
	Name, EmojiID      string
	PricePKR, PriceUSD float64
	PerCycle           int
}
type ImportSettings struct {
	Services   []ImportService
	KeepPrices bool
}
type ImportResult struct {
	Service         string
	Added, Existing int
}
type ImportJob struct {
	ID, BotInstanceID, UserID, ChatID                             int64
	Nonce, FileName, State, ErrorCode, LeaseToken                 string
	MessageID, ValidCount, InvalidCount, DuplicateCount, Attempts int
	Settings                                                      ImportSettings
	Results                                                       []ImportResult
	CreatedAt, ExpiresAt                                          time.Time
	input                                                         []byte
}

const importColumns = `id,bot_instance_id,user_id,chat_id,nonce,file_name,state,error_code,lease_token,message_id,valid_count,invalid_count,duplicate_count,attempts,settings,result,created_at,expires_at,input_config`

func scanImport(row pgx.Row) (j ImportJob, err error) {
	var settings, result []byte
	err = row.Scan(&j.ID, &j.BotInstanceID, &j.UserID, &j.ChatID, &j.Nonce, &j.FileName, &j.State, &j.ErrorCode, &j.LeaseToken, &j.MessageID, &j.ValidCount, &j.InvalidCount, &j.DuplicateCount, &j.Attempts, &settings, &result, &j.CreatedAt, &j.ExpiresAt, &j.input)
	if err == nil {
		err = json.Unmarshal(settings, &j.Settings)
	}
	if err == nil {
		err = json.Unmarshal(result, &j.Results)
	}
	return
}

func (s *Store) CreateImportDraft(ctx context.Context, instance, user, chat int64, nonce, name string, numbers []ImportNumber, invalid, duplicates int) (ImportJob, error) {
	if len(numbers) == 0 || nonce == "" {
		return ImportJob{}, errors.New("no valid numbers in import")
	}
	raw, err := json.Marshal(numbers)
	if err != nil {
		return ImportJob{}, err
	}
	encrypted, err := s.cipher.Encrypt(raw)
	if err != nil {
		return ImportJob{}, err
	}
	envelope, _ := json.Marshal(map[string]string{"encrypted": encrypted})
	return scanImport(s.pool.QueryRow(ctx, `INSERT INTO number_import_jobs(bot_instance_id,user_id,chat_id,nonce,file_name,input_config,valid_count,invalid_count,duplicate_count,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now()+interval '30 minutes')
 ON CONFLICT(bot_instance_id,user_id,nonce) DO UPDATE SET nonce=EXCLUDED.nonce RETURNING `+importColumns, instanceID(instance), user, chat, nonce, name, envelope, len(numbers), invalid, duplicates))
}
func (s *Store) ImportJob(ctx context.Context, instance, user, id int64) (ImportJob, error) {
	return scanImport(s.pool.QueryRow(ctx, `SELECT `+importColumns+` FROM number_import_jobs WHERE bot_instance_id=$1 AND user_id=$2 AND id=$3`, instanceID(instance), user, id))
}
func (s *Store) ImportByNonce(ctx context.Context, instance, user int64, nonce string) (ImportJob, error) {
	return scanImport(s.pool.QueryRow(ctx, `SELECT `+importColumns+` FROM number_import_jobs WHERE bot_instance_id=$1 AND user_id=$2 AND nonce=$3`, instanceID(instance), user, nonce))
}
func (s *Store) DetachImportScreens(ctx context.Context, instance, user int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE number_import_jobs SET message_id=0,notified_at=now(),state=CASE WHEN state='draft' THEN 'cancelled' ELSE state END WHERE bot_instance_id=$1 AND user_id=$2 AND (state IN ('draft','queued','running') OR notified_at IS NULL)`, instanceID(instance), user)
	return err
}
func (s *Store) ImportNumbers(ctx context.Context, j ImportJob) ([]ImportNumber, error) {
	if len(j.input) == 0 || !j.ExpiresAt.After(time.Now()) {
		return nil, errors.New("import input expired")
	}
	plaintext, err := decryptEnvelope(j.input, s.cipher.Decrypt)
	if err != nil {
		return nil, err
	}
	var numbers []ImportNumber
	err = json.Unmarshal([]byte(plaintext), &numbers)
	return numbers, err
}
func validateImportSettings(settings ImportSettings) error {
	if len(settings.Services) == 0 || len(settings.Services) > 20 {
		return errors.New("select between 1 and 20 apps")
	}
	seen := map[string]bool{}
	for _, v := range settings.Services {
		key := strings.ToLower(strings.TrimSpace(v.Name))
		if key == "" || len(v.Name) > 64 || seen[key] || v.PerCycle < 1 || v.PricePKR < 0 || v.PriceUSD < 0 || math.IsNaN(v.PricePKR) || math.IsNaN(v.PriceUSD) || math.IsInf(v.PricePKR, 0) || math.IsInf(v.PriceUSD, 0) {
			return errors.New("invalid import settings")
		}
		seen[key] = true
	}
	return nil
}
func (s *Store) QueueImport(ctx context.Context, instance, user, id int64, settings ImportSettings, expected TelegramFlow) (ImportJob, error) {
	if err := validateImportSettings(settings); err != nil {
		return ImportJob{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ImportJob{}, err
	}
	defer tx.Rollback(ctx)
	j, err := scanImport(tx.QueryRow(ctx, `SELECT `+importColumns+` FROM number_import_jobs WHERE id=$1 AND bot_instance_id=$2 AND user_id=$3 FOR UPDATE`, id, instanceID(instance), user))
	if err != nil {
		return j, err
	}
	if j.State != "draft" {
		return j, nil
	}
	if !j.ExpiresAt.After(time.Now()) {
		return j, errors.New("import input expired")
	}
	raw, _ := json.Marshal(settings)
	j, err = scanImport(tx.QueryRow(ctx, `UPDATE number_import_jobs SET state='queued',settings=$2,expires_at=now()+interval '7 days',updated_at=now() WHERE id=$1 RETURNING `+importColumns, id, raw))
	if err != nil {
		return j, err
	}
	if expected.Kind != "" {
		_, err = tx.Exec(ctx, `DELETE FROM telegram_flows WHERE bot_instance_id=$1 AND user_id=$2 AND kind=$3 AND expires_at=$4`, instanceID(instance), user, expected.Kind, expected.ExpiresAt)
		if err != nil {
			return j, err
		}
	}
	return j, tx.Commit(ctx)
}
func (s *Store) SetImportMessage(ctx context.Context, instance, user, id, chat int64, message int) error {
	_, err := s.pool.Exec(ctx, `UPDATE number_import_jobs SET chat_id=$4,message_id=$5,expires_at=CASE WHEN state='draft' THEN now()+interval '30 minutes' ELSE expires_at END WHERE bot_instance_id=$1 AND user_id=$2 AND id=$3`, instanceID(instance), user, id, chat, message)
	return err
}
func (s *Store) ListImports(ctx context.Context, instance, user int64, offset int) ([]ImportJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+importColumns+` FROM number_import_jobs WHERE bot_instance_id=$1 AND user_id=$2 ORDER BY id DESC LIMIT 20 OFFSET $3`, instanceID(instance), user, max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportJob{}
	for rows.Next() {
		j, e := scanImport(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) ChangeImportState(ctx context.Context, instance, user, id int64, action string) (ImportJob, error) {
	state, condition := "cancelled", `state IN ('draft','queued','running','failed')`
	if action == "retry" {
		state = "queued"
		condition = `state='failed' AND input_config IS NOT NULL AND expires_at>now()`
	} else if action != "cancel" {
		return ImportJob{}, errors.New("invalid import action")
	}
	_, err := s.pool.Exec(ctx, `UPDATE number_import_jobs SET state=$4,lease_token='',lease_until=NULL,error_code='',notified_at=NULL,updated_at=now() WHERE bot_instance_id=$1 AND user_id=$2 AND id=$3 AND `+condition, instanceID(instance), user, id, state)
	if err != nil {
		return ImportJob{}, err
	}
	return s.ImportJob(ctx, instance, user, id)
}
func (s *Store) ClaimImport(ctx context.Context, instance int64) (ImportJob, error) {
	token := newUUID()
	return scanImport(s.pool.QueryRow(ctx, `UPDATE number_import_jobs SET state='running',lease_token=$2,lease_until=now()+interval '2 minutes',attempts=attempts+1,updated_at=now()
 WHERE id=(SELECT id FROM number_import_jobs WHERE bot_instance_id=$1 AND expires_at>now() AND input_config IS NOT NULL AND (state='queued' OR (state='running' AND lease_until<now())) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+importColumns, instanceID(instance), token))
}
func (s *Store) RenewImportLease(ctx context.Context, j ImportJob) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE number_import_jobs SET lease_until=now()+interval '2 minutes' WHERE id=$1 AND state='running' AND lease_token=$2`, j.ID, j.LeaseToken)
	return tag.RowsAffected() == 1, err
}
func (s *Store) FailImport(ctx context.Context, j ImportJob, err error) error {
	_, e := s.pool.Exec(ctx, `UPDATE number_import_jobs SET state='failed',error_code=$3,lease_until=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2 AND state='running'`, j.ID, j.LeaseToken, ImportErrorCode(err))
	return e
}
func ImportErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "INTERRUPTED"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "TIMEOUT"
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return "DATABASE_" + pg.Code
	}
	if strings.Contains(fmt.Sprint(err), "failed to encode") {
		return "DATABASE_PARAMETER"
	}
	return "IMPORT_FAILED"
}
func (s *Store) ExecuteImport(ctx context.Context, j ImportJob) ([]ImportResult, error) {
	allowed, err := s.HasAdminPermission(ctx, j.BotInstanceID, j.UserID, "manage_settings")
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, errors.New("inventory permission was revoked")
	}
	numbers, err := s.ImportNumbers(ctx, j)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Do not lock the job during bulk preparation: navigation, cancellation and
	// lease renewal must remain responsive. The conditional final update is the
	// publication guard, in the same transaction as every inventory write.
	var state, token string
	if err = tx.QueryRow(ctx, `SELECT state,lease_token FROM number_import_jobs WHERE id=$1`, j.ID).Scan(&state, &token); err != nil {
		return nil, err
	}
	if state != "running" || token != j.LeaseToken {
		return nil, errors.New("import lease ended")
	}
	results, err := s.bulkImportNumbers(ctx, tx, j.BotInstanceID, j.UserID, j.Settings, numbers, false)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(results)
	tag, err := tx.Exec(ctx, `UPDATE number_import_jobs SET state='succeeded',result=$2,input_config=NULL,lease_until=NULL,error_code='',updated_at=now() WHERE id=$1 AND state='running' AND lease_token=$3`, j.ID, raw, j.LeaseToken)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, errors.New("import lease ended before publication")
	}
	return results, tx.Commit(ctx)
}
func (s *Store) PendingImportNotices(ctx context.Context, instance int64) ([]ImportJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+importColumns+` FROM number_import_jobs WHERE bot_instance_id=$1 AND state IN ('succeeded','failed','cancelled') AND notified_at IS NULL AND message_id<>0 ORDER BY id LIMIT 20`, instanceID(instance))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportJob{}
	for rows.Next() {
		j, e := scanImport(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) ImportNotified(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE number_import_jobs SET notified_at=now() WHERE id=$1`, id)
	return err
}
func (s *Store) CleanupImports(ctx context.Context, instance int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE number_import_jobs SET input_config=NULL,state=CASE WHEN state IN ('draft','queued','running','failed') THEN 'cancelled' ELSE state END WHERE bot_instance_id=$1 AND expires_at<now() AND input_config IS NOT NULL`, instanceID(instance))
	return err
}

// All app/country inventory is published by the caller's single transaction.
// Round trips depend on batches, never on the number of phone/app pairs.
func (s *Store) bulkImportNumbers(ctx context.Context, tx pgx.Tx, instance, actor int64, settings ImportSettings, numbers []ImportNumber, strictCountry bool) ([]ImportResult, error) {
	if err := validateImportSettings(settings); err != nil {
		return nil, err
	}
	services := append([]ImportService(nil), settings.Services...)
	sort.Slice(services, func(i, j int) bool { return strings.ToLower(services[i].Name) < strings.ToLower(services[j].Name) })
	for i, v := range services {
		v.Name = strings.TrimSpace(v.Name)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, strings.ToLower(v.Name)); err != nil {
			return nil, err
		}
		var canonical string
		err := tx.QueryRow(ctx, `SELECT service FROM service_countries WHERE lower(btrim(service))=lower($1) ORDER BY service LIMIT 1`, v.Name).Scan(&canonical)
		if err == nil {
			v.Name = canonical
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		services[i] = v
		if instance != 0 && v.EmojiID != "" {
			_, err = tx.Exec(ctx, `INSERT INTO service_profiles(bot_instance_id,service_key,display_name,custom_emoji_id,created_by) VALUES($1,lower($2),$2,$3,NULLIF($4::bigint,0)) ON CONFLICT(bot_instance_id,service_key) DO UPDATE SET display_name=EXCLUDED.display_name,custom_emoji_id=EXCLUDED.custom_emoji_id,updated_at=now()`, instanceID(instance), v.Name, v.EmojiID, actor)
			if err != nil {
				return nil, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE import_number_stage(phone text,country text,country_code text) ON COMMIT DROP`); err != nil {
		return nil, err
	}
	rows := make([][]any, 0, min(2000, len(numbers)))
	seen := map[string]bool{}
	flush := func() error {
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"import_number_stage"}, []string{"phone", "country", "country_code"}, pgx.CopyFromRows(rows))
		rows = rows[:0]
		return err
	}
	for _, n := range numbers {
		phone := NormalizePhone(n.Phone)
		if len(phone) < 5 || len(phone) > 20 || seen[phone] {
			continue
		}
		if strings.TrimSpace(n.Country) == "" {
			return nil, errors.New("country is required")
		}
		seen[phone] = true
		rows = append(rows, []any{phone, strings.TrimSpace(n.Country), n.CountryCode})
		if len(rows) == 2000 {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if len(rows) > 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `CREATE INDEX ON import_number_stage(phone)`); err != nil {
		return nil, err
	}
	results := []ImportResult{}
	for _, v := range services {
		if strictCountry {
			var conflict bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM numbers n JOIN import_number_stage i ON i.phone=n.normalized_phone WHERE n.service_key=lower($1) AND n.country<>i.country)`, v.Name).Scan(&conflict); err != nil {
				return nil, err
			}
			if conflict {
				return nil, errors.New("number already belongs to another country for this service")
			}
		}
		conflict := `DO UPDATE SET country_code=EXCLUDED.country_code,price_pkr=EXCLUDED.price_pkr,price_usd=EXCLUDED.price_usd,numbers_per_cycle=EXCLUDED.numbers_per_cycle,enabled=true`
		if settings.KeepPrices {
			conflict = `DO NOTHING`
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_countries(service,country,country_code,price_pkr,price_usd,numbers_per_cycle) SELECT $1,country,min(country_code),$2,$3,$4 FROM import_number_stage GROUP BY country ON CONFLICT(service,country) `+conflict, v.Name, v.PricePKR, v.PriceUSD, v.PerCycle); err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO numbers(phone,normalized_phone,service,country) SELECT phone,phone,$1,country FROM import_number_stage ORDER BY phone ON CONFLICT(normalized_phone,service_key) DO NOTHING`, v.Name)
		if err != nil {
			return nil, err
		}
		results = append(results, ImportResult{Service: v.Name, Added: int(tag.RowsAffected()), Existing: len(seen) - int(tag.RowsAffected())})
	}
	return results, nil
}

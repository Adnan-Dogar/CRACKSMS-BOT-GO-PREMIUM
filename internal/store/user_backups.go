package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const BackupFileLimit = 10 << 20
const backupChunkSize = 5 << 20
const backupDecodedLimit = 256 << 20
const backupCompressedLimit = 128 << 20
const backupMagic = "CRACKSMS-USER-BACKUP-1\n"

type BackupPart struct {
	ID          string
	Part, Total int
	File        []byte
}
type backupPartPayload struct {
	ID          string
	Part, Total int
	Digest      string
	Data        []byte
}
type backupBot struct {
	ID       int64
	Username string
}
type UserBackup struct {
	Version   int                          `json:"version"`
	ID        string                       `json:"id"`
	CreatedAt time.Time                    `json:"created_at"`
	Bots      []backupBot                  `json:"bots"`
	Tables    map[string][]json.RawMessage `json:"tables"`
}
type BackupPreview struct {
	BackupID        string    `json:"backup_id"`
	CreatedAt       time.Time `json:"created_at"`
	NewUsers        int       `json:"new_users"`
	ExistingUsers   int       `json:"existing_users"`
	Records         int       `json:"records"`
	AlreadyRestored bool      `json:"already_restored"`
}

// The archive contains user data only. This allowlist deliberately excludes all
// administrative privileges, API keys, provider credentials and outgoing jobs.
var backupTables = []string{"users", "bot_instance_users", "user_preferences", "user_subscriptions", "user_favorites", "user_last_selections", "availability_watches", "service_countries", "numbers", "otp_events", "assignments", "assignment_numbers", "reward_schedules", "reward_rules", "reward_awards", "user_daily_progress", "balance_ledger", "referrals", "withdrawal_accounts", "withdrawals"}
var backupHistoryOwner = map[string]string{"otp_events": "assigned_user_id", "assignments": "user_id", "reward_awards": "user_id", "user_daily_progress": "user_id", "balance_ledger": "user_id", "referrals": "user_id", "withdrawal_accounts": "user_id", "withdrawals": "user_id"}

type backupLimitWriter struct {
	w    io.Writer
	size int
}

func (w *backupLimitWriter) Write(p []byte) (int, error) {
	if w.size+len(p) > backupDecodedLimit {
		return 0, errors.New("user backup exceeds the 256 MB decoded limit")
	}
	n, e := w.w.Write(p)
	w.size += n
	return n, e
}
func (s *Store) backupPermission(ctx context.Context, actor int64) error {
	allowed, err := s.IsAdmin(ctx, actor, "manage_backups")
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("main-admin backup permission is required")
	}
	return nil
}
func (s *Store) ExportUserBackup(ctx context.Context, actor int64) ([]BackupPart, error) {
	if err := s.backupPermission(ctx, actor); err != nil {
		return nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	id := newUUID()
	created := time.Now().UTC()
	var bots []backupBot
	rows, err := tx.Query(ctx, `SELECT id,username FROM bot_instances ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b backupBot
		if err = rows.Scan(&b.ID, &b.Username); err != nil {
			rows.Close()
			return nil, err
		}
		bots = append(bots, b)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	limited := &backupLimitWriter{w: gz}
	enc := json.NewEncoder(limited)
	write := func(v string) error { _, e := io.WriteString(limited, v); return e }
	if err = write(`{"version":1,"id":`); err != nil {
		return nil, err
	}
	if err = enc.Encode(id); err != nil {
		return nil, err
	}
	if err = write(`,"created_at":`); err != nil {
		return nil, err
	}
	if err = enc.Encode(created); err != nil {
		return nil, err
	}
	if err = write(`,"bots":`); err != nil {
		return nil, err
	}
	if err = enc.Encode(bots); err != nil {
		return nil, err
	}
	if err = write(`,"tables":{`); err != nil {
		return nil, err
	}
	for i, table := range backupTables {
		if i > 0 {
			if err = write(","); err != nil {
				return nil, err
			}
		}
		if err = write(strconv.Quote(table) + ":["); err != nil {
			return nil, err
		}
		query := `SELECT to_jsonb(t) FROM ` + pgx.Identifier{table}.Sanitize() + ` t`
		switch table {
		case "otp_events":
			query += ` WHERE assigned_user_id IS NOT NULL`
		case "numbers":
			query += ` WHERE EXISTS(SELECT 1 FROM assignment_numbers an WHERE an.number_id=t.id)`
		case "service_countries":
			query += ` WHERE EXISTS(SELECT 1 FROM numbers n JOIN assignment_numbers an ON an.number_id=n.id WHERE n.service=t.service AND n.country=t.country)`
		}
		rows, err = tx.Query(ctx, query)
		if err != nil {
			return nil, err
		}
		first := true
		for rows.Next() {
			var raw json.RawMessage
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			if !first {
				if err = write(","); err != nil {
					rows.Close()
					return nil, err
				}
			}
			first = false
			if err = enc.Encode(raw); err != nil {
				rows.Close()
				return nil, err
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
		if err = write("]"); err != nil {
			return nil, err
		}
	}
	if err = write("}}"); err != nil {
		return nil, err
	}
	if err = gz.Close(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	if compressed.Len() > backupCompressedLimit {
		return nil, errors.New("user backup exceeds compressed size limit")
	}
	digest := sha256.Sum256(compressed.Bytes())
	total := max(1, (compressed.Len()+backupChunkSize-1)/backupChunkSize)
	var parts []BackupPart
	for n := 0; n < total; n++ {
		payload := backupPartPayload{ID: id, Part: n + 1, Total: total, Digest: hex.EncodeToString(digest[:]), Data: compressed.Bytes()[n*backupChunkSize : min((n+1)*backupChunkSize, compressed.Len())]}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		sealed, err := s.cipher.Encrypt(raw)
		if err != nil {
			return nil, err
		}
		file := []byte(backupMagic + sealed)
		if len(file) > BackupFileLimit {
			return nil, errors.New("backup part exceeds upload limit")
		}
		parts = append(parts, BackupPart{ID: id, Part: n + 1, Total: total, File: file})
	}
	return parts, nil
}
func (s *Store) readBackupPart(file []byte) (backupPartPayload, error) {
	var part backupPartPayload
	if len(file) > BackupFileLimit || !bytes.HasPrefix(file, []byte(backupMagic)) {
		return part, errors.New("not a CrackSMS user backup")
	}
	raw, err := s.cipher.Decrypt(strings.TrimSpace(string(file[len(backupMagic):])))
	if err != nil {
		return part, errors.New("backup integrity check failed or encryption key does not match")
	}
	if err = json.Unmarshal(raw, &part); err != nil {
		return part, errors.New("invalid backup part")
	}
	if part.ID == "" || part.Total < 1 || part.Total > 32 || part.Part < 1 || part.Part > part.Total || len(part.Data) > backupChunkSize || len(part.Digest) != 64 {
		return part, errors.New("invalid backup part metadata")
	}
	return part, nil
}
func (s *Store) DecodeUserBackup(files [][]byte) (UserBackup, error) {
	var data UserBackup
	if len(files) == 0 || len(files) > 32 {
		return data, errors.New("upload every backup part")
	}
	parts := make([]backupPartPayload, 0, len(files))
	seen := map[int]bool{}
	for _, file := range files {
		p, err := s.readBackupPart(file)
		if err != nil {
			return data, err
		}
		if seen[p.Part] {
			return data, errors.New("duplicate backup part")
		}
		seen[p.Part] = true
		parts = append(parts, p)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Part < parts[j].Part })
	first := parts[0]
	if first.Total != len(parts) {
		return data, errors.New("backup parts are missing")
	}
	var compressed bytes.Buffer
	for i, p := range parts {
		if p.ID != first.ID || p.Digest != first.Digest || p.Total != first.Total || p.Part != i+1 {
			return data, errors.New("backup parts do not belong to the same archive")
		}
		compressed.Write(p.Data)
	}
	digest := sha256.Sum256(compressed.Bytes())
	if hex.EncodeToString(digest[:]) != first.Digest {
		return data, errors.New("backup checksum mismatch")
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		return data, errors.New("invalid compressed backup")
	}
	defer gz.Close()
	raw, err := io.ReadAll(io.LimitReader(gz, backupDecodedLimit+1))
	if err != nil || len(raw) > backupDecodedLimit {
		return data, errors.New("backup is damaged or exceeds decoded size limit")
	}
	if err = json.Unmarshal(raw, &data); err != nil || data.Version != 1 || data.ID != first.ID || data.CreatedAt.IsZero() {
		return UserBackup{}, errors.New("unsupported or invalid backup manifest")
	}
	known := map[string]bool{}
	for _, table := range backupTables {
		known[table] = true
		if _, ok := data.Tables[table]; !ok {
			return UserBackup{}, fmt.Errorf("backup section %s is missing", table)
		}
	}
	for table := range data.Tables {
		if !known[table] {
			return UserBackup{}, errors.New("unsupported backup section")
		}
	}
	return data, nil
}
func backupRowID(row json.RawMessage, key string) (int64, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(row, &fields); err != nil {
		return 0, err
	}
	var id int64
	if err := json.Unmarshal(fields[key], &id); err != nil {
		return 0, err
	}
	return id, nil
}
func (s *Store) PreviewUserBackup(ctx context.Context, actor int64, data UserBackup) (BackupPreview, error) {
	if err := s.backupPermission(ctx, actor); err != nil {
		return BackupPreview{}, err
	}
	p := BackupPreview{BackupID: data.ID, CreatedAt: data.CreatedAt}
	if data.Version != 1 || data.ID == "" {
		return p, errors.New("invalid backup")
	}
	for _, bot := range data.Bots {
		var username string
		err := s.pool.QueryRow(ctx, `SELECT username FROM bot_instances WHERE id=$1`, bot.ID).Scan(&username)
		if err != nil || username != bot.Username {
			return p, errors.New("backup bot instances do not match this installation")
		}
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, row := range data.Tables["users"] {
		id, err := backupRowID(row, "id")
		if err != nil || id <= 0 || seen[id] {
			return p, errors.New("invalid or repeated user in backup")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=ANY($1::bigint[])`, ids).Scan(&p.ExistingUsers); err != nil {
		return p, err
	}
	p.NewUsers = len(ids) - p.ExistingUsers
	for _, rows := range data.Tables {
		p.Records += len(rows)
	}
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_backup_restores WHERE backup_id=$1)`, data.ID).Scan(&p.AlreadyRestored); err != nil {
		return p, err
	}
	return p, nil
}

// Only allowlisted, non-generated columns are used. Archive table names never
// become SQL identifiers; immutable financial/history rows are new-user only.
func insertBackupRows(ctx context.Context, tx pgx.Tx, table string, rows []json.RawMessage, filter string, omit map[string]bool) error {
	if len(rows) == 0 {
		return nil
	}
	// Older archives omit columns added by later migrations. Let PostgreSQL use
	// their defaults instead of projecting NULL from jsonb_populate_recordset.
	var archived map[string]json.RawMessage
	if err := json.Unmarshal(rows[0], &archived); err != nil {
		return err
	}
	cols, err := tx.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND is_generated='NEVER' ORDER BY ordinal_position`, table)
	if err != nil {
		return err
	}
	var names []string
	for cols.Next() {
		var col string
		if err = cols.Scan(&col); err != nil {
			cols.Close()
			return err
		}
		if _, present := archived[col]; present && !omit[col] {
			names = append(names, pgx.Identifier{col}.Sanitize())
		}
	}
	cols.Close()
	if err = cols.Err(); err != nil {
		return err
	}
	if len(names) == 0 {
		return errors.New("backup table columns are missing")
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	projection := strings.Join(names, ",")
	_, err = tx.Exec(ctx, `INSERT INTO `+pgx.Identifier{table}.Sanitize()+` (`+projection+`) SELECT `+projection+` FROM jsonb_populate_recordset(NULL::`+pgx.Identifier{table}.Sanitize()+`,$1::jsonb) r `+filter+` ON CONFLICT DO NOTHING`, raw)
	return err
}
func changeBackupRows(rows []json.RawMessage, change func(map[string]json.RawMessage)) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(rows))
	for _, raw := range rows {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		change(row)
		body, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		out = append(out, body)
	}
	return out, nil
}
func (s *Store) RestoreUserBackup(ctx context.Context, actor int64, data UserBackup) (BackupPreview, error) {
	preview, err := s.PreviewUserBackup(ctx, actor, data)
	if err != nil {
		return preview, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return preview, err
	}
	defer tx.Rollback(ctx)
	rawSummary, _ := json.Marshal(preview)
	tag, err := tx.Exec(ctx, `INSERT INTO user_backup_restores(backup_id,actor_id,summary) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, data.ID, actor, rawSummary)
	if err != nil {
		return preview, err
	}
	if tag.RowsAffected() == 0 {
		preview.AlreadyRestored = true
		preview.NewUsers = 0
		return preview, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `CREATE TEMP TABLE backup_new_users(id bigint PRIMARY KEY) ON COMMIT DROP; CREATE TEMP TABLE backup_number_map(old_id bigint PRIMARY KEY,new_id bigint NOT NULL) ON COMMIT DROP`); err != nil {
		return preview, err
	}
	userRows, err := changeBackupRows(data.Tables["users"], func(row map[string]json.RawMessage) { row["referred_by"] = json.RawMessage(`null`) })
	if err != nil {
		return preview, err
	}
	rawUsers, _ := json.Marshal(userRows)
	_, err = tx.Exec(ctx, `WITH added AS (INSERT INTO users SELECT r.* FROM jsonb_populate_recordset(NULL::users,$1::jsonb) r ON CONFLICT DO NOTHING RETURNING id) INSERT INTO backup_new_users SELECT id FROM added`, rawUsers)
	if err != nil {
		return preview, err
	}
	// An unrelated referral-code conflict must not silently lose a new user.
	var missing int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM jsonb_populate_recordset(NULL::users,$1::jsonb) r WHERE NOT EXISTS(SELECT 1 FROM users u WHERE u.id=r.id)`, rawUsers).Scan(&missing); err != nil || missing != 0 {
		if err == nil {
			err = errors.New("backup conflicts with an existing referral code")
		}
		return preview, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM backup_new_users`).Scan(&preview.NewUsers); err != nil {
		return preview, err
	}
	preview.ExistingUsers = len(userRows) - preview.NewUsers
	if preview.NewUsers > 0 {
		// Explicit archived IDs and live writers must not race the sequence repair.
		// Take these locks in one deterministic statement for the short restore transaction.
		if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
			return preview, err
		}
		if _, err = tx.Exec(ctx, `LOCK TABLE reward_schedules,reward_rules,reward_awards,balance_ledger,withdrawal_accounts,withdrawals IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return preview, err
		}
	}
	original, _ := json.Marshal(data.Tables["users"])
	if _, err = tx.Exec(ctx, `UPDATE users u SET referred_by=r.referred_by FROM jsonb_populate_recordset(NULL::users,$1::jsonb) r WHERE u.id=r.id AND u.id IN(SELECT id FROM backup_new_users) AND EXISTS(SELECT 1 FROM users ref WHERE ref.id=r.referred_by)`, original); err != nil {
		return preview, err
	}
	// Foreign identifiers are retained only if they still identify the same owner.
	for table, owner := range backupHistoryOwner {
		if table == "referrals" || table == "user_daily_progress" {
			continue
		}
		raw, _ := json.Marshal(data.Tables[table])
		var conflict bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jsonb_populate_recordset(NULL::`+table+`,$1::jsonb) r JOIN `+table+` live ON live.id=r.id WHERE r.`+owner+` IN(SELECT id FROM backup_new_users) AND live.`+owner+` IS DISTINCT FROM r.`+owner+`)`, raw).Scan(&conflict)
		if err != nil {
			return preview, err
		}
		if conflict {
			return preview, fmt.Errorf("backup history IDs conflict in %s", table)
		}
	}
	for _, table := range []string{"bot_instance_users", "user_preferences", "user_subscriptions", "user_favorites", "user_last_selections", "availability_watches"} {
		omit := map[string]bool{}
		if table == "user_favorites" || table == "availability_watches" {
			omit["id"] = true
		}
		if err = insertBackupRows(ctx, tx, table, data.Tables[table], "", omit); err != nil {
			return preview, fmt.Errorf("restore %s: %w", table, err)
		}
	}
	if preview.NewUsers > 0 {
		catalog, err := changeBackupRows(data.Tables["service_countries"], func(row map[string]json.RawMessage) { row["enabled"] = json.RawMessage(`false`) })
		if err != nil {
			return preview, err
		}
		if err = insertBackupRows(ctx, tx, "service_countries", catalog, "", nil); err != nil {
			return preview, err
		}
		numbers, err := changeBackupRows(data.Tables["numbers"], func(row map[string]json.RawMessage) { row["state"] = json.RawMessage(`"disabled"`) })
		if err != nil {
			return preview, err
		}
		if err = insertBackupRows(ctx, tx, "numbers", numbers, "", map[string]bool{"id": true}); err != nil {
			return preview, err
		}
		rawNumbers, _ := json.Marshal(data.Tables["numbers"])
		if _, err = tx.Exec(ctx, `INSERT INTO backup_number_map SELECT r.id,n.id FROM jsonb_populate_recordset(NULL::numbers,$1::jsonb) r JOIN numbers n ON n.normalized_phone=r.normalized_phone AND n.service_key=lower(btrim(r.service))`, rawNumbers); err != nil {
			return preview, err
		}
		schedules, err := changeBackupRows(data.Tables["reward_schedules"], func(row map[string]json.RawMessage) {
			if string(row["user_id"]) == "null" {
				row["enabled"] = json.RawMessage(`false`)
			}
			row["created_by"] = json.RawMessage(`null`)
		})
		if err != nil {
			return preview, err
		}
		if err = insertBackupRows(ctx, tx, "reward_schedules", schedules, "WHERE r.user_id IS NULL OR r.user_id IN(SELECT id FROM backup_new_users)", nil); err != nil {
			return preview, err
		}
		if err = insertBackupRows(ctx, tx, "reward_rules", data.Tables["reward_rules"], "WHERE EXISTS(SELECT 1 FROM reward_schedules s WHERE s.id=r.schedule_id)", nil); err != nil {
			return preview, err
		}
	}
	for _, table := range []string{"otp_events", "assignments", "reward_awards", "user_daily_progress", "balance_ledger", "referrals", "withdrawal_accounts", "withdrawals"} {
		rows := data.Tables[table]
		if table == "otp_events" {
			rows, err = changeBackupRows(rows, func(row map[string]json.RawMessage) {
				row["panel_id"] = json.RawMessage(`null`)
				row["shared_from_event_id"] = json.RawMessage(`null`)
			})
		}
		if table == "assignments" {
			rows, err = changeBackupRows(rows, func(row map[string]json.RawMessage) {
				if string(row["state"]) == `"active"` {
					row["state"] = json.RawMessage(`"expired"`)
				}
				if string(row["closed_at"]) == "null" {
					row["closed_at"], _ = json.Marshal(time.Now().UTC())
				}
			})
		}
		if table == "withdrawals" {
			rows, err = changeBackupRows(rows, func(row map[string]json.RawMessage) { row["resolved_by"] = json.RawMessage(`null`) })
		}
		if err != nil {
			return preview, err
		}
		if err = insertBackupRows(ctx, tx, table, rows, "WHERE r."+backupHistoryOwner[table]+" IN(SELECT id FROM backup_new_users)", nil); err != nil {
			return preview, fmt.Errorf("restore %s: %w", table, err)
		}
	}
	rawLinks, _ := json.Marshal(data.Tables["assignment_numbers"])
	if _, err = tx.Exec(ctx, `INSERT INTO assignment_numbers(assignment_id,number_id,consumed_at,released_at,otp_event_id)
 SELECT r.assignment_id,m.new_id,r.consumed_at,COALESCE(r.released_at,now()),r.otp_event_id FROM jsonb_populate_recordset(NULL::assignment_numbers,$1::jsonb) r JOIN backup_number_map m ON m.old_id=r.number_id JOIN assignments a ON a.id=r.assignment_id JOIN backup_new_users u ON u.id=a.user_id ON CONFLICT DO NOTHING`, rawLinks); err != nil {
		return preview, err
	}
	if preview.NewUsers > 0 {
		for _, table := range []string{"reward_schedules", "reward_rules", "reward_awards", "balance_ledger", "withdrawal_accounts", "withdrawals"} {
			if _, err = tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1,'id'),GREATEST(COALESCE((SELECT max(id) FROM `+table+`),1), (SELECT last_value FROM `+table+`_id_seq)),true)`, table); err != nil {
				return preview, err
			}
		}
	}
	rawSummary, _ = json.Marshal(preview)
	if _, err = tx.Exec(ctx, `UPDATE user_backup_restores SET summary=$2 WHERE backup_id=$1`, data.ID, rawSummary); err != nil {
		return preview, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_log(bot_instance_id,actor_user_id,action,target_type,target_id,metadata) VALUES(1,$1,'users.restore','backup',$2,$3)`, actor, data.ID, rawSummary); err != nil {
		return preview, err
	}
	return preview, tx.Commit(ctx)
}

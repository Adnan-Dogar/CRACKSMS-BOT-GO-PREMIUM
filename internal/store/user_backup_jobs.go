package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type UserBackupJob struct {
	ID, Actor, Chat                         int64
	Kind, State, BackupID, Phase, ErrorCode string
	Summary                                 BackupPreview
	ClaimedAt                               *time.Time
	ExpiresAt                               time.Time
}

func (s *Store) CreateUserBackupJob(ctx context.Context, actor, chat int64, kind, key string) (UserBackupJob, error) {
	if err := s.backupPermission(ctx, actor); err != nil {
		return UserBackupJob{}, err
	}
	if kind != "export" && kind != "restore" {
		return UserBackupJob{}, errors.New("invalid backup action")
	}
	state := "uploading"
	if kind == "export" {
		state = "queued"
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO user_backup_jobs(actor_id,chat_id,kind,state,request_key) VALUES($1,$2,$3,$4,$5) ON CONFLICT(actor_id,kind,request_key) DO UPDATE SET request_key=EXCLUDED.request_key RETURNING id`, actor, chat, kind, state, key).Scan(&id)
	if err != nil {
		return UserBackupJob{}, err
	}
	return s.UserBackupJob(ctx, actor, id)
}
func (s *Store) UserBackupJob(ctx context.Context, actor, id int64) (UserBackupJob, error) {
	var j UserBackupJob
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id,actor_id,chat_id,kind,state,backup_id,phase,error_code,summary,claimed_at,expires_at FROM user_backup_jobs WHERE id=$1 AND actor_id=$2`, id, actor).Scan(&j.ID, &j.Actor, &j.Chat, &j.Kind, &j.State, &j.BackupID, &j.Phase, &j.ErrorCode, &raw, &j.ClaimedAt, &j.ExpiresAt)
	if err == nil {
		err = json.Unmarshal(raw, &j.Summary)
	}
	return j, err
}
func (s *Store) PutUserBackupPart(ctx context.Context, actor, id int64, file []byte) (int, int, error) {
	if err := s.backupPermission(ctx, actor); err != nil {
		return 0, 0, err
	}
	part, err := s.readBackupPart(file)
	if err != nil {
		return 0, 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	var backupID string
	err = tx.QueryRow(ctx, `SELECT backup_id FROM user_backup_jobs WHERE id=$1 AND actor_id=$2 AND kind='restore' AND state='uploading' AND expires_at>now() FOR UPDATE`, id, actor).Scan(&backupID)
	if err != nil {
		return 0, 0, err
	}
	if backupID != "" && backupID != part.ID {
		return 0, 0, errors.New("this restore already contains a different backup")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_backup_parts(job_id,part,total,data) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, id, part.Part, part.Total, file); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE user_backup_jobs SET backup_id=$2 WHERE id=$1`, id, part.ID); err != nil {
		return 0, 0, err
	}
	var received int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM user_backup_parts WHERE job_id=$1`, id).Scan(&received); err != nil {
		return 0, 0, err
	}
	return received, part.Total, tx.Commit(ctx)
}
func (s *Store) UserBackupFiles(ctx context.Context, actor, id int64) ([][]byte, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.data FROM user_backup_parts p JOIN user_backup_jobs j ON j.id=p.job_id WHERE j.actor_id=$1 AND j.id=$2 AND j.expires_at>now() ORDER BY p.part`, actor, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files [][]byte
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		files = append(files, b)
	}
	return files, rows.Err()
}
func (s *Store) QueueUserBackup(ctx context.Context, actor, id int64, phase string) error {
	if err := s.backupPermission(ctx, actor); err != nil {
		return err
	}
	if phase != "inspect" && phase != "restore" {
		return errors.New("invalid restore phase")
	}
	expected := "uploading"
	if phase == "restore" {
		expected = "preview"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE user_backup_jobs SET state='queued',phase=$3,error_code='' WHERE id=$1 AND actor_id=$2 AND kind='restore' AND state=$4 AND expires_at>now()`, id, actor, phase, expected)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
func (s *Store) ClaimUserBackup(ctx context.Context) (UserBackupJob, error) {
	// Export sends that were interrupted may already have reached Telegram.
	if _, err := s.pool.Exec(ctx, `UPDATE user_backup_jobs SET state=CASE WHEN state='sending' THEN 'uncertain' ELSE 'queued' END,error_code=CASE WHEN state='sending' THEN 'DELIVERY_UNCONFIRMED' ELSE '' END WHERE state IN ('processing','sending') AND claimed_at<now()-interval '10 minutes'`); err != nil {
		return UserBackupJob{}, err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM user_backup_parts p USING user_backup_jobs j WHERE j.id=p.job_id AND j.expires_at<now()`); err != nil {
		return UserBackupJob{}, err
	}
	var id, actor int64
	err := s.pool.QueryRow(ctx, `WITH candidate AS(SELECT id FROM user_backup_jobs WHERE state='queued' AND expires_at>now() ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE user_backup_jobs j SET state='processing',claimed_at=now() FROM candidate c WHERE j.id=c.id RETURNING j.id,j.actor_id`).Scan(&id, &actor)
	if err != nil {
		return UserBackupJob{}, err
	}
	return s.UserBackupJob(ctx, actor, id)
}
func (s *Store) SaveExportUserBackup(ctx context.Context, j UserBackupJob, parts []BackupPart) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM user_backup_jobs WHERE id=$1 AND actor_id=$2 FOR UPDATE`, j.ID, j.Actor).Scan(&state); err != nil {
		return err
	}
	if state != "processing" {
		return errors.New("backup job is no longer processing")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_backup_parts WHERE job_id=$1`, j.ID); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err = tx.Exec(ctx, `INSERT INTO user_backup_parts(job_id,part,total,data) VALUES($1,$2,$3,$4)`, j.ID, p.Part, p.Total, p.File); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE user_backup_jobs SET state='sending',backup_id=$2,claimed_at=now() WHERE id=$1`, j.ID, parts[0].ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) FinishUserBackup(ctx context.Context, j UserBackupJob, state string, summary BackupPreview, code string) error {
	if state != "preview" && state != "succeeded" && state != "failed" && state != "uncertain" {
		return errors.New("invalid backup completion")
	}
	raw, _ := json.Marshal(summary)
	_, err := s.pool.Exec(ctx, `UPDATE user_backup_jobs SET state=$3,summary=$4,error_code=$5 WHERE id=$1 AND actor_id=$2 AND state IN ('processing','sending')`, j.ID, j.Actor, state, raw, code)
	return err
}

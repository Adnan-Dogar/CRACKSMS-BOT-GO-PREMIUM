package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
)

func (s *Store) EnqueuePanelEvents(ctx context.Context, panelID int64, events []domain.OTPEvent, cursor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lastSMS *time.Time
	for _, event := range events {
		t := event.ReceivedAt
		if event.ProviderTimestamp != nil {
			t = *event.ProviderTimestamp
		}
		if lastSMS == nil || t.After(*lastSMS) {
			copy := t
			lastSMS = &copy
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO panel_ingest_jobs(panel_id,bot_instance_id,dedup_key,payload)
			SELECT $1,$2,$3,$4 WHERE NOT EXISTS (
				SELECT 1 FROM otp_events WHERE dedup_key=$3)
			ON CONFLICT(panel_id,dedup_key) DO NOTHING`, panelID, instanceID(event.BotInstanceID), event.DedupKey, raw); err != nil {
			return err
		}
	}
	var progress struct {
		Page     int  `json:"page"`
		LastPage int  `json:"last_page"`
		Done     bool `json:"done"`
	}
	_ = json.Unmarshal([]byte(cursor), &progress)
	backlog := max(0, progress.LastPage-progress.Page+1)
	if progress.Done {
		backlog = 0
	}
	if _, err := tx.Exec(ctx, `UPDATE panels SET healthy=true,consecutive_failures=0,last_cursor=$2,
		last_sms_at=GREATEST(last_sms_at,$3::timestamptz),backlog_pages=$4,
		last_success_at=now(),last_error='',updated_at=now() WHERE id=$1`, panelID, cursor, lastSMS, backlog); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ClaimIngestJob(ctx context.Context) (domain.IngestJob, error) {
	var job domain.IngestJob
	var raw []byte
	err := s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM panel_ingest_jobs
		WHERE next_attempt_at<=now() AND (
		  state IN ('pending','retry') OR (state='processing' AND claimed_at<now()-interval '2 minutes'))
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE panel_ingest_jobs j SET state='processing',claimed_at=now(),attempts=j.attempts+1
		FROM candidate c WHERE j.id=c.id RETURNING j.id,j.payload,j.attempts`).Scan(&job.ID, &raw, &job.Attempts)
	if err != nil {
		return job, err
	}
	if err := json.Unmarshal(raw, &job.Event); err != nil {
		return job, err
	}
	return job, nil
}

func (s *Store) CompleteIngestJob(ctx context.Context, job domain.IngestJob, processErr error) error {
	if processErr == nil {
		// The accepted OTP row is the durable dedup record. Removing the raw queue
		// row keeps this high-volume table bounded without allowing a panel replay
		// to be processed again (EnqueuePanelEvents checks otp_events first).
		_, err := s.pool.Exec(ctx, `DELETE FROM panel_ingest_jobs WHERE id=$1`, job.ID)
		return err
	}
	if job.Attempts >= 8 {
		_, err := s.pool.Exec(ctx, `UPDATE panel_ingest_jobs SET state='failed',last_error=$2 WHERE id=$1`,
			job.ID, truncate(processErr.Error(), 500))
		return err
	}
	delay := time.Duration(1<<min(job.Attempts, 8)) * time.Second
	_, err := s.pool.Exec(ctx, `UPDATE panel_ingest_jobs SET state='retry',next_attempt_at=now()+$2::interval,last_error=$3
		WHERE id=$1`, job.ID, postgresInterval(delay), truncate(processErr.Error(), 500))
	return err
}

func (s *Store) PendingIngestCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM panel_ingest_jobs WHERE state IN ('pending','processing','retry')`).Scan(&count)
	return count, err
}

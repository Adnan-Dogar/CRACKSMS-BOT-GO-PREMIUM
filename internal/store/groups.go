package store

import (
	"context"
	"errors"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) UpsertOTPGroup(ctx context.Context, destination domain.OTPGroupDestination, createdBy int64) error {
	return s.UpsertOTPGroupForInstance(ctx, instanceID(destination.BotInstanceID), destination, createdBy)
}

func (s *Store) UpsertOTPGroupForInstance(ctx context.Context, botInstanceID int64, destination domain.OTPGroupDestination, createdBy int64) error {
	botInstanceID = instanceID(botInstanceID)
	if destination.OTPVisibility == "" {
		destination.OTPVisibility = "visible"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO otp_group_destinations(
		bot_instance_id,chat_id,title,buttons_enabled,enabled,healthy,created_by,otp_visibility,theme_id)
		VALUES($1,$2,$3,$4,$5,true,$6,$7,$8)
		ON CONFLICT(bot_instance_id,chat_id) DO UPDATE SET title=EXCLUDED.title,
		buttons_enabled=EXCLUDED.buttons_enabled,enabled=EXCLUDED.enabled,
		healthy=true,last_error='',otp_visibility=EXCLUDED.otp_visibility,theme_id=EXCLUDED.theme_id`,
		botInstanceID, destination.ChatID, destination.Title, destination.ButtonsEnabled,
		destination.Enabled, createdBy, destination.OTPVisibility, destination.ThemeID)
	return err
}

func (s *Store) ListOTPGroups(ctx context.Context) ([]domain.OTPGroupDestination, error) {
	return s.ListOTPGroupsForInstance(ctx, MainBotInstanceID)
}

func (s *Store) ListOTPGroupsForInstance(ctx context.Context, botInstanceID int64) ([]domain.OTPGroupDestination, error) {
	rows, err := s.pool.Query(ctx, `SELECT bot_instance_id,chat_id,title,buttons_enabled,enabled,healthy,last_error,last_success_at,
		otp_visibility,theme_id FROM otp_group_destinations WHERE bot_instance_id=$1 ORDER BY created_at,chat_id`, instanceID(botInstanceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OTPGroupDestination
	for rows.Next() {
		var item domain.OTPGroupDestination
		if err := rows.Scan(&item.BotInstanceID, &item.ChatID, &item.Title, &item.ButtonsEnabled, &item.Enabled,
			&item.Healthy, &item.LastError, &item.LastSuccessAt, &item.OTPVisibility, &item.ThemeID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SetOTPGroupButtons(ctx context.Context, chatID int64, enabled bool) error {
	return s.SetOTPGroupButtonsForInstance(ctx, MainBotInstanceID, chatID, enabled)
}

func (s *Store) SetOTPGroupButtonsForInstance(ctx context.Context, botInstanceID, chatID int64, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE otp_group_destinations SET buttons_enabled=$3 WHERE bot_instance_id=$1 AND chat_id=$2`,
		instanceID(botInstanceID), chatID, enabled)
	if err == nil && tag.RowsAffected() == 0 {
		err = pgx.ErrNoRows
	}
	return err
}

func (s *Store) SetOTPGroupEnabled(ctx context.Context, chatID int64, enabled bool) error {
	return s.SetOTPGroupEnabledForInstance(ctx, MainBotInstanceID, chatID, enabled)
}

func (s *Store) SetOTPGroupEnabledForInstance(ctx context.Context, botInstanceID, chatID int64, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE otp_group_destinations SET enabled=$3 WHERE bot_instance_id=$1 AND chat_id=$2`,
		instanceID(botInstanceID), chatID, enabled)
	if err == nil && tag.RowsAffected() == 0 {
		err = pgx.ErrNoRows
	}
	return err
}

func (s *Store) RemoveOTPGroup(ctx context.Context, chatID int64) error {
	return s.RemoveOTPGroupForInstance(ctx, MainBotInstanceID, chatID)
}

func (s *Store) RemoveOTPGroupForInstance(ctx context.Context, botInstanceID, chatID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM otp_group_destinations WHERE bot_instance_id=$1 AND chat_id=$2`, instanceID(botInstanceID), chatID)
	return err
}

func (s *Store) SetOTPGroupPrivacy(ctx context.Context, botInstanceID, chatID int64, privacy string) error {
	if privacy != "visible" && privacy != "masked" && privacy != "hidden" {
		return errors.New("privacy must be visible, masked, or hidden")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE otp_group_destinations SET otp_visibility=$3 WHERE bot_instance_id=$1 AND chat_id=$2`,
		instanceID(botInstanceID), chatID, privacy)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) SetOTPGroupTheme(ctx context.Context, botInstanceID, chatID int64, themeID *int) error {
	if themeID != nil && (*themeID < 0 || *themeID > 9) {
		return errors.New("theme must be between 0 and 9")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE otp_group_destinations SET theme_id=$3 WHERE bot_instance_id=$1 AND chat_id=$2`,
		instanceID(botInstanceID), chatID, themeID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) MarkOTPGroupDelivery(ctx context.Context, chatID int64, deliveryErr error) error {
	return s.MarkOTPGroupDeliveryForInstance(ctx, MainBotInstanceID, chatID, deliveryErr)
}

func (s *Store) MarkOTPGroupDeliveryForInstance(ctx context.Context, botInstanceID, chatID int64, deliveryErr error) error {
	if deliveryErr == nil {
		_, err := s.pool.Exec(ctx, `UPDATE otp_group_destinations
			SET healthy=true,last_error='',last_success_at=now() WHERE bot_instance_id=$1 AND chat_id=$2`, instanceID(botInstanceID), chatID)
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE otp_group_destinations SET healthy=false,last_error=$3 WHERE bot_instance_id=$1 AND chat_id=$2`,
		instanceID(botInstanceID), chatID, truncate(deliveryErr.Error(), 500))
	return err
}

func (s *Store) ClaimDeliveryJob(ctx context.Context) (domain.DeliveryJob, error) {
	var job domain.DeliveryJob
	var providerTimestamp *time.Time
	err := s.pool.QueryRow(ctx, `
		WITH candidate AS (
		  SELECT id FROM delivery_jobs
		  WHERE next_attempt_at<=now()
		    AND (state IN ('pending','retry') OR (state='sending' AND claimed_at<now()-interval '2 minutes'))
		  ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE delivery_jobs d SET state='sending',claimed_at=now(),attempts=d.attempts+1
		FROM candidate c,otp_events e
		WHERE d.id=c.id AND e.id=d.otp_event_id
		RETURNING d.id,d.bot_instance_id,d.target_kind,d.target_id,d.buttons_enabled,d.theme_id,d.otp_visibility,d.attempts,
		  e.id,e.bot_instance_id,COALESCE(e.panel_id,0),e.panel_name,e.phone,e.normalized_phone,e.service,e.country,e.message,e.code,e.provider_timestamp,e.received_at`,
	).Scan(&job.ID, &job.BotInstanceID, &job.TargetKind, &job.TargetID, &job.ButtonsEnabled, &job.ThemeID, &job.OTPVisibility, &job.Attempts,
		&job.Event.ID, &job.Event.BotInstanceID, &job.Event.PanelID, &job.Event.PanelName, &job.Event.Phone, &job.Event.NormalizedPhone,
		&job.Event.Service, &job.Event.Country, &job.Event.Message, &job.Event.Code, &providerTimestamp, &job.Event.ReceivedAt)
	job.Event.ProviderTimestamp = providerTimestamp
	return job, err
}

func (s *Store) CompleteDeliveryJob(ctx context.Context, job domain.DeliveryJob, deliveryErr error, retryAfter time.Duration, permanent bool) error {
	if deliveryErr == nil {
		_, err := s.pool.Exec(ctx, `UPDATE delivery_jobs SET state='sent',sent_at=now(),last_error='' WHERE id=$1`, job.ID)
		return err
	}
	if permanent || job.Attempts >= 8 {
		_, err := s.pool.Exec(ctx, `UPDATE delivery_jobs SET state='failed',last_error=$2 WHERE id=$1`,
			job.ID, truncate(deliveryErr.Error(), 500))
		return err
	}
	if retryAfter <= 0 {
		retryAfter = time.Duration(1<<min(job.Attempts, 8)) * time.Second
	}
	_, err := s.pool.Exec(ctx, `UPDATE delivery_jobs SET state='retry',next_attempt_at=now()+$2::interval,last_error=$3
		WHERE id=$1`, job.ID, postgresInterval(retryAfter), truncate(deliveryErr.Error(), 500))
	return err
}

func (s *Store) PendingDeliveryCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM delivery_jobs WHERE state IN ('pending','retry','sending')`).Scan(&count)
	return count, err
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func IsNoDeliveryJob(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

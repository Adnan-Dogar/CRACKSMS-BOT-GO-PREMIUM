package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/jackc/pgx/v5"
)

func randomSecret(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Store) CreateWebhook(ctx context.Context, botInstanceID, userID int64, endpointURL string, events []string) (int64, string, error) {
	if len(events) == 0 {
		events = []string{"otp.received"}
	}
	secret, err := randomSecret(32)
	if err != nil {
		return 0, "", err
	}
	encrypted, err := s.cipher.Encrypt([]byte(secret))
	if err != nil {
		return 0, "", err
	}
	envelope, _ := json.Marshal(map[string]string{"encrypted": encrypted})
	var id int64
	err = s.pool.QueryRow(ctx, `INSERT INTO webhook_endpoints(bot_instance_id,user_id,url,secret_config,events)
		VALUES($1,$2,$3,$4,$5) RETURNING id`, instanceID(botInstanceID), userID, endpointURL, envelope, events).Scan(&id)
	return id, secret, err
}

func (s *Store) ListWebhooks(ctx context.Context, botInstanceID, userID int64) ([]domain.WebhookEndpoint, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,bot_instance_id,user_id,url,events,enabled,consecutive_failures,last_error
		FROM webhook_endpoints WHERE bot_instance_id=$1 AND user_id=$2 ORDER BY id`, instanceID(botInstanceID), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.WebhookEndpoint
	for rows.Next() {
		var endpoint domain.WebhookEndpoint
		if err := rows.Scan(&endpoint.ID, &endpoint.BotInstanceID, &endpoint.UserID, &endpoint.URL, &endpoint.Events,
			&endpoint.Enabled, &endpoint.ConsecutiveFailures, &endpoint.LastError); err != nil {
			return nil, err
		}
		out = append(out, endpoint)
	}
	return out, rows.Err()
}

func (s *Store) RemoveWebhook(ctx context.Context, botInstanceID, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id=$1 AND bot_instance_id=$2 AND user_id=$3`, id, instanceID(botInstanceID), userID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) ClaimWebhookDelivery(ctx context.Context) (domain.WebhookDelivery, error) {
	var delivery domain.WebhookDelivery
	var secretRaw []byte
	var providerTimestamp *time.Time
	err := s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM webhook_deliveries WHERE next_attempt_at<=now() AND (
		state IN ('pending','retry') OR (state='sending' AND claimed_at<now()-interval '2 minutes'))
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE webhook_deliveries wd SET state='sending',claimed_at=now(),attempts=wd.attempts+1
		FROM candidate c,webhook_endpoints we,otp_events e
		WHERE wd.id=c.id AND we.id=wd.endpoint_id AND e.id=wd.otp_event_id
		RETURNING wd.id,wd.event_name,wd.attempts,we.id,we.bot_instance_id,we.user_id,we.url,we.secret_config,we.events,
		we.enabled,we.consecutive_failures,we.last_error,e.id,e.bot_instance_id,COALESCE(e.panel_id,0),e.panel_name,
		e.phone,e.normalized_phone,e.service,e.country,e.message,e.code,e.provider_timestamp,e.received_at`,
	).Scan(&delivery.ID, &delivery.EventName, &delivery.Attempts, &delivery.Endpoint.ID, &delivery.Endpoint.BotInstanceID,
		&delivery.Endpoint.UserID, &delivery.Endpoint.URL, &secretRaw, &delivery.Endpoint.Events, &delivery.Endpoint.Enabled,
		&delivery.Endpoint.ConsecutiveFailures, &delivery.Endpoint.LastError, &delivery.Event.ID, &delivery.Event.BotInstanceID,
		&delivery.Event.PanelID, &delivery.Event.PanelName, &delivery.Event.Phone, &delivery.Event.NormalizedPhone,
		&delivery.Event.Service, &delivery.Event.Country, &delivery.Event.Message, &delivery.Event.Code, &providerTimestamp, &delivery.Event.ReceivedAt)
	if err != nil {
		return delivery, err
	}
	delivery.Event.ProviderTimestamp = providerTimestamp
	var envelope struct {
		Encrypted string `json:"encrypted"`
	}
	if err := json.Unmarshal(secretRaw, &envelope); err != nil {
		return delivery, err
	}
	plain, err := s.cipher.Decrypt(envelope.Encrypted)
	delivery.Endpoint.Secret = string(plain)
	return delivery, err
}

func (s *Store) CompleteWebhookDelivery(ctx context.Context, delivery domain.WebhookDelivery, statusCode int, deliveryErr error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if deliveryErr == nil && statusCode >= 200 && statusCode < 300 {
		if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET state='sent',response_code=$2,sent_at=now(),last_error='' WHERE id=$1`, delivery.ID, statusCode); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET consecutive_failures=0,last_success_at=now(),last_error='' WHERE id=$1`, delivery.Endpoint.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	errorText := fmt.Sprintf("HTTP %d", statusCode)
	if deliveryErr != nil {
		errorText = deliveryErr.Error()
	}
	state := "retry"
	if delivery.Attempts >= 8 {
		state = "failed"
	}
	delay := time.Duration(1<<min(delivery.Attempts, 8)) * time.Second
	if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET state=$2,response_code=NULLIF($3,0),
		next_attempt_at=now()+$4::interval,last_error=$5 WHERE id=$1`, delivery.ID, state, statusCode,
		postgresInterval(delay), truncate(errorText, 500)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET consecutive_failures=consecutive_failures+1,last_error=$2,
		enabled=CASE WHEN consecutive_failures+1>=20 THEN false ELSE enabled END WHERE id=$1`,
		delivery.Endpoint.ID, truncate(errorText, 500)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateScheduledMessage(ctx context.Context, message domain.ScheduledMessage) (int64, error) {
	if message.TargetKind != "user" && message.TargetKind != "group" && message.TargetKind != "all_users" {
		return 0, invalidInput("target must be user, group, or all_users")
	}
	if strings.TrimSpace(message.Body) == "" || !message.DeliverAt.After(time.Now()) {
		return 0, invalidInput("message body and a future delivery time are required")
	}
	if message.ParseMode == "" {
		message.ParseMode = "HTML"
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO scheduled_messages(
		bot_instance_id,creator_user_id,target_kind,target_id,body,parse_mode,deliver_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, instanceID(message.BotInstanceID), message.CreatorUserID,
		message.TargetKind, message.TargetID, message.Body, message.ParseMode, message.DeliverAt).Scan(&id)
	return id, err
}

func (s *Store) ListScheduledMessages(ctx context.Context, botInstanceID, creatorID int64) ([]domain.ScheduledMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,bot_instance_id,creator_user_id,target_kind,target_id,body,parse_mode,deliver_at,attempts
		FROM scheduled_messages WHERE bot_instance_id=$1 AND creator_user_id=$2 AND state='pending' ORDER BY deliver_at`,
		instanceID(botInstanceID), creatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ScheduledMessage
	for rows.Next() {
		var item domain.ScheduledMessage
		if err := rows.Scan(&item.ID, &item.BotInstanceID, &item.CreatorUserID, &item.TargetKind, &item.TargetID,
			&item.Body, &item.ParseMode, &item.DeliverAt, &item.Attempts); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CancelScheduledMessage(ctx context.Context, botInstanceID, creatorID, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE scheduled_messages SET state='cancelled' WHERE id=$1 AND bot_instance_id=$2
		AND creator_user_id=$3 AND state='pending'`, id, instanceID(botInstanceID), creatorID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) ClaimScheduledMessage(ctx context.Context) (domain.ScheduledMessage, error) {
	var item domain.ScheduledMessage
	err := s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM scheduled_messages WHERE deliver_at<=now() AND (
		state='pending' OR (state='sending' AND claimed_at<now()-interval '2 minutes'))
		ORDER BY deliver_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE scheduled_messages sm SET state='sending',claimed_at=now(),attempts=sm.attempts+1
		FROM candidate c WHERE sm.id=c.id RETURNING sm.id,sm.bot_instance_id,sm.creator_user_id,
		sm.target_kind,sm.target_id,sm.body,sm.parse_mode,sm.deliver_at,sm.attempts`).Scan(&item.ID,
		&item.BotInstanceID, &item.CreatorUserID, &item.TargetKind, &item.TargetID, &item.Body,
		&item.ParseMode, &item.DeliverAt, &item.Attempts)
	return item, err
}

func (s *Store) CompleteScheduledMessage(ctx context.Context, item domain.ScheduledMessage, sendErr error) error {
	if sendErr == nil {
		_, err := s.pool.Exec(ctx, `UPDATE scheduled_messages SET state='sent',sent_at=now(),last_error='' WHERE id=$1`, item.ID)
		return err
	}
	state := "pending"
	if item.Attempts >= 8 {
		state = "failed"
	}
	_, err := s.pool.Exec(ctx, `UPDATE scheduled_messages SET state=$2,deliver_at=now()+$3::interval,last_error=$4 WHERE id=$1`,
		item.ID, state, postgresInterval(time.Duration(1<<min(item.Attempts, 8))*time.Second), truncate(sendErr.Error(), 500))
	return err
}

func (s *Store) CreateAPIKey(ctx context.Context, botInstanceID, userID int64, name string, scopes []string, expiresAt *time.Time) (domain.APIKey, string, error) {
	if len(scopes) == 0 {
		scopes = []string{"analytics:read", "history:read"}
	}
	secret, err := randomSecret(32)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	rawKey := "csk_" + secret
	hash := sha256.Sum256([]byte(rawKey))
	prefix := rawKey[:12]
	var key domain.APIKey
	err = s.pool.QueryRow(ctx, `INSERT INTO api_keys(bot_instance_id,user_id,name,key_prefix,key_hash,scopes,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,bot_instance_id,user_id,name,key_prefix,scopes,enabled,expires_at,last_used_at`,
		instanceID(botInstanceID), userID, name, prefix, hex.EncodeToString(hash[:]), scopes, expiresAt).
		Scan(&key.ID, &key.BotInstanceID, &key.UserID, &key.Name, &key.Prefix, &key.Scopes, &key.Enabled, &key.ExpiresAt, &key.LastUsedAt)
	return key, rawKey, err
}

func (s *Store) AuthenticateAPIKey(ctx context.Context, rawKey string) (domain.APIKey, error) {
	hash := sha256.Sum256([]byte(rawKey))
	var key domain.APIKey
	err := s.pool.QueryRow(ctx, `UPDATE api_keys SET last_used_at=now() WHERE key_hash=$1 AND enabled
		AND (expires_at IS NULL OR expires_at>now()) RETURNING id,bot_instance_id,user_id,name,key_prefix,scopes,enabled,expires_at,last_used_at`,
		hex.EncodeToString(hash[:])).Scan(&key.ID, &key.BotInstanceID, &key.UserID, &key.Name, &key.Prefix,
		&key.Scopes, &key.Enabled, &key.ExpiresAt, &key.LastUsedAt)
	return key, err
}

func (s *Store) ListAPIKeys(ctx context.Context, botInstanceID, userID int64) ([]domain.APIKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,bot_instance_id,user_id,name,key_prefix,scopes,enabled,expires_at,last_used_at
		FROM api_keys WHERE bot_instance_id=$1 AND user_id=$2 ORDER BY id`, instanceID(botInstanceID), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.APIKey
	for rows.Next() {
		var key domain.APIKey
		if err := rows.Scan(&key.ID, &key.BotInstanceID, &key.UserID, &key.Name, &key.Prefix,
			&key.Scopes, &key.Enabled, &key.ExpiresAt, &key.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAPIKey(ctx context.Context, botInstanceID, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET enabled=false WHERE id=$1 AND bot_instance_id=$2 AND user_id=$3`,
		id, instanceID(botInstanceID), userID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/jackc/pgx/v5"
)

// UsePanelTemplate creates an independent child-owned copy without credentials.
func (s *Store) UsePanelTemplate(ctx context.Context, instance, source int64) (int64, error) {
	if instanceID(instance) == MainBotInstanceID {
		return 0, errors.New("select a child bot")
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO panel_sources(bot_instance_id,name,kind,url,origin_source_id)
 SELECT $1,CASE WHEN EXISTS(SELECT 1 FROM panel_sources x WHERE x.bot_instance_id=$1 AND x.name=s.name) THEN s.name||' · Main #'||s.id ELSE s.name END,s.kind,s.url,s.id
 FROM panel_sources s JOIN bot_instances b ON b.id=$1 AND b.parent_id=s.bot_instance_id
 WHERE s.id=$2 AND s.bot_instance_id=1
 ON CONFLICT(bot_instance_id,origin_source_id) WHERE origin_source_id IS NOT NULL DO UPDATE SET origin_source_id=EXCLUDED.origin_source_id RETURNING id`, instance, source).Scan(&id)
	return id, err
}
func (s *Store) SetChildOTPSharing(ctx context.Context, actor, child int64, enabled bool) error {
	allowed, err := s.IsAdmin(ctx, actor, "manage_bots")
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("main-bot management permission is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE bot_instances SET share_main_otps=$2,otp_share_enabled_at=CASE WHEN $2 AND NOT share_main_otps THEN now() WHEN NOT $2 THEN NULL ELSE otp_share_enabled_at END WHERE id=$1 AND parent_id=1 AND NOT is_main`, child, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if !enabled {
		if _, err = tx.Exec(ctx, `DELETE FROM panel_ingest_jobs WHERE bot_instance_id=$1 AND shared_from_event_id IS NOT NULL`, child); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE delivery_jobs j SET state='failed',last_error='Main OTP sharing disabled' FROM otp_events e WHERE j.otp_event_id=e.id AND e.bot_instance_id=$1 AND e.shared_from_event_id IS NOT NULL AND j.state IN ('pending','retry')`, child); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_log(bot_instance_id,actor_user_id,action,target_type,target_id,metadata) VALUES(1,$1,'child.otp_sharing','bot',$2,$3)`, actor, fmt.Sprint(child), json.RawMessage(fmt.Sprintf(`{"enabled":%t}`, enabled))); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Only newly accepted main events can create copies; this runs in the acceptance
// transaction so a crash cannot lose the fanout or cause historical replay.
func enqueueChildCopies(ctx context.Context, tx pgx.Tx, event domain.OTPEvent) error {
	if event.BotInstanceID != MainBotInstanceID || event.PanelID == 0 || event.Code == "" || event.SharedFromEventID != "" {
		return nil
	}
	eventTime := event.ReceivedAt
	if event.ProviderTimestamp != nil {
		eventTime = *event.ProviderTimestamp
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT b.id FROM bot_instances b JOIN assignments a ON a.bot_instance_id=b.id JOIN assignment_numbers an ON an.assignment_id=a.id JOIN numbers n ON n.id=an.number_id
 WHERE b.parent_id=1 AND b.share_main_otps AND b.enabled AND b.status IN ('approved','running','error')
 AND $4>=b.otp_share_enabled_at AND $3>=b.otp_share_enabled_at AND a.state='active' AND a.expires_at>now() AND $3 BETWEEN a.assigned_at AND a.expires_at
 AND n.normalized_phone=$1 AND n.service_key=lower(btrim($2)) AND n.state='assigned' AND an.consumed_at IS NULL AND an.released_at IS NULL`, event.NormalizedPhone, event.Service, eventTime, event.ReceivedAt)
	if err != nil {
		return err
	}
	var children []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		children = append(children, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range children {
		copy := event
		copy.ID = ""
		copy.PanelID = 0
		copy.BotInstanceID = id
		copy.SharedFromEventID = event.ID
		copy.LegacyDedupKey = ""
		copy.DedupKey = DedupKey(fmt.Sprintf("shared:%d", id), event.NormalizedPhone, event.DedupKey)
		raw, err := json.Marshal(copy)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO panel_ingest_jobs(panel_id,bot_instance_id,dedup_key,payload,shared_from_event_id) VALUES(NULL,$1,$2,$3,$4) ON CONFLICT DO NOTHING`, id, copy.DedupKey, raw, event.ID); err != nil {
			return err
		}
	}
	return nil
}

func sharedEventAllowed(ctx context.Context, tx pgx.Tx, event domain.OTPEvent) (bool, error) {
	if event.SharedFromEventID == "" {
		return true, nil
	}
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bot_instances b JOIN otp_events e ON e.id=$2 WHERE b.id=$1 AND b.parent_id=1 AND b.share_main_otps AND b.enabled AND b.status IN ('approved','running','error') AND e.bot_instance_id=1 AND e.received_at>=b.otp_share_enabled_at AND COALESCE(e.provider_timestamp,e.received_at)>=b.otp_share_enabled_at AND EXISTS(SELECT 1 FROM assignments a JOIN assignment_numbers an ON an.assignment_id=a.id JOIN numbers n ON n.id=an.number_id WHERE a.bot_instance_id=b.id AND a.state='active' AND a.expires_at>now() AND COALESCE(e.provider_timestamp,e.received_at) BETWEEN a.assigned_at AND a.expires_at AND an.consumed_at IS NULL AND an.released_at IS NULL AND n.normalized_phone=e.normalized_phone AND n.service_key=lower(btrim(e.service))))`, event.BotInstanceID, event.SharedFromEventID).Scan(&enabled)
	return enabled, err
}

// The delivery worker checks again so queued shared OTPs stop when disabled.
func (s *Store) SharedDeliveryAllowed(ctx context.Context, eventID string) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT e.shared_from_event_id IS NULL OR (b.share_main_otps AND b.enabled AND e.received_at>=b.otp_share_enabled_at AND COALESCE(e.provider_timestamp,e.received_at)>=b.otp_share_enabled_at AND EXISTS(SELECT 1 FROM assignments a JOIN assignment_numbers an ON an.assignment_id=a.id JOIN numbers n ON n.id=an.number_id WHERE a.bot_instance_id=b.id AND a.state='active' AND a.expires_at>now() AND COALESCE(e.provider_timestamp,e.received_at) BETWEEN a.assigned_at AND a.expires_at AND an.consumed_at IS NULL AND an.released_at IS NULL AND n.normalized_phone=e.normalized_phone AND n.service_key=lower(btrim(e.service)))) FROM otp_events e JOIN bot_instances b ON b.id=e.bot_instance_id WHERE e.id=$1`, eventID).Scan(&allowed)
	return allowed, err
}

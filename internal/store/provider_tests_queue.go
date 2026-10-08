package store

import (
	"context"
	"time"
)

type ConnectionTest struct {
	PanelID, Instance int64
	Enable            bool
	Fingerprint       string
}

func (s *Store) SchedulePanelTest(ctx context.Context, instance, panel int64, at time.Time, enable bool) error {
	_, e := s.pool.Exec(ctx, `INSERT INTO panel_connection_tests(panel_id,next_attempt_at,enable_after,config_fingerprint) SELECT id,$3,$4,md5(config::text) FROM panels WHERE bot_instance_id=$1 AND id=$2 ON CONFLICT(panel_id) DO UPDATE SET next_attempt_at=EXCLUDED.next_attempt_at,enable_after=EXCLUDED.enable_after,config_fingerprint=EXCLUDED.config_fingerprint`, instanceID(instance), panel, at, enable)
	if e == nil {
		e = s.UpdateProviderState(ctx, panel, "Connection test queued", at)
	}
	return e
}
func (s *Store) ClaimPanelTest(ctx context.Context) (ConnectionTest, error) {
	v := ConnectionTest{}
	e := s.pool.QueryRow(ctx, `WITH candidate AS(SELECT panel_id FROM panel_connection_tests WHERE next_attempt_at<=now() ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE panel_connection_tests j SET next_attempt_at=now()+interval '2 minutes' FROM candidate c,panels p WHERE j.panel_id=c.panel_id AND p.id=j.panel_id RETURNING j.panel_id,p.bot_instance_id,j.enable_after,j.config_fingerprint`).Scan(&v.PanelID, &v.Instance, &v.Enable, &v.Fingerprint)
	return v, e
}
func (s *Store) FinishPanelTest(ctx context.Context, v ConnectionTest, healthy bool, status string, retry time.Time) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if !retry.IsZero() {
		_, e = tx.Exec(ctx, `UPDATE panel_connection_tests SET next_attempt_at=$3 WHERE panel_id=$1 AND config_fingerprint=$2`, v.PanelID, v.Fingerprint, retry)
	} else {
		_, e = tx.Exec(ctx, `DELETE FROM panel_connection_tests WHERE panel_id=$1 AND config_fingerprint=$2`, v.PanelID, v.Fingerprint)
	}
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE panels SET healthy=$3,enabled=CASE WHEN $3 AND $4 THEN true ELSE enabled END,connection_status=$5,next_retry_at=$6,last_error=CASE WHEN $3 THEN '' ELSE $5 END,last_success_at=CASE WHEN $3 THEN now() ELSE last_success_at END WHERE id=$1 AND md5(config::text)=$2`, v.PanelID, v.Fingerprint, healthy, v.Enable, status, nullableTime(retry))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

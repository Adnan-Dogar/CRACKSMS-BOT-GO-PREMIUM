package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type Broadcast struct {
	ID, Instance, Creator, Recipient                 int64
	Kind, Body, FileID, Audience, State              string
	Entities                                         json.RawMessage
	Total, Sent, Failed, Skipped, Uncertain, Pending int
}

const broadcastAudienceSQL = `SELECT u.user_id FROM bot_instance_users u LEFT JOIN user_subscriptions s ON s.bot_instance_id=u.bot_instance_id AND s.user_id=u.user_id WHERE u.bot_instance_id=$1 AND ($2='all' OR ($2='active' AND u.last_active_at>now()-interval '7 days') OR $2=CASE WHEN s.status='active' AND (s.expires_at IS NULL OR s.expires_at>now()) THEN s.tier ELSE 'free' END)`

func (s *Store) BroadcastAudienceCount(ctx context.Context, instance int64, audience string) (int, error) {
	var n int
	e := s.pool.QueryRow(ctx, `SELECT count(*) FROM (`+broadcastAudienceSQL+`) q`, instanceID(instance), audience).Scan(&n)
	return n, e
}
func (s *Store) CreateBroadcast(ctx context.Context, b Broadcast, key string) (int64, error) {
	if b.Kind != "text" && b.Kind != "photo" && b.Kind != "video" {
		return 0, errors.New("unsupported broadcast content")
	}
	if b.Audience != "all" && b.Audience != "active" && b.Audience != "free" && b.Audience != "pro" && b.Audience != "enterprise" {
		return 0, errors.New("invalid audience")
	}
	if len(b.Entities) == 0 {
		b.Entities = json.RawMessage(`[]`)
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	var id int64
	e = tx.QueryRow(ctx, `INSERT INTO broadcasts(bot_instance_id,creator_id,confirmation_key,kind,body,entities,file_id,audience,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'preparing') ON CONFLICT(bot_instance_id,creator_id,confirmation_key) DO NOTHING RETURNING id`, instanceID(b.Instance), b.Creator, key, b.Kind, b.Body, b.Entities, b.FileID, b.Audience).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		e = tx.QueryRow(ctx, `SELECT id FROM broadcasts WHERE bot_instance_id=$1 AND creator_id=$2 AND confirmation_key=$3`, instanceID(b.Instance), b.Creator, key).Scan(&id)
		if e != nil {
			return 0, e
		}
		return id, tx.Commit(ctx)
	}
	if e != nil {
		return 0, e
	}
	return id, tx.Commit(ctx)
}
func (s *Store) BroadcastStatus(ctx context.Context, instance, id int64) (Broadcast, error) {
	var b Broadcast
	e := s.pool.QueryRow(ctx, `SELECT b.id,b.state,count(r.user_id),count(*) FILTER(WHERE r.state='sent'),count(*) FILTER(WHERE r.state='failed'),count(*) FILTER(WHERE r.state='skipped'),count(*) FILTER(WHERE r.state='uncertain'),count(*) FILTER(WHERE r.state IN ('pending','sending')) FROM broadcasts b LEFT JOIN broadcast_recipients r ON r.broadcast_id=b.id WHERE b.id=$1 AND b.bot_instance_id=$2 GROUP BY b.id`, id, instanceID(instance)).Scan(&b.ID, &b.State, &b.Total, &b.Sent, &b.Failed, &b.Skipped, &b.Uncertain, &b.Pending)
	return b, e
}
func (s *Store) CancelBroadcast(ctx context.Context, instance, id int64) error {
	_, e := s.pool.Exec(ctx, `WITH stopped AS (UPDATE broadcasts SET state='cancelled' WHERE id=$1 AND bot_instance_id=$2 RETURNING id) UPDATE broadcast_recipients SET state='skipped' WHERE broadcast_id IN(SELECT id FROM stopped) AND state='pending'`, id, instanceID(instance))
	return e
}
func (s *Store) ClaimBroadcast(ctx context.Context) (Broadcast, error) {
	var b Broadcast
	// An interrupted request may have reached Telegram. Do not resend it automatically.
	_, e := s.pool.Exec(ctx, `UPDATE broadcast_recipients SET state='uncertain',last_error='Worker interrupted; delivery unconfirmed' WHERE state='sending' AND claimed_at<now()-interval '5 minutes'`)
	if e != nil {
		return b, e
	}
	_, e = s.pool.Exec(ctx, `UPDATE broadcasts b SET state='completed' WHERE state IN('pending','running') AND NOT EXISTS(SELECT 1 FROM broadcast_recipients r WHERE r.broadcast_id=b.id AND r.state IN('pending','sending'))`)
	if e != nil {
		return b, e
	}
	e = s.pool.QueryRow(ctx, `WITH candidate AS (SELECT r.broadcast_id,r.user_id FROM broadcast_recipients r JOIN broadcasts b ON b.id=r.broadcast_id WHERE r.state='pending' AND r.next_attempt_at<=now() AND b.state IN('pending','running') ORDER BY r.next_attempt_at FOR UPDATE OF r SKIP LOCKED LIMIT 1), claimed AS(UPDATE broadcast_recipients r SET state='sending',attempts=attempts+1,claimed_at=now() FROM candidate c WHERE r.broadcast_id=c.broadcast_id AND r.user_id=c.user_id RETURNING r.broadcast_id,r.user_id) SELECT b.id,b.bot_instance_id,b.kind,b.body,b.entities,b.file_id,c.user_id FROM claimed c JOIN broadcasts b ON b.id=c.broadcast_id`).Scan(&b.ID, &b.Instance, &b.Kind, &b.Body, &b.Entities, &b.FileID, &b.Recipient)
	if e == nil {
		_, _ = s.pool.Exec(ctx, `UPDATE broadcasts SET state='running' WHERE id=$1 AND state='pending'`, b.ID)
	}
	return b, e
}
func (s *Store) FinishBroadcastRecipient(ctx context.Context, b Broadcast, state string, delay time.Duration) error {
	_, e := s.pool.Exec(ctx, `UPDATE broadcast_recipients SET state=CASE WHEN $3='pending' AND EXISTS(SELECT 1 FROM broadcasts WHERE id=$1 AND state='cancelled') THEN 'skipped' ELSE $3 END,next_attempt_at=now()+make_interval(secs => $4),last_error=CASE WHEN $3='uncertain' THEN 'Delivery unconfirmed' ELSE '' END WHERE broadcast_id=$1 AND user_id=$2 AND state='sending'`, b.ID, b.Recipient, state, delay.Seconds())
	return e
}

func (s *Store) RecentBroadcasts(ctx context.Context, instance int64) ([]Broadcast, error) {
	rows, e := s.pool.Query(ctx, `SELECT id,state,audience,kind FROM broadcasts WHERE bot_instance_id=$1 ORDER BY id DESC LIMIT 10`, instanceID(instance))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Broadcast
	for rows.Next() {
		var b Broadcast
		if e = rows.Scan(&b.ID, &b.State, &b.Audience, &b.Kind); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Bounded transactions let cancellation and admin status proceed while a large
// audience is prepared. A crash resumes at the durable cursor.
func (s *Store) PrepareBroadcastBatch(ctx context.Context) (bool, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	var id, instance, cursor int64
	var audience string
	var cutoff time.Time
	e = tx.QueryRow(ctx, `SELECT id,bot_instance_id,audience,prepare_cursor,prepare_before FROM broadcasts WHERE state='preparing' ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &instance, &audience, &cursor, &cutoff)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var next int64
	e = tx.QueryRow(ctx, `WITH audience AS (
 SELECT u.user_id FROM bot_instance_users u LEFT JOIN user_subscriptions s ON s.bot_instance_id=u.bot_instance_id AND s.user_id=u.user_id
 WHERE u.bot_instance_id=$1 AND u.user_id>$3 AND u.joined_at<=$4 AND ($2='all' OR ($2='active' AND u.last_active_at>$4::timestamptz-interval '7 days') OR $2=CASE WHEN s.status='active' AND s.starts_at<=$4 AND (s.expires_at IS NULL OR s.expires_at>$4) THEN s.tier ELSE 'free' END)
 ORDER BY u.user_id LIMIT 1000), added AS (INSERT INTO broadcast_recipients(broadcast_id,user_id) SELECT $5,user_id FROM audience ON CONFLICT DO NOTHING RETURNING user_id)
 SELECT COALESCE(max(user_id),0) FROM audience`, instance, audience, cursor, cutoff, id).Scan(&next)
	if e != nil {
		return false, e
	}
	if next == 0 {
		_, e = tx.Exec(ctx, `UPDATE broadcasts SET state='pending' WHERE id=$1`, id)
	} else {
		_, e = tx.Exec(ctx, `UPDATE broadcasts SET prepare_cursor=$2 WHERE id=$1`, id, next)
	}
	if e != nil {
		return false, e
	}
	return true, tx.Commit(ctx)
}

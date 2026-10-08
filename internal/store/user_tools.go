package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SavedSelection struct {
	ID               int64
	Service, Country string
}

func (s *Store) SetDisplayFormat(ctx context.Context, instance, user int64, format string) error {
	if format != "auto" && format != "rich" && format != "classic" {
		return errors.New("invalid display format")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO user_preferences(bot_instance_id,user_id,display_format) VALUES($1,$2,$3)
		ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET display_format=EXCLUDED.display_format,updated_at=now()`, instanceID(instance), user, format)
	return err
}
func (s *Store) SaveLastSelection(ctx context.Context, instance, user int64, service, country string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO user_last_selections(bot_instance_id,user_id,service,country) VALUES($1,$2,$3,$4)
		ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET service=EXCLUDED.service,country=EXCLUDED.country,updated_at=now()`, instanceID(instance), user, service, country)
	return err
}
func (s *Store) LastSelection(ctx context.Context, instance, user int64) (SavedSelection, error) {
	var v SavedSelection
	err := s.pool.QueryRow(ctx, `SELECT service,country FROM user_last_selections WHERE bot_instance_id=$1 AND user_id=$2`, instanceID(instance), user).Scan(&v.Service, &v.Country)
	return v, err
}
func (s *Store) SavedSelections(ctx context.Context, instance, user int64, watches bool) ([]SavedSelection, error) {
	query := `SELECT id,service,country FROM user_favorites WHERE bot_instance_id=$1 AND user_id=$2 ORDER BY id`
	if watches {
		query = `SELECT id,service,country FROM availability_watches WHERE bot_instance_id=$1 AND user_id=$2 AND enabled ORDER BY id`
	}
	rows, err := s.pool.Query(ctx, query, instanceID(instance), user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedSelection
	for rows.Next() {
		var v SavedSelection
		if err = rows.Scan(&v.ID, &v.Service, &v.Country); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) AddSavedSelection(ctx context.Context, instance, user int64, service, country string, watches bool) (int64, error) {
	instance = instanceID(instance)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	// Serialize the per-user limits, including duplicate button presses.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmtUserKey(instance, user)); err != nil {
		return 0, err
	}
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_countries WHERE service=$1 AND country=$2 AND enabled)`, service, country).Scan(&enabled); err != nil {
		return 0, err
	}
	if !enabled {
		return 0, errors.New("this selection is no longer configured")
	}
	var id int64
	query := `SELECT id FROM user_favorites WHERE bot_instance_id=$1 AND user_id=$2 AND service=$3 AND country=$4`
	if watches {
		query = `SELECT id FROM availability_watches WHERE bot_instance_id=$1 AND user_id=$2 AND service=$3 AND country=$4 AND enabled`
	}
	err = tx.QueryRow(ctx, query, instance, user, service, country).Scan(&id)
	if err == nil {
		return id, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	limit := 20
	query = `SELECT count(*) FROM user_favorites WHERE bot_instance_id=$1 AND user_id=$2`
	if watches {
		limit = 10
		query = `SELECT count(*) FROM availability_watches WHERE bot_instance_id=$1 AND user_id=$2 AND enabled`
	}
	var count int
	if err = tx.QueryRow(ctx, query, instance, user).Scan(&count); err != nil {
		return 0, err
	}
	if count >= limit {
		return 0, errors.New("saved selection limit reached; remove an existing entry first")
	}
	query = `INSERT INTO user_favorites(bot_instance_id,user_id,service,country) VALUES($1,$2,$3,$4) RETURNING id`
	if watches {
		query = `INSERT INTO availability_watches(bot_instance_id,user_id,service,country,was_available)
		VALUES($1,$2,$3,$4,EXISTS(SELECT 1 FROM numbers n WHERE n.service=$3 AND n.country=$4 AND n.state='available'
		AND NOT EXISTS(SELECT 1 FROM number_user_exclusions e WHERE e.number_id=n.id AND e.user_id=$2 AND e.expires_at>now())))
		ON CONFLICT(bot_instance_id,user_id,service,country) DO UPDATE SET enabled=true,was_available=EXCLUDED.was_available RETURNING id`
	}
	if err = tx.QueryRow(ctx, query, instance, user, service, country).Scan(&id); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}
func fmtUserKey(instance, user int64) string { return fmt.Sprintf("tools:%d:%d", instance, user) }
func (s *Store) RemoveSavedSelection(ctx context.Context, instance, user, id int64, watches bool) error {
	query := `DELETE FROM user_favorites WHERE bot_instance_id=$1 AND user_id=$2 AND id=$3`
	if watches {
		query = `UPDATE availability_watches SET enabled=false WHERE bot_instance_id=$1 AND user_id=$2 AND id=$3`
	}
	_, err := s.pool.Exec(ctx, query, instanceID(instance), user, id)
	return err
}

func (s *Store) PauseLiveScreens(ctx context.Context, instance int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE live_screen_sessions SET paused=true WHERE bot_instance_id=$1`, instanceID(instance))
	return err
}
func (s *Store) SaveLiveScreen(ctx context.Context, instance, user int64, message int, view, period string, page int, paused bool, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO live_screen_sessions(bot_instance_id,user_id,message_id,view,period,page,paused,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET message_id=EXCLUDED.message_id,view=EXCLUDED.view,period=EXCLUDED.period,page=EXCLUDED.page,paused=EXCLUDED.paused,expires_at=EXCLUDED.expires_at,updated_at=now()`, instanceID(instance), user, message, view, period, page, paused, expires)
	return err
}

type StoredLiveScreen struct {
	Message      int
	View, Period string
	Page         int
	Expires      time.Time
}

func (s *Store) LiveScreen(ctx context.Context, instance, user int64) (StoredLiveScreen, error) {
	var v StoredLiveScreen
	err := s.pool.QueryRow(ctx, `SELECT message_id,view,period,page,expires_at FROM live_screen_sessions WHERE bot_instance_id=$1 AND user_id=$2`, instanceID(instance), user).Scan(&v.Message, &v.View, &v.Period, &v.Page, &v.Expires)
	return v, err
}
func (s *Store) UserBlocked(ctx context.Context, user int64) (bool, error) {
	var banned bool
	err := s.pool.QueryRow(ctx, `SELECT banned FROM users WHERE id=$1`, user).Scan(&banned)
	return banned, err
}

// Queue transitions in the same transaction that records their state. The
// partial unique index coalesces alerts while a delivery is outstanding.
func (s *Store) CheckAvailabilityWatches(ctx context.Context, instance int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT w.id,w.user_id,w.was_available,w.last_notified_at,
		EXISTS(SELECT 1 FROM numbers n JOIN service_countries sc ON sc.service=n.service AND sc.country=n.country AND sc.enabled
		WHERE n.service=w.service AND n.country=w.country AND n.state='available'
		AND NOT EXISTS(SELECT 1 FROM number_user_exclusions e WHERE e.number_id=n.id AND e.user_id=w.user_id AND e.expires_at>now()))
		FROM availability_watches w JOIN users u ON u.id=w.user_id WHERE w.bot_instance_id=$1 AND w.enabled AND NOT u.banned FOR UPDATE OF w`, instanceID(instance))
	if err != nil {
		return err
	}
	type watch struct {
		id, user    int64
		before, now bool
		last        *time.Time
	}
	var all []watch
	for rows.Next() {
		var w watch
		if err = rows.Scan(&w.id, &w.user, &w.before, &w.last, &w.now); err != nil {
			rows.Close()
			return err
		}
		all = append(all, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, w := range all {
		eligible := w.now && !w.before && (w.last == nil || time.Since(*w.last) >= 30*time.Minute)
		if eligible {
			if _, err = tx.Exec(ctx, `INSERT INTO ui_notification_jobs(bot_instance_id,user_id,watch_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, instanceID(instance), w.user, w.id); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE availability_watches SET was_available=$2,last_notified_at=CASE WHEN $3 THEN now() ELSE last_notified_at END WHERE id=$1`, w.id, w.now, eligible); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type AvailabilityNotification struct {
	ID, UserID, WatchID int64
	Service, Country    string
	Attempts            int
}

func (s *Store) ClaimAvailabilityNotification(ctx context.Context, instance int64) (AvailabilityNotification, error) {
	_, err := s.pool.Exec(ctx, `UPDATE ui_notification_jobs SET state='uncertain' WHERE bot_instance_id=$1 AND state='sending' AND claimed_at<now()-interval '2 minutes'`, instanceID(instance))
	if err != nil {
		return AvailabilityNotification{}, err
	}
	var v AvailabilityNotification
	err = s.pool.QueryRow(ctx, `WITH candidate AS(SELECT j.id FROM ui_notification_jobs j WHERE j.bot_instance_id=$1 AND j.state='pending' AND j.next_attempt_at<=now() ORDER BY j.id FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE ui_notification_jobs j SET state='sending',attempts=j.attempts+1,claimed_at=now() FROM candidate c,availability_watches w WHERE j.id=c.id AND w.id=j.watch_id RETURNING j.id,j.user_id,w.id,w.service,w.country,j.attempts`, instanceID(instance)).Scan(&v.ID, &v.UserID, &v.WatchID, &v.Service, &v.Country, &v.Attempts)
	return v, err
}
func (s *Store) FinishAvailabilityNotification(ctx context.Context, id int64, state string, delay time.Duration) error {
	_, err := s.pool.Exec(ctx, `UPDATE ui_notification_jobs SET state=$2,next_attempt_at=now()+$3::interval WHERE id=$1`, id, state, postgresInterval(delay))
	return err
}
func (s *Store) AvailabilityNotificationEligible(ctx context.Context, instance int64, v AvailabilityNotification) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM availability_watches w JOIN users u ON u.id=w.user_id WHERE w.id=$1 AND w.bot_instance_id=$2 AND w.user_id=$3 AND w.enabled AND NOT u.banned
		AND EXISTS(SELECT 1 FROM numbers n JOIN service_countries sc ON sc.service=n.service AND sc.country=n.country AND sc.enabled WHERE n.service=w.service AND n.country=w.country AND n.state='available' AND NOT EXISTS(SELECT 1 FROM number_user_exclusions e WHERE e.number_id=n.id AND e.user_id=w.user_id AND e.expires_at>now())))`, v.WatchID, instanceID(instance), v.UserID).Scan(&ok)
	return ok, err
}

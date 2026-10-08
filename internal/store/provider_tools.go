package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/jackc/pgx/v5"
)

type OwnedNumber struct {
	ID                                           int64
	AssignmentID, Phone, Service, Country, State string
	ExpiresAt                                    time.Time
}

func (s *Store) OwnedNumbers(ctx context.Context, instance, user int64) ([]OwnedNumber, error) {
	rows, e := s.pool.Query(ctx, `SELECT n.id,a.id,n.normalized_phone,a.service,a.country,
 CASE WHEN an.consumed_at IS NOT NULL THEN 'received' WHEN an.released_at IS NOT NULL THEN 'released' WHEN a.expires_at<=now() THEN 'expired' ELSE 'pending' END,a.expires_at
 FROM assignments a JOIN assignment_numbers an ON an.assignment_id=a.id JOIN numbers n ON n.id=an.number_id
 WHERE a.bot_instance_id=$1 AND a.user_id=$2 AND a.assigned_at>now()-interval '24 hours' ORDER BY a.assigned_at DESC,n.id LIMIT 100`, instanceID(instance), user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []OwnedNumber{}
	for rows.Next() {
		var v OwnedNumber
		if e = rows.Scan(&v.ID, &v.AssignmentID, &v.Phone, &v.Service, &v.Country, &v.State, &v.ExpiresAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) ReleaseNumber(ctx context.Context, instance, user int64, assignment string, number int64, cooldown time.Duration) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var consumed, released bool
	e = tx.QueryRow(ctx, `SELECT an.consumed_at IS NOT NULL,an.released_at IS NOT NULL FROM assignments a JOIN assignment_numbers an ON an.assignment_id=a.id JOIN numbers n ON n.id=an.number_id WHERE a.bot_instance_id=$1 AND a.user_id=$2 AND a.id=$3 AND n.id=$4 FOR UPDATE OF n,an,a`, instanceID(instance), user, assignment, number).Scan(&consumed, &released)
	if e != nil {
		return errors.New("this number does not belong to your assignment")
	}
	if consumed {
		return errors.New("an OTP has already consumed this number")
	}
	if released {
		return nil
	}
	if _, e = tx.Exec(ctx, `UPDATE assignment_numbers SET released_at=now() WHERE assignment_id=$1 AND number_id=$2`, assignment, number); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE numbers SET state='available',updated_at=now() WHERE id=$1 AND state='assigned'`, number); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO number_user_exclusions(number_id,user_id,expires_at) VALUES($1,$2,now()+$3::interval) ON CONFLICT(number_id,user_id) DO UPDATE SET expires_at=GREATEST(number_user_exclusions.expires_at,EXCLUDED.expires_at)`, number, user, postgresInterval(cooldown)); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE assignments a SET state=CASE WHEN EXISTS(SELECT 1 FROM assignment_numbers WHERE assignment_id=a.id AND consumed_at IS NOT NULL) THEN 'completed'::assignment_state ELSE 'cancelled'::assignment_state END,closed_at=now() WHERE a.id=$1 AND NOT EXISTS(SELECT 1 FROM assignment_numbers WHERE assignment_id=a.id AND consumed_at IS NULL AND released_at IS NULL)`, assignment); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type NotificationPreferences struct {
	Enabled    bool
	Start, End *int
	Zone       string
}

func (s *Store) NotificationPreferences(ctx context.Context, instance, user int64) (NotificationPreferences, error) {
	v := NotificationPreferences{Enabled: true, Zone: s.location.String()}
	e := s.pool.QueryRow(ctx, `SELECT availability_notifications,quiet_start,quiet_end,timezone FROM user_preferences WHERE bot_instance_id=$1 AND user_id=$2`, instanceID(instance), user).Scan(&v.Enabled, &v.Start, &v.End, &v.Zone)
	if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	return v, e
}
func (s *Store) SetNotificationPreferences(ctx context.Context, instance, user int64, enabled bool, start, end *int) error {
	if (start == nil) != (end == nil) || start != nil && (*start < 0 || *start > 23 || *end < 0 || *end > 23 || *start == *end) {
		return errors.New("choose different quiet hours from 0 to 23")
	}
	_, e := s.pool.Exec(ctx, `INSERT INTO user_preferences(bot_instance_id,user_id,availability_notifications,quiet_start,quiet_end) VALUES($1,$2,$3,$4,$5) ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET availability_notifications=EXCLUDED.availability_notifications,quiet_start=EXCLUDED.quiet_start,quiet_end=EXCLUDED.quiet_end`, instanceID(instance), user, enabled, start, end)
	return e
}
func (v NotificationPreferences) Allowed(now time.Time) bool {
	if !v.Enabled {
		return false
	}
	if v.Start == nil {
		return true
	}
	loc, e := time.LoadLocation(v.Zone)
	if e != nil {
		loc = time.UTC
	}
	h := now.In(loc).Hour()
	if *v.Start < *v.End {
		return h < *v.Start || h >= *v.End
	}
	return h < *v.Start && h >= *v.End
}

type ServiceMapping struct {
	ID                            int64
	Kind, Value, Service, Country string
}

func (s *Store) ServiceMappings(ctx context.Context, instance, panel int64) ([]ServiceMapping, error) {
	rows, e := s.pool.Query(ctx, `SELECT m.id,m.match_kind,m.match_value,m.service,m.country FROM panel_service_mappings m JOIN panels p ON p.id=m.panel_id WHERE p.bot_instance_id=$1 AND p.id=$2 ORDER BY m.id`, instanceID(instance), panel)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ServiceMapping{}
	for rows.Next() {
		var m ServiceMapping
		if e = rows.Scan(&m.ID, &m.Kind, &m.Value, &m.Service, &m.Country); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) SaveServiceMapping(ctx context.Context, instance, panel int64, m ServiceMapping) error {
	if m.Kind != "service" && m.Kind != "sender" && m.Kind != "range" && m.Kind != "message" {
		return errors.New("mapping type must be service, sender, range or message")
	}
	m.Value = strings.TrimSpace(m.Value)
	if m.Value == "" || len(m.Value) > 256 {
		return errors.New("mapping value must be 1–256 characters")
	}
	if m.Kind == "message" {
		if _, e := regexp.Compile(m.Value); e != nil {
			return errors.New("invalid message pattern")
		}
	}
	tag, e := s.pool.Exec(ctx, `INSERT INTO panel_service_mappings(panel_id,match_kind,match_value,service,country) SELECT p.id,$3,$4,sc.service,$6 FROM panels p JOIN (SELECT DISTINCT service FROM service_countries WHERE lower(btrim(service))=lower(btrim($5))) sc ON true WHERE p.id=$2 AND p.bot_instance_id=$1 ON CONFLICT(panel_id,match_kind,match_value) DO UPDATE SET service=EXCLUDED.service,country=EXCLUDED.country`, instanceID(instance), panel, m.Kind, m.Value, m.Service, m.Country)
	if e == nil && tag.RowsAffected() == 0 {
		return errors.New("choose an existing panel and configured app")
	}
	return e
}
func (s *Store) DeleteServiceMapping(ctx context.Context, instance, panel, id int64) error {
	_, e := s.pool.Exec(ctx, `DELETE FROM panel_service_mappings m USING panels p WHERE m.panel_id=p.id AND p.bot_instance_id=$1 AND p.id=$2 AND m.id=$3`, instanceID(instance), panel, id)
	return e
}
func (s *Store) ResolveEventService(ctx context.Context, event *domain.OTPEvent) (bool, error) {
	candidates := map[string]string{}
	rows, e := s.pool.Query(ctx, `SELECT DISTINCT service FROM service_countries WHERE enabled AND lower(btrim(service))=lower(btrim($1))`, event.Service)
	if e != nil {
		return false, e
	}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return false, e
		}
		candidates[strings.ToLower(strings.TrimSpace(v))] = v
	}
	rows.Close()
	mappings, e := s.ServiceMappings(ctx, event.BotInstanceID, event.PanelID)
	if e != nil {
		return false, e
	}
	country := ""
	for _, m := range mappings {
		match := false
		switch m.Kind {
		case "service":
			match = strings.EqualFold(strings.TrimSpace(event.Service), m.Value)
		case "sender":
			match = strings.EqualFold(strings.TrimSpace(event.Sender), m.Value)
		case "range":
			match = strings.EqualFold(event.ProviderRange, m.Value)
		case "message":
			r, e := regexp.Compile(m.Value)
			match = e == nil && r.MatchString(event.Message)
		}
		if match {
			candidates[strings.ToLower(strings.TrimSpace(m.Service))] = m.Service
			if m.Country != "" {
				country = m.Country
			}
		}
	}
	if len(candidates) == 0 && event.Sender != "" {
		var v string
		e = s.pool.QueryRow(ctx, `SELECT service FROM service_countries WHERE enabled AND lower(btrim(service))=lower(btrim($1)) ORDER BY service LIMIT 1`, event.Sender).Scan(&v)
		if e == nil {
			candidates[strings.ToLower(strings.TrimSpace(v))] = v
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return false, e
		}
	}
	if len(candidates) != 1 {
		return false, nil
	}
	for _, v := range candidates {
		event.Service = v
	}
	if country != "" {
		event.Country = country
	}
	if event.Country == "" {
		_ = s.pool.QueryRow(ctx, `SELECT country FROM numbers WHERE normalized_phone=$1 AND service_key=lower(btrim($2))`, event.NormalizedPhone, event.Service).Scan(&event.Country)
	}
	return true, nil
}
func (s *Store) HoldUnmappedEvent(ctx context.Context, event domain.OTPEvent) error {
	raw, e := json.Marshal(event)
	if e != nil {
		return e
	}
	_, e = s.pool.Exec(ctx, `INSERT INTO panel_unmapped_sms(panel_id,dedup_key,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, event.PanelID, event.DedupKey, raw)
	return e
}
func (s *Store) RetryUnmapped(ctx context.Context, instance, panel int64) (int, error) {
	rows, e := s.pool.Query(ctx, `SELECT u.id,u.payload FROM panel_unmapped_sms u JOIN panels p ON p.id=u.panel_id WHERE p.bot_instance_id=$1 AND p.id=$2 ORDER BY u.id LIMIT 200`, instanceID(instance), panel)
	if e != nil {
		return 0, e
	}
	type item struct {
		id    int64
		event domain.OTPEvent
	}
	items := []item{}
	for rows.Next() {
		var v item
		var raw []byte
		if e = rows.Scan(&v.id, &raw); e != nil {
			rows.Close()
			return 0, e
		}
		if e = json.Unmarshal(raw, &v.event); e != nil {
			rows.Close()
			return 0, e
		}
		items = append(items, v)
	}
	rows.Close()
	n := 0
	for _, v := range items {
		ok, e := s.ResolveEventService(ctx, &v.event)
		if e != nil {
			return n, e
		}
		if !ok {
			continue
		}
		raw, e := json.Marshal(v.event)
		if e != nil {
			return n, e
		}
		tx, e := s.pool.Begin(ctx)
		if e != nil {
			return n, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO panel_ingest_jobs(panel_id,bot_instance_id,dedup_key,payload) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, panel, instanceID(instance), v.event.DedupKey, raw)
		if e == nil {
			_, e = tx.Exec(ctx, `DELETE FROM panel_unmapped_sms WHERE id=$1`, v.id)
		}
		if e != nil {
			tx.Rollback(ctx)
			return n, e
		}
		if e = tx.Commit(ctx); e != nil {
			return n, e
		}
		n++
	}
	return n, nil
}

func (s *Store) UnmappedEvents(ctx context.Context, instance, panel int64) ([]domain.OTPEvent, error) {
	rows, e := s.pool.Query(ctx, `SELECT u.payload FROM panel_unmapped_sms u JOIN panels p ON p.id=u.panel_id WHERE p.bot_instance_id=$1 AND p.id=$2 ORDER BY u.id DESC LIMIT 10`, instanceID(instance), panel)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.OTPEvent{}
	for rows.Next() {
		var raw []byte
		var v domain.OTPEvent
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// All processes and child bots share a rolling budget for the same credential.
func (s *Store) TakeProviderRequest(ctx context.Context, key string, limit int) (time.Time, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return time.Time{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `INSERT INTO provider_request_budgets(key_hash,request_limit) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, limit); e != nil {
		return time.Time{}, e
	}
	var requests []time.Time
	var paused *time.Time
	var observed int
	if e = tx.QueryRow(ctx, `SELECT requests,paused_until,request_limit FROM provider_request_budgets WHERE key_hash=$1 FOR UPDATE`, key).Scan(&requests, &paused, &observed); e != nil {
		return time.Time{}, e
	}
	if observed < limit {
		limit = observed
	}
	now := time.Now()
	window := time.Minute
	if key == "augestel:source-ip" || strings.HasPrefix(key, "provider-ip:") {
		window = 20 * time.Minute
	}
	kept := []time.Time{}
	for _, t := range requests {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	next := now
	if paused != nil && paused.After(next) {
		next = *paused
	}
	if len(kept) >= limit {
		candidate := kept[len(kept)-limit].Add(window + time.Millisecond)
		if candidate.After(next) {
			next = candidate
		}
	}
	if next.After(now) {
		return next, tx.Commit(ctx)
	}
	kept = append(kept, now)
	if _, e = tx.Exec(ctx, `UPDATE provider_request_budgets SET requests=$2,updated_at=now() WHERE key_hash=$1`, key, kept); e != nil {
		return time.Time{}, e
	}
	return time.Time{}, tx.Commit(ctx)
}
func (s *Store) UpdateProviderBudget(ctx context.Context, key string, limit int, until time.Time) error {
	_, e := s.pool.Exec(ctx, `UPDATE provider_request_budgets SET request_limit=CASE WHEN $2>0 THEN LEAST(request_limit,$2) ELSE request_limit END,paused_until=CASE WHEN $3::timestamptz IS NULL THEN paused_until ELSE GREATEST(COALESCE(paused_until,$3),$3) END,updated_at=now() WHERE key_hash=$1`, key, limit, nullableTime(until))
	return e
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func (s *Store) PanelDiagnostics(ctx context.Context, instance, panel int64) (string, error) {
	var last, next *time.Time
	var backlog, pending int
	var status string
	e := s.pool.QueryRow(ctx, `SELECT last_sms_at,next_retry_at,backlog_pages,connection_status,(SELECT count(*) FROM panel_unmapped_sms WHERE panel_id=p.id) FROM panels p WHERE bot_instance_id=$1 AND id=$2`, instanceID(instance), panel).Scan(&last, &next, &backlog, &status, &pending)
	if e != nil {
		return "", e
	}
	lastText, nextText := "None yet", "Ready"
	if last != nil {
		lastText = last.In(s.location).Format("02 Jan 15:04:05")
	}
	if next != nil && next.After(time.Now()) {
		nextText = next.In(s.location).Format("15:04:05")
	}
	return fmt.Sprintf("Connection: %s\nLast SMS: %s\nBackfill pages: %d\nNext retry: %s\nUnmapped messages: %d", status, lastText, backlog, nextText, pending), nil
}
func (s *Store) UpdateProviderState(ctx context.Context, panel int64, status string, retry time.Time) error {
	_, e := s.pool.Exec(ctx, `UPDATE panels SET connection_status=$2,next_retry_at=$3 WHERE id=$1`, panel, status, nullableTime(retry))
	return e
}

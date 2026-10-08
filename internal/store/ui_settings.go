package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/adnan-dogar/cracksms-vnext/internal/themes"
	"time"
)

func (s *Store) SetMenuMode(ctx context.Context, instance, user int64, compact bool) error {
	_, e := s.pool.Exec(ctx, `INSERT INTO user_preferences(bot_instance_id,user_id,compact_menu) VALUES($1,$2,$3) ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET compact_menu=EXCLUDED.compact_menu,updated_at=now()`, instanceID(instance), user, compact)
	return e
}
func (s *Store) SetTimezone(ctx context.Context, instance, user int64, zone string) error {
	if _, e := time.LoadLocation(zone); e != nil {
		return errors.New("unknown timezone; use an IANA name such as Asia/Karachi")
	}
	_, e := s.pool.Exec(ctx, `INSERT INTO user_preferences(bot_instance_id,user_id,timezone) VALUES($1,$2,$3) ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET timezone=EXCLUDED.timezone,updated_at=now()`, instanceID(instance), user, zone)
	return e
}
func (s *Store) InstanceSettings(ctx context.Context, instance int64) (map[string]any, error) {
	var raw []byte
	e := s.pool.QueryRow(ctx, `SELECT settings FROM bot_instances WHERE id=$1`, instanceID(instance)).Scan(&raw)
	if e != nil {
		return nil, e
	}
	var values map[string]any
	e = json.Unmarshal(raw, &values)
	if values == nil {
		values = map[string]any{}
	}
	return values, e
}
func (s *Store) SetInstanceSetting(ctx context.Context, instance int64, key string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	_, e = s.pool.Exec(ctx, `UPDATE bot_instances SET settings=jsonb_set(settings,ARRAY[$2]::text[],$3::jsonb,true) WHERE id=$1`, instanceID(instance), key, raw)
	return e
}
func (s *Store) EffectiveLinks(ctx context.Context, instance int64, defaults themes.Links) (themes.Links, error) {
	values, e := s.InstanceSettings(ctx, instance)
	if e != nil {
		return defaults, e
	}
	for key, target := range map[string]*string{"group_url": &defaults.Group, "channel_url": &defaults.Channel, "number_bot_url": &defaults.NumberBot, "developer_url": &defaults.Developer, "support_url": &defaults.Support} {
		if value, ok := values[key].(string); ok {
			*target = value
		}
	}
	return defaults, nil
}
func (s *Store) AssignmentLimit(ctx context.Context, instance int64) (int, error) {
	values, e := s.InstanceSettings(ctx, instance)
	if e != nil {
		return 0, e
	}
	if value, ok := values["assignment_limit"].(float64); ok && value >= 1 && value <= 1000 {
		return int(value), nil
	}
	return 0, nil
}

type PeriodStats struct {
	Events, Counted, Users                   int64
	BasePKR, BaseUSD, RewardsPKR, RewardsUSD float64
}

func (s *Store) PeriodStatistics(ctx context.Context, instance, user int64, since *time.Time) (PeriodStats, error) {
	var out PeriodStats
	e := s.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE counted),count(DISTINCT assigned_user_id) FROM otp_events WHERE bot_instance_id=$1 AND ($2::bigint=0 OR assigned_user_id=$2) AND ($3::timestamptz IS NULL OR received_at >= $3)`, instanceID(instance), user, since).Scan(&out.Events, &out.Counted, &out.Users)
	if e != nil {
		return out, e
	}
	e = s.pool.QueryRow(ctx, `SELECT COALESCE(sum(l.amount) FILTER(WHERE l.currency='PKR'),0),COALESCE(sum(l.amount) FILTER(WHERE l.currency='USD'),0) FROM balance_ledger l JOIN otp_events e ON e.id::text=l.reference_id WHERE l.reference_type='otp_event' AND e.bot_instance_id=$1 AND ($2::bigint=0 OR e.assigned_user_id=$2) AND ($3::timestamptz IS NULL OR e.received_at >= $3)`, instanceID(instance), user, since).Scan(&out.BasePKR, &out.BaseUSD)
	if e != nil {
		return out, e
	}
	// Rewards are global per user/day in the existing financial model. Label them
	// as account-wide instead of pretending to attribute them to a child bot.
	if user != 0 {
		e = s.pool.QueryRow(ctx, `SELECT COALESCE(sum(amount_pkr),0),COALESCE(sum(amount_usd),0) FROM reward_awards WHERE user_id=$1 AND ($2::timestamptz IS NULL OR local_date>=($2 AT TIME ZONE $3)::date)`, user, since, s.location.String()).Scan(&out.RewardsPKR, &out.RewardsUSD)
	}
	return out, e
}

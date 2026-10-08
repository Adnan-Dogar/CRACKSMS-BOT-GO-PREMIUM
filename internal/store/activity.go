package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/country"
	"github.com/adnan-dogar/cracksms-vnext/internal/premium"
)

type ActivityApp struct {
	Key, Name, EmojiID string
	Count              int64
}
type ActivityCountry struct {
	Key, Name, Code string
	Count           int64
	Apps            map[string]int64
}
type ActivitySnapshot struct {
	Total     int64
	Countries []ActivityCountry
	Apps      []ActivityApp
	At        time.Time
}

func ActivityPeriod(period string) time.Duration {
	switch period {
	case "1h":
		return time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// The public snapshot contains counts only. It never selects a code, message,
// phone, panel, or user identifier, including from another bot instance.
func (s *Store) Activity(ctx context.Context, instance int64, period string) (ActivitySnapshot, error) {
	out := ActivitySnapshot{At: time.Now()}
	catalog, err := s.CatalogForInstance(ctx, instance)
	if err != nil {
		return out, err
	}
	apps := map[string]*ActivityApp{}
	countries := map[string]*ActivityCountry{}
	aliases := map[string]string{}
	labels := map[string]string{}
	for _, app := range premium.DefaultApps() {
		labels[premium.AppKey(app.Name)] = app.Name
	}
	addApp := func(name, emoji string) *ActivityApp {
		key := premium.AppKey(name)
		if key == "" {
			key = "unknown"
			name = "Unknown"
		}
		if v := apps[key]; v != nil {
			if emoji != "" {
				v.EmojiID = emoji
			}
			return v
		}
		if label := labels[key]; label != "" {
			name = label
		}
		if emoji == "" {
			emoji = premium.AppEmojiID(name)
		}
		v := &ActivityApp{Key: key, Name: name, EmojiID: emoji}
		apps[key] = v
		return v
	}
	addCountry := func(name, code string) *ActivityCountry {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if key == "" {
			key, name = "unknown", "Unknown"
		}
		if canonical := aliases[key]; canonical != "" {
			key = canonical
		}
		if v := countries[key]; v != nil {
			return v
		}
		info := country.Resolve(name)
		if name == "Unknown" && code != "" {
			info = country.Resolve(code)
		}
		if code == "" {
			code = info.Code
		}
		if info.Name != "" {
			name = info.Name
			key = strings.ToLower(name)
		}
		if v := countries[key]; v != nil {
			return v
		}
		v := &ActivityCountry{Key: key, Name: name, Code: code, Apps: map[string]int64{}}
		countries[key] = v
		aliases[strings.ToLower(name)] = key
		if code != "" {
			aliases[strings.ToLower(code)] = key
		}
		return v
	}
	for _, service := range SortedServices(catalog) {
		for _, row := range catalog[service] {
			addApp(service, row.CustomEmojiID)
			addCountry(row.Country, row.CountryCode)
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT service,country,count(*) FILTER(WHERE COALESCE(provider_timestamp,received_at) >= $2) FROM otp_events
		WHERE bot_instance_id=$1 AND code<>'' GROUP BY service,country ORDER BY country,service`, instanceID(instance), out.At.Add(-ActivityPeriod(period)))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var service, name string
		var count int64
		if err = rows.Scan(&service, &name, &count); err != nil {
			return out, err
		}
		app := addApp(service, "")
		c := addCountry(name, "")
		app.Count += count
		c.Count += count
		c.Apps[app.Key] += count
		out.Total += count
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	profiles, err := s.pool.Query(ctx, `SELECT service_key,custom_emoji_id FROM service_profiles WHERE bot_instance_id=$1`, instanceID(instance))
	if err != nil {
		return out, err
	}
	for profiles.Next() {
		var name, id string
		if err = profiles.Scan(&name, &id); err != nil {
			profiles.Close()
			return out, err
		}
		if app := apps[premium.AppKey(name)]; app != nil && id != "" {
			app.EmojiID = id
		}
	}
	err = profiles.Err()
	profiles.Close()
	if err != nil {
		return out, err
	}
	for _, app := range apps {
		out.Apps = append(out.Apps, *app)
	}
	for _, c := range countries {
		out.Countries = append(out.Countries, *c)
	}
	sort.Slice(out.Apps, func(i, j int) bool { return strings.ToLower(out.Apps[i].Name) < strings.ToLower(out.Apps[j].Name) })
	sort.Slice(out.Countries, func(i, j int) bool {
		return strings.ToLower(out.Countries[i].Name) < strings.ToLower(out.Countries[j].Name)
	})
	return out, nil
}

type LiveAssignment struct {
	Phone, Service, Country, State string
	ExpiresAt                      time.Time
}

func (s *Store) LiveAssignments(ctx context.Context, instance, user int64) ([]LiveAssignment, error) {
	rows, err := s.pool.Query(ctx, `SELECT n.normalized_phone,a.service,a.country,
		CASE WHEN an.consumed_at IS NOT NULL THEN 'received' WHEN a.expires_at<=now() OR an.released_at IS NOT NULL THEN 'expired' ELSE 'pending' END,a.expires_at
		FROM assignments a JOIN assignment_numbers an ON an.assignment_id=a.id JOIN numbers n ON n.id=an.number_id
		WHERE a.bot_instance_id=$1 AND a.user_id=$2 AND a.assigned_at>now()-interval '24 hours'
		ORDER BY a.assigned_at DESC,n.id LIMIT 8`, instanceID(instance), user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveAssignment
	for rows.Next() {
		var v LiveAssignment
		if err = rows.Scan(&v.Phone, &v.Service, &v.Country, &v.State, &v.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

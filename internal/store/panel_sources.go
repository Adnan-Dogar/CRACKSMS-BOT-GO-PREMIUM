package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/url"
	"strings"
)

type PanelSource struct {
	ID, BotInstanceID int64
	OriginSourceID    *int64
	Shared            bool
	Name, Kind, URL   string
	Accounts, Online  int
}

func ValidPanelSource(kind, link string) bool {
	if kind != "login" && kind != "token_api" && kind != "legacy_api" && kind != "websocket" && kind != "ivas" && kind != "socketio" && kind != "axon_asp" && kind != "augestel" {
		return false
	}
	u, e := url.ParseRequestURI(link)
	return e == nil && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || ((kind == "login" || kind == "token_api" || kind == "legacy_api") && u.Scheme == "http") || ((kind == "websocket" || kind == "socketio" || kind == "ivas") && u.Scheme == "wss"))
}
func (s *Store) SavePanelSource(ctx context.Context, instance, id int64, name, kind, link string) (int64, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 80 || !ValidPanelSource(kind, link) {
		return 0, errors.New("enter a name and a credential-free link: HTTP/HTTPS for login and legacy APIs, HTTPS for other APIs, WSS for streams")
	}
	if id != 0 {
		tag, e := s.pool.Exec(ctx, `UPDATE panel_sources SET name=$3,kind=$4,url=$5 WHERE bot_instance_id=$1 AND id=$2`, instanceID(instance), id, name, kind, link)
		if e == nil && tag.RowsAffected() == 0 {
			e = pgx.ErrNoRows
		}
		return id, e
	}
	var saved int64
	e := s.pool.QueryRow(ctx, `INSERT INTO panel_sources(bot_instance_id,name,kind,url) VALUES($1,$2,$3,$4) RETURNING id`, instanceID(instance), name, kind, link).Scan(&saved)
	return saved, e
}
func (s *Store) PanelSources(ctx context.Context, instance int64) ([]PanelSource, error) {
	rows, e := s.pool.Query(ctx, `SELECT s.id,s.bot_instance_id,s.name,s.kind,s.url,count(p.id),count(p.id) FILTER(WHERE p.enabled AND p.healthy),s.origin_source_id,(s.bot_instance_id<>$1) FROM panel_sources s LEFT JOIN panels p ON p.source_id=s.id AND p.bot_instance_id=$1 WHERE s.bot_instance_id=$1 OR ($1<>1 AND s.bot_instance_id=1 AND EXISTS(SELECT 1 FROM bot_instances b WHERE b.id=$1 AND b.parent_id=1) AND NOT EXISTS(SELECT 1 FROM panel_sources own WHERE own.bot_instance_id=$1 AND own.origin_source_id=s.id)) GROUP BY s.id ORDER BY s.bot_instance_id,s.id`, instanceID(instance))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []PanelSource{}
	for rows.Next() {
		var p PanelSource
		if e = rows.Scan(&p.ID, &p.BotInstanceID, &p.Name, &p.Kind, &p.URL, &p.Accounts, &p.Online, &p.OriginSourceID, &p.Shared); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) PanelSource(ctx context.Context, instance, id int64) (PanelSource, error) {
	items, e := s.PanelSources(ctx, instance)
	if e != nil {
		return PanelSource{}, e
	}
	for _, p := range items {
		if p.ID == id {
			return p, nil
		}
	}
	return PanelSource{}, pgx.ErrNoRows
}
func (s *Store) SourceAccounts(ctx context.Context, instance, id int64) ([]PanelHealth, error) {
	rows, e := s.pool.Query(ctx, `SELECT id,account_label,kind,enabled,healthy,consecutive_failures,last_success_at,last_error,otp_count FROM panels WHERE bot_instance_id=$1 AND source_id=$2 ORDER BY id`, instanceID(instance), id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []PanelHealth{}
	for rows.Next() {
		var p PanelHealth
		if e = rows.Scan(&p.ID, &p.Name, &p.Kind, &p.Enabled, &p.Healthy, &p.Failures, &p.LastSuccessAt, &p.LastError, &p.OTPCount); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) AccountSourceID(ctx context.Context, instance, id int64) (int64, error) {
	var source int64
	e := s.pool.QueryRow(ctx, `SELECT source_id FROM panels WHERE bot_instance_id=$1 AND id=$2`, instanceID(instance), id).Scan(&source)
	return source, e
}
func (s *Store) SavePanelAccount(ctx context.Context, instance, sourceID, accountID int64, label string, config map[string]any, ready bool) (int64, error) {
	source, e := s.PanelSource(ctx, instance, sourceID)
	if e != nil {
		return 0, e
	}
	if source.Shared {
		if accountID != 0 {
			return 0, errors.New("edit this child bot's own account")
		}
		sourceID, e = s.UsePanelTemplate(ctx, instance, sourceID)
		if e != nil {
			return 0, e
		}
		source, e = s.PanelSource(ctx, instance, sourceID)
		if e != nil {
			return 0, e
		}
	}
	if len(strings.TrimSpace(label)) < 1 || len(label) > 80 {
		return 0, errors.New("account label must be 1–80 characters")
	}
	raw, e := json.Marshal(config)
	if e != nil {
		return 0, e
	}
	encrypted, e := s.cipher.Encrypt(raw)
	if e != nil {
		return 0, e
	}
	envelope, _ := json.Marshal(map[string]string{"encrypted": encrypted})
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	// Serialize quota enforcement across concurrent administrators.
	var tier string
	if e = tx.QueryRow(ctx, `SELECT tier FROM bot_instances WHERE id=$1 FOR UPDATE`, instanceID(instance)).Scan(&tier); e != nil {
		return 0, e
	}
	if accountID == 0 {
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM panels WHERE bot_instance_id=$1`, instanceID(instance)).Scan(&count); e != nil {
			return 0, e
		}
		if count >= TierPanelLimit(tier) {
			return 0, errors.New("account connection limit reached")
		}
		e = tx.QueryRow(ctx, `INSERT INTO panels(bot_instance_id,source_id,name,account_label,kind,config,enabled,healthy) VALUES($1,$2,$3,$4,$5,$6,$7,false) RETURNING id`, instanceID(instance), sourceID, fmt.Sprintf("source-%d-%s", sourceID, label), label, source.Kind, envelope, ready).Scan(&accountID)
	} else {
		result, err := tx.Exec(ctx, `UPDATE panels SET config=$4,account_label=$5,enabled=$6,kind=$7,last_cursor=CASE WHEN kind=$7 THEN last_cursor ELSE '' END,healthy=false,consecutive_failures=0,last_error='',updated_at=now() WHERE bot_instance_id=$1 AND source_id=$2 AND id=$3`, instanceID(instance), sourceID, accountID, envelope, label, ready, source.Kind)
		e = err
		if e == nil && result.RowsAffected() == 0 {
			e = pgx.ErrNoRows
		}
	}
	if e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM provider_response_cache WHERE panel_id=$1`, accountID); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM panel_connection_tests WHERE panel_id=$1`, accountID); e != nil {
		return 0, e
	}
	return accountID, tx.Commit(ctx)
}
func (s *Store) DeletePanelSource(ctx context.Context, instance, id int64) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `DELETE FROM panels WHERE bot_instance_id=$1 AND source_id=$2`, instanceID(instance), id); e != nil {
		return e
	}
	result, e := tx.Exec(ctx, `DELETE FROM panel_sources WHERE bot_instance_id=$1 AND id=$2`, instanceID(instance), id)
	if e != nil {
		return e
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}

// HydratePanelSourceLinks fills metadata for accounts migrated from the old
// single-panel model, without changing account IDs, credentials or cursors.
func (s *Store) HydratePanelSourceLinks(ctx context.Context) error {
	rows, e := s.pool.Query(ctx, `SELECT p.bot_instance_id,p.id,p.source_id FROM panels p JOIN panel_sources s ON s.id=p.source_id WHERE s.url='' ORDER BY p.id`)
	if e != nil {
		return e
	}
	type item struct{ instance, id, source int64 }
	var items []item
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.instance, &v.id, &v.source); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range items {
		p, e := s.PanelForInstance(ctx, v.instance, v.id)
		if e != nil {
			return e
		}
		link, _ := p.Config["base_url"].(string)
		if link == "" {
			link, _ = p.Config["url"].(string)
		}
		if u, e := url.Parse(link); e == nil && u.Hostname() != "" {
			u.User = nil
			u.RawQuery = ""
			u.Fragment = ""
			if _, e = s.pool.Exec(ctx, `UPDATE panel_sources SET url=$2 WHERE id=$1 AND url=''`, v.source, u.String()); e != nil {
				return e
			}
		}
	}
	return nil
}

package store

import (
	"context"
	"fmt"
	"strings"
)

func (s *Store) PreviewNumbers(ctx context.Context, service string, phones []string) (int, error) {
	normalized := make([]string, 0, len(phones))
	seen := map[string]bool{}
	for _, p := range phones {
		v := NormalizePhone(p)
		if len(v) >= 5 && len(v) <= 20 && !seen[v] {
			seen[v] = true
			normalized = append(normalized, v)
		}
	}
	var existing int
	e := s.pool.QueryRow(ctx, `SELECT count(*) FROM numbers WHERE normalized_phone=ANY($1) AND service_key=lower(btrim($2))`, normalized, strings.TrimSpace(service)).Scan(&existing)
	return existing, e
}
func (s *Store) CachedProviderResponse(ctx context.Context, instance, panel int64, key string) ([]byte, error) {
	var payload []byte
	e := s.pool.QueryRow(ctx, `SELECT c.payload FROM provider_response_cache c JOIN panels p ON p.id=c.panel_id WHERE p.bot_instance_id=$1 AND p.id=$2 AND c.cache_key=$3 AND c.updated_at>now()-interval '60 seconds'`, instanceID(instance), panel, key).Scan(&payload)
	return payload, e
}
func (s *Store) SaveProviderResponse(ctx context.Context, instance, panel int64, key string, payload []byte) error {
	tag, e := s.pool.Exec(ctx, `INSERT INTO provider_response_cache(panel_id,cache_key,payload) SELECT id,$3,$4 FROM panels WHERE bot_instance_id=$1 AND id=$2 ON CONFLICT(panel_id,cache_key) DO UPDATE SET payload=EXCLUDED.payload,updated_at=now()`, instanceID(instance), panel, key, payload)
	if e == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("panel not found")
	}
	return e
}

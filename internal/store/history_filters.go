package store

import (
	"context"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"time"
)

func (s *Store) FilteredHistory(ctx context.Context, instance, user int64, service string, since time.Time, limit, offset int) ([]domain.OTPHistoryItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	rows, e := s.pool.Query(ctx, `SELECT e.id,e.panel_name,e.normalized_phone,e.service,e.country,e.message,e.code,COALESCE(e.provider_timestamp,e.received_at),e.counted,COALESCE(sc.price_pkr,0)
 FROM otp_events e LEFT JOIN service_countries sc ON sc.service=e.service AND sc.country=e.country
 WHERE e.bot_instance_id=$1 AND e.assigned_user_id=$2 AND ($3='' OR lower(btrim(e.service))=lower(btrim($3))) AND COALESCE(e.provider_timestamp,e.received_at)>=$4 ORDER BY COALESCE(e.provider_timestamp,e.received_at) DESC,e.id LIMIT $5 OFFSET $6`, instanceID(instance), user, service, since, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.OTPHistoryItem{}
	for rows.Next() {
		var v domain.OTPHistoryItem
		if e = rows.Scan(&v.EventID, &v.PanelName, &v.Phone, &v.Service, &v.Country, &v.Message, &v.Code, &v.ReceivedAt, &v.Counted, &v.BasePKR); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

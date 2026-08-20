package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
)

func (s *Store) ListEnabledPanels(ctx context.Context) ([]domain.Panel, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,bot_instance_id,name,kind,config,
		extract(epoch from poll_interval),enabled,healthy,consecutive_failures,last_cursor
		FROM panels WHERE enabled ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var panels []domain.Panel
	for rows.Next() {
		var panel domain.Panel
		var raw []byte
		var seconds float64
		if err := rows.Scan(&panel.ID, &panel.BotInstanceID, &panel.Name, &panel.Kind, &raw, &seconds, &panel.Enabled,
			&panel.Healthy, &panel.ConsecutiveFailures, &panel.LastCursor); err != nil {
			return nil, err
		}
		var envelope struct {
			Encrypted string `json:"encrypted"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Encrypted == "" {
			return nil, fmt.Errorf("panel %d has invalid encrypted config", panel.ID)
		}
		plaintext, err := s.cipher.Decrypt(envelope.Encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt panel %d config: %w", panel.ID, err)
		}
		if err := json.Unmarshal(plaintext, &panel.Config); err != nil {
			return nil, err
		}
		panel.PollInterval = time.Duration(seconds * float64(time.Second))
		panels = append(panels, panel)
	}
	return panels, rows.Err()
}

func (s *Store) UpsertPanel(ctx context.Context, panel domain.Panel) (int64, error) {
	return s.UpsertPanelForInstance(ctx, instanceID(panel.BotInstanceID), panel)
}

func (s *Store) UpsertPanelForInstance(ctx context.Context, botInstanceID int64, panel domain.Panel) (int64, error) {
	botInstanceID = instanceID(botInstanceID)
	raw, err := json.Marshal(panel.Config)
	if err != nil {
		return 0, err
	}
	if panel.PollInterval <= 0 {
		panel.PollInterval = 2 * time.Second
	}
	encrypted, err := s.cipher.Encrypt(raw)
	if err != nil {
		return 0, err
	}
	envelope, err := json.Marshal(map[string]string{"encrypted": encrypted})
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.pool.QueryRow(ctx, `INSERT INTO panels(bot_instance_id,name,kind,config,poll_interval,enabled)
		VALUES($1,$2,$3,$4,$5::interval,$6)
		ON CONFLICT(bot_instance_id,name) DO UPDATE SET kind=EXCLUDED.kind,config=EXCLUDED.config,
		poll_interval=EXCLUDED.poll_interval,enabled=EXCLUDED.enabled,updated_at=now()
		RETURNING id`, botInstanceID, panel.Name, panel.Kind, envelope, postgresInterval(panel.PollInterval), panel.Enabled).Scan(&id)
	return id, err
}

func (s *Store) SetPanelEnabled(ctx context.Context, id int64, enabled bool) error {
	return s.SetPanelEnabledForInstance(ctx, MainBotInstanceID, id, enabled)
}

func (s *Store) SetPanelEnabledForInstance(ctx context.Context, botInstanceID, id int64, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE panels SET enabled=$3,updated_at=now() WHERE bot_instance_id=$1 AND id=$2`,
		instanceID(botInstanceID), id, enabled)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("panel %d not found", id)
	}
	return err
}

func (s *Store) UpdatePanelHealth(ctx context.Context, id int64, cursor string, panelErr error) error {
	if panelErr == nil {
		_, err := s.pool.Exec(ctx, `UPDATE panels SET healthy=true,consecutive_failures=0,last_cursor=$2,
			last_success_at=now(),last_error='',updated_at=now() WHERE id=$1`, id, cursor)
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE panels SET healthy=false,consecutive_failures=consecutive_failures+1,
		last_error_at=now(),last_error=$2,updated_at=now() WHERE id=$1`, id, truncate(panelErr.Error(), 500))
	return err
}

func (s *Store) PanelHealthReport(ctx context.Context) ([]PanelHealth, error) {
	return s.PanelHealthReportForInstance(ctx, MainBotInstanceID)
}

func (s *Store) PanelHealthReportForInstance(ctx context.Context, botInstanceID int64) ([]PanelHealth, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,kind,enabled,healthy,consecutive_failures,last_success_at,last_error,otp_count
		FROM panels WHERE bot_instance_id=$1 ORDER BY id`, instanceID(botInstanceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PanelHealth
	for rows.Next() {
		var item PanelHealth
		if err := rows.Scan(&item.ID, &item.Name, &item.Kind, &item.Enabled, &item.Healthy,
			&item.Failures, &item.LastSuccessAt, &item.LastError, &item.OTPCount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type PanelHealth struct {
	ID            int64
	Name          string
	Kind          string
	Enabled       bool
	Healthy       bool
	Failures      int
	LastSuccessAt *time.Time
	LastError     string
	OTPCount      int64
}

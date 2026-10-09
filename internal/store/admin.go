package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO settings(key,value) VALUES($1,$2)
		ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, key, raw)
	return err
}

func (s *Store) GetSetting(ctx context.Context, key string, target any) error {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func (s *Store) AddAdmin(ctx context.Context, userID int64, permissions []string) error {
	if len(permissions) == 0 {
		permissions = []string{"*"}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO users(id) VALUES($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO admins(user_id,permissions) VALUES($1,$2)
		ON CONFLICT(user_id) DO UPDATE SET permissions=EXCLUDED.permissions`, userID, permissions)
	return err
}

func (s *Store) RemoveAdmin(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM admins WHERE user_id=$1`, userID)
	return err
}

type InstanceAdmin struct {
	UserID      int64
	Username    string
	FirstName   string
	Permissions []string
}

func (s *Store) ListInstanceAdmins(ctx context.Context, botInstanceID int64) ([]InstanceAdmin, error) {
	botInstanceID = instanceID(botInstanceID)
	query := `SELECT a.user_id,u.username,u.first_name,a.permissions
		FROM admins a JOIN users u ON u.id=a.user_id ORDER BY a.user_id`
	args := []any{}
	if botInstanceID != MainBotInstanceID {
		query = `SELECT a.user_id,u.username,u.first_name,a.permissions
			FROM tenant_admins a JOIN users u ON u.id=a.user_id
			WHERE a.bot_instance_id=$1 ORDER BY a.user_id`
		args = append(args, botInstanceID)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InstanceAdmin
	for rows.Next() {
		var item InstanceAdmin
		if err := rows.Scan(&item.UserID, &item.Username, &item.FirstName, &item.Permissions); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type InstanceUser struct {
	UserID     int64
	Username   string
	FirstName  string
	Tier       string
	TotalOTPs  int64
	BalancePKR float64
}

func (s *Store) ListUsersForInstance(ctx context.Context, botInstanceID int64, limit int) ([]InstanceUser, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id,u.username,u.first_name,
		COALESCE(us.tier,'free'),count(e.id),u.balance_pkr
		FROM bot_instance_users iu JOIN users u ON u.id=iu.user_id
		LEFT JOIN user_subscriptions us ON us.bot_instance_id=iu.bot_instance_id AND us.user_id=u.id
			AND us.status='active' AND (us.expires_at IS NULL OR us.expires_at>now())
		LEFT JOIN otp_events e ON e.bot_instance_id=iu.bot_instance_id AND e.assigned_user_id=u.id AND e.counted
		WHERE iu.bot_instance_id=$1 GROUP BY u.id,u.username,u.first_name,us.tier,u.balance_pkr
		ORDER BY count(e.id) DESC,u.id LIMIT $2`, instanceID(botInstanceID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InstanceUser
	for rows.Next() {
		var item InstanceUser
		if err := rows.Scan(&item.UserID, &item.Username, &item.FirstName, &item.Tier, &item.TotalOTPs, &item.BalancePKR); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type RequiredChat struct {
	BotInstanceID int64
	ChatID        int64
	Title         string
	InviteURL     string
	Enabled       bool
}

func (s *Store) UpsertRequiredChat(ctx context.Context, chat RequiredChat) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO required_chats(bot_instance_id,chat_id,title,invite_url,enabled)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(bot_instance_id,chat_id) DO UPDATE SET
		title=EXCLUDED.title,invite_url=EXCLUDED.invite_url,enabled=EXCLUDED.enabled`,
		instanceID(chat.BotInstanceID), chat.ChatID, chat.Title, chat.InviteURL, chat.Enabled)
	return err
}

func (s *Store) ListRequiredChats(ctx context.Context) ([]RequiredChat, error) {
	return s.ListRequiredChatsForInstance(ctx, MainBotInstanceID)
}

func (s *Store) ListRequiredChatsForInstance(ctx context.Context, botInstanceID int64) ([]RequiredChat, error) {
	rows, err := s.pool.Query(ctx, `SELECT bot_instance_id,chat_id,title,invite_url,enabled FROM required_chats
		WHERE bot_instance_id=$1 AND enabled ORDER BY chat_id`, instanceID(botInstanceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequiredChat
	for rows.Next() {
		var chat RequiredChat
		if err := rows.Scan(&chat.BotInstanceID, &chat.ChatID, &chat.Title, &chat.InviteURL, &chat.Enabled); err != nil {
			return nil, err
		}
		out = append(out, chat)
	}
	return out, rows.Err()
}

func (s *Store) RemoveRequiredChat(ctx context.Context, chatID int64) error {
	return s.RemoveRequiredChatForInstance(ctx, MainBotInstanceID, chatID)
}

func (s *Store) RemoveRequiredChatForInstance(ctx context.Context, botInstanceID, chatID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM required_chats WHERE bot_instance_id=$1 AND chat_id=$2`, instanceID(botInstanceID), chatID)
	return err
}

func (s *Store) RegisterReferral(ctx context.Context, userID, referrerID int64) error {
	if userID == referrerID || referrerID == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND NOT banned)`, referrerID).Scan(&exists); err != nil || !exists {
		return err
	}
	// Only fresh accounts can be referred, and never by someone they referred,
	// so established users cannot hand out instant referral rewards.
	tag, err := tx.Exec(ctx, `UPDATE users SET referred_by=$2 WHERE id=$1 AND referred_by IS NULL AND total_otps=0
		AND NOT EXISTS(SELECT 1 FROM users r WHERE r.id=$2 AND r.referred_by=$1)`, userID, referrerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO referrals(user_id,referrer_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, referrerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ReferralStats(ctx context.Context, userID int64) (total, qualified int, earned float64, err error) {
	err = s.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE qualified),COALESCE(sum(reward_pkr),0)
		FROM referrals WHERE referrer_id=$1`, userID).Scan(&total, &qualified, &earned)
	return
}

func (s *Store) AllUserIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM users WHERE NOT banned ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) DatabaseReady(ctx context.Context) bool {
	var one int
	return s.pool.QueryRow(ctx, `SELECT 1`).Scan(&one) == nil && one == 1
}

func (s *Store) HasSetting(ctx context.Context, key string) bool {
	var value bool
	return s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM settings WHERE key=$1)`, key).Scan(&value) == nil && value
}

func IsNotFound(err error) bool { return err == pgx.ErrNoRows }

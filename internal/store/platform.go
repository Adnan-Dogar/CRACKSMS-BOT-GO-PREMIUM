package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/jackc/pgx/v5"
)

const MainBotInstanceID int64 = 1

var validTiers = map[string]bool{"free": true, "pro": true, "enterprise": true}

func instanceID(id int64) int64 {
	if id <= 0 {
		return MainBotInstanceID
	}
	return id
}

func (s *Store) EnsureUserForInstance(ctx context.Context, botInstanceID, id int64, username, firstName, lastName string) error {
	if err := s.EnsureUser(ctx, id, username, firstName, lastName); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO bot_instance_users(bot_instance_id,user_id,last_active_at)
		VALUES($1,$2,now()) ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET last_active_at=now()`,
		instanceID(botInstanceID), id)
	return err
}

func (s *Store) AllUserIDsForInstance(ctx context.Context, botInstanceID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT iu.user_id FROM bot_instance_users iu JOIN users u ON u.id=iu.user_id
		WHERE iu.bot_instance_id=$1 AND NOT u.banned ORDER BY iu.user_id`, instanceID(botInstanceID))
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

func (s *Store) HasAnyAdminRole(ctx context.Context, botInstanceID, userID int64) (bool, error) {
	botInstanceID = instanceID(botInstanceID)
	var allowed bool
	if botInstanceID == MainBotInstanceID {
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins WHERE user_id=$1)`, userID).Scan(&allowed)
		return allowed, err
	}
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM bot_instances WHERE id=$1 AND owner_user_id=$2
		UNION ALL SELECT 1 FROM tenant_admins WHERE bot_instance_id=$1 AND user_id=$2)`,
		botInstanceID, userID).Scan(&allowed)
	return allowed, err
}

func (s *Store) HasAdminPermission(ctx context.Context, botInstanceID, userID int64, permission string) (bool, error) {
	botInstanceID = instanceID(botInstanceID)
	var allowed bool
	if botInstanceID == MainBotInstanceID {
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins
			WHERE user_id=$1 AND ('*'=ANY(permissions) OR $2=ANY(permissions)))`, userID, permission).Scan(&allowed)
		return allowed, err
	}
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM bot_instances WHERE id=$1 AND owner_user_id=$2
		UNION ALL SELECT 1 FROM tenant_admins WHERE bot_instance_id=$1 AND user_id=$2
		AND ('*'=ANY(permissions) OR $3=ANY(permissions)))`, botInstanceID, userID, permission).Scan(&allowed)
	return allowed, err
}

func (s *Store) AddInstanceAdmin(ctx context.Context, botInstanceID, userID int64, permissions []string) error {
	botInstanceID = instanceID(botInstanceID)
	if len(permissions) == 0 {
		permissions = []string{"*"}
	}
	if botInstanceID == MainBotInstanceID {
		return s.AddAdmin(ctx, userID, permissions)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO users(id) VALUES($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO tenant_admins(bot_instance_id,user_id,permissions) VALUES($1,$2,$3)
		ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET permissions=EXCLUDED.permissions`, botInstanceID, userID, permissions)
	return err
}

func (s *Store) RemoveInstanceAdmin(ctx context.Context, botInstanceID, userID int64) error {
	botInstanceID = instanceID(botInstanceID)
	if botInstanceID == MainBotInstanceID {
		return s.RemoveAdmin(ctx, userID)
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM tenant_admins WHERE bot_instance_id=$1 AND user_id=$2`, botInstanceID, userID)
	return err
}

func (s *Store) UserTier(ctx context.Context, botInstanceID, userID int64) (string, error) {
	botInstanceID = instanceID(botInstanceID)
	var tier string
	err := s.pool.QueryRow(ctx, `SELECT tier FROM user_subscriptions
		WHERE bot_instance_id=$1 AND user_id=$2 AND status='active' AND (expires_at IS NULL OR expires_at>now())`,
		botInstanceID, userID).Scan(&tier)
	if errors.Is(err, pgx.ErrNoRows) {
		return "free", nil
	}
	return tier, err
}

func (s *Store) SetUserTier(ctx context.Context, botInstanceID, userID int64, tier string, expiresAt *time.Time, grantedBy int64) error {
	botInstanceID = instanceID(botInstanceID)
	tier = strings.ToLower(strings.TrimSpace(tier))
	if !validTiers[tier] {
		return errors.New("tier must be free, pro, or enterprise")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO users(id) VALUES($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO user_subscriptions(bot_instance_id,user_id,tier,status,expires_at,granted_by)
		VALUES($1,$2,$3,'active',$4,$5) ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET
		tier=EXCLUDED.tier,status='active',starts_at=now(),expires_at=EXCLUDED.expires_at,granted_by=EXCLUDED.granted_by`,
		botInstanceID, userID, tier, expiresAt, grantedBy)
	return err
}

func TierPanelLimit(tier string) int {
	switch tier {
	case "enterprise":
		return 50
	case "pro":
		return 10
	default:
		return 2
	}
}

func TierAllows(tier, feature string) bool {
	switch feature {
	case "themes", "basic_otp", "admin_panel":
		return true
	case "analytics", "webhooks", "scheduling", "priority_support":
		return tier == "pro" || tier == "enterprise"
	case "api_access", "custom_patterns", "child_bots", "rate_limiting":
		return tier == "enterprise"
	default:
		return false
	}
}

func (s *Store) Preference(ctx context.Context, botInstanceID, userID int64) (domain.UserPreference, error) {
	botInstanceID = instanceID(botInstanceID)
	pref := domain.UserPreference{BotInstanceID: botInstanceID, UserID: userID, Language: "en", CompactMenu: true, Timezone: "Asia/Karachi", DisplayFormat: "auto"}
	err := s.pool.QueryRow(ctx, `SELECT theme_id,language,compact_menu,timezone,display_format FROM user_preferences
		WHERE bot_instance_id=$1 AND user_id=$2`, botInstanceID, userID).
		Scan(&pref.ThemeID, &pref.Language, &pref.CompactMenu, &pref.Timezone, &pref.DisplayFormat)
	if errors.Is(err, pgx.ErrNoRows) {
		return pref, nil
	}
	return pref, err
}

func (s *Store) SetUserTheme(ctx context.Context, botInstanceID, userID int64, themeID int) error {
	if themeID < 0 || themeID > 9 {
		return errors.New("theme must be between 0 and 9")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO user_preferences(bot_instance_id,user_id,theme_id)
		VALUES($1,$2,$3) ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET theme_id=EXCLUDED.theme_id,updated_at=now()`,
		instanceID(botInstanceID), userID, themeID)
	return err
}

func (s *Store) SetInstanceTheme(ctx context.Context, botInstanceID int64, themeID int) error {
	if themeID < 0 || themeID > 9 {
		return errors.New("theme must be between 0 and 9")
	}
	_, err := s.pool.Exec(ctx, `UPDATE bot_instances SET default_theme=$2 WHERE id=$1`, instanceID(botInstanceID), themeID)
	return err
}

func (s *Store) DefaultTheme(ctx context.Context, botInstanceID int64) (int, error) {
	var theme int
	err := s.pool.QueryRow(ctx, `SELECT default_theme FROM bot_instances WHERE id=$1`, instanceID(botInstanceID)).Scan(&theme)
	return theme, err
}

func (s *Store) EffectiveTheme(ctx context.Context, botInstanceID, userID int64) (int, error) {
	pref, err := s.Preference(ctx, botInstanceID, userID)
	if err != nil {
		return 0, err
	}
	if pref.ThemeID != nil {
		return *pref.ThemeID, nil
	}
	return s.DefaultTheme(ctx, botInstanceID)
}

func (s *Store) CreateChildBotRequest(ctx context.Context, ownerID int64, name, token string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(token) == "" {
		return 0, errors.New("bot name and token are required")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO users(id) VALUES($1) ON CONFLICT DO NOTHING`, ownerID); err != nil {
		return 0, err
	}
	encrypted, err := s.cipher.Encrypt([]byte(strings.TrimSpace(token)))
	if err != nil {
		return 0, err
	}
	envelope, _ := json.Marshal(map[string]string{"encrypted": encrypted})
	var id int64
	hash := sha256.Sum256([]byte(strings.TrimSpace(token)))
	err = s.pool.QueryRow(ctx, `INSERT INTO bot_instances(parent_id,owner_user_id,name,token_config,token_hash,status,enabled)
		VALUES(1,$1,$2,$3,$4,'pending',false) RETURNING id`, ownerID, name, envelope, fmt.Sprintf("%x", hash[:])).Scan(&id)
	return id, err
}

func (s *Store) SetMainBotTokenHash(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(strings.TrimSpace(token)))
	_, err := s.pool.Exec(ctx, `UPDATE bot_instances SET token_hash=$2 WHERE id=$1`, MainBotInstanceID, fmt.Sprintf("%x", hash[:]))
	return err
}

func (s *Store) ChildBotToken(ctx context.Context, id int64) (string, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT token_config FROM bot_instances WHERE id=$1 AND NOT is_main`, id).Scan(&raw); err != nil {
		return "", err
	}
	var envelope struct {
		Encrypted string `json:"encrypted"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Encrypted == "" {
		return "", errors.New("invalid encrypted child bot token")
	}
	plain, err := s.cipher.Decrypt(envelope.Encrypted)
	return string(plain), err
}

func (s *Store) ApproveChildBot(ctx context.Context, id, adminID int64, tier string) error {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if !validTiers[tier] {
		return errors.New("tier must be free, pro, or enterprise")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner int64
	err = tx.QueryRow(ctx, `UPDATE bot_instances SET tier=$2,status='approved',enabled=true,approved_at=now(),approved_by=$3,last_error=''
		WHERE id=$1 AND NOT is_main AND status IN ('pending','stopped','rejected','error') RETURNING owner_user_id`, id, tier, adminID).Scan(&owner)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tenant_admins(bot_instance_id,user_id,permissions) VALUES($1,$2,ARRAY['*']::text[])
		ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET permissions=EXCLUDED.permissions`, id, owner); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RejectChildBot(ctx context.Context, id, adminID int64, reason string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE bot_instances SET status='rejected',enabled=false,approved_by=$2,last_error=$3
		WHERE id=$1 AND NOT is_main`, id, adminID, truncate(reason, 500))
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) SetChildBotEnabled(ctx context.Context, id int64, enabled bool) error {
	status := "stopped"
	if enabled {
		status = "approved"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE bot_instances SET enabled=$2,status=$3,last_error='' WHERE id=$1 AND NOT is_main`, id, enabled, status)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) UpdateBotRuntime(ctx context.Context, id int64, status, username string, runtimeErr error) error {
	lastError := ""
	if runtimeErr != nil {
		lastError = truncate(runtimeErr.Error(), 500)
	}
	_, err := s.pool.Exec(ctx, `UPDATE bot_instances SET status=$2,username=CASE WHEN $3='' THEN username ELSE $3 END,
		last_error=$4,last_started_at=CASE WHEN $2='running' THEN now() ELSE last_started_at END WHERE id=$1`,
		id, status, username, lastError)
	return err
}

func (s *Store) ListBotInstances(ctx context.Context, enabledOnly bool) ([]domain.BotInstance, error) {
	query := `SELECT id,parent_id,owner_user_id,name,username,tier,status,enabled,is_main,default_theme,
		default_group_privacy,settings,last_error,created_at,share_main_otps FROM bot_instances`
	if enabledOnly {
		query += ` WHERE enabled AND NOT is_main AND status IN ('approved','running','error')`
	}
	query += ` ORDER BY id`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BotInstance
	for rows.Next() {
		var item domain.BotInstance
		var raw []byte
		if err := rows.Scan(&item.ID, &item.ParentID, &item.OwnerUserID, &item.Name, &item.Username, &item.Tier,
			&item.Status, &item.Enabled, &item.IsMain, &item.DefaultTheme, &item.DefaultGroupPrivacy,
			&raw, &item.LastError, &item.CreatedAt, &item.ShareMainOTPs); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.Settings)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListBotInstancesForOwner(ctx context.Context, ownerID int64) ([]domain.BotInstance, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,parent_id,owner_user_id,name,username,tier,status,enabled,is_main,default_theme,
		default_group_privacy,settings,last_error,created_at,share_main_otps FROM bot_instances
		WHERE owner_user_id=$1 AND NOT is_main ORDER BY id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BotInstance
	for rows.Next() {
		var item domain.BotInstance
		var raw []byte
		if err := rows.Scan(&item.ID, &item.ParentID, &item.OwnerUserID, &item.Name, &item.Username, &item.Tier,
			&item.Status, &item.Enabled, &item.IsMain, &item.DefaultTheme, &item.DefaultGroupPrivacy,
			&raw, &item.LastError, &item.CreatedAt, &item.ShareMainOTPs); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.Settings)
		out = append(out, item)
	}
	return out, rows.Err()
}

type InstanceUserStats struct {
	TotalOTPs       int64
	TodayOTPs       int64
	ActiveNumbers   int64
	BaseTodayPKR    float64
	RewardsTodayPKR float64
	BalancePKR      float64
	BalanceUSD      float64
}

func (s *Store) UserStatsForInstance(ctx context.Context, botInstanceID, userID int64) (InstanceUserStats, error) {
	var stats InstanceUserStats
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM otp_events WHERE bot_instance_id=$1 AND assigned_user_id=$2 AND counted),
		(SELECT count(*) FROM otp_events WHERE bot_instance_id=$1 AND assigned_user_id=$2 AND counted
			AND (received_at AT TIME ZONE $3)::date=(now() AT TIME ZONE $3)::date),
		(SELECT count(*) FROM assignments a JOIN assignment_numbers an ON an.assignment_id=a.id
			WHERE a.bot_instance_id=$1 AND a.user_id=$2 AND a.state='active' AND an.consumed_at IS NULL AND an.released_at IS NULL),
		COALESCE((SELECT base_earnings_pkr FROM user_daily_progress WHERE user_id=$2
			AND local_date=(now() AT TIME ZONE $3)::date),0),
		COALESCE((SELECT reward_earnings_pkr FROM user_daily_progress WHERE user_id=$2
			AND local_date=(now() AT TIME ZONE $3)::date),0),
		(SELECT balance_pkr FROM users WHERE id=$2),(SELECT balance_usd FROM users WHERE id=$2)`,
		instanceID(botInstanceID), userID, s.location.String()).Scan(&stats.TotalOTPs, &stats.TodayOTPs,
		&stats.ActiveNumbers, &stats.BaseTodayPKR, &stats.RewardsTodayPKR, &stats.BalancePKR, &stats.BalanceUSD)
	return stats, err
}

func (s *Store) OTPHistory(ctx context.Context, botInstanceID, userID int64, limit, offset int) ([]domain.OTPHistoryItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 5
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id,e.panel_name,e.normalized_phone,e.service,e.country,e.message,e.code,e.received_at,e.counted,
		COALESCE(sc.price_pkr,0),COALESCE((SELECT sum(amount_pkr) FROM reward_awards ra
		WHERE ra.user_id=e.assigned_user_id AND ra.local_date=(e.received_at AT TIME ZONE $4)::date AND ra.otp_count=(
			SELECT otp_count FROM user_daily_progress udp WHERE udp.user_id=e.assigned_user_id
			AND udp.local_date=(e.received_at AT TIME ZONE $4)::date)),0)
		FROM otp_events e
		LEFT JOIN service_countries sc ON sc.service=e.service AND sc.country=e.country
		WHERE e.bot_instance_id=$1 AND e.assigned_user_id=$2 ORDER BY e.received_at DESC LIMIT $3 OFFSET $5`,
		instanceID(botInstanceID), userID, limit, s.location.String(), offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OTPHistoryItem
	for rows.Next() {
		var item domain.OTPHistoryItem
		if err := rows.Scan(&item.EventID, &item.PanelName, &item.Phone, &item.Service, &item.Country, &item.Message, &item.Code,
			&item.ReceivedAt, &item.Counted, &item.BasePKR, &item.RewardPKR); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) Analytics(ctx context.Context, botInstanceID int64) (domain.Analytics, error) {
	botInstanceID = instanceID(botInstanceID)
	var a domain.Analytics
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM bot_instance_users WHERE bot_instance_id=$1),
		(SELECT count(*) FROM bot_instance_users WHERE bot_instance_id=$1 AND last_active_at>now()-interval '24 hours'),
		(SELECT count(*) FROM otp_events WHERE bot_instance_id=$1),
		(SELECT count(*) FROM otp_events WHERE bot_instance_id=$1 AND counted),
		(SELECT count(*) FROM otp_events WHERE bot_instance_id=$1 AND received_at>date_trunc('day',now() AT TIME ZONE $2) AT TIME ZONE $2),
		(SELECT count(*) FROM numbers WHERE state='available'),
		(SELECT count(*) FROM assignments WHERE bot_instance_id=$1 AND state='active'),
		(SELECT count(*) FROM panels WHERE bot_instance_id=$1 AND enabled AND healthy),
		(SELECT count(*) FROM panels WHERE bot_instance_id=$1),
		(SELECT count(*) FROM delivery_jobs WHERE bot_instance_id=$1 AND state IN ('pending','retry','sending')),
		(SELECT count(*) FROM delivery_jobs WHERE bot_instance_id=$1 AND state='failed'),
		(SELECT count(*) FROM webhook_deliveries wd JOIN webhook_endpoints we ON we.id=wd.endpoint_id
			WHERE we.bot_instance_id=$1 AND wd.state IN ('pending','retry','sending')),
		(SELECT count(*) FROM scheduled_messages WHERE bot_instance_id=$1 AND state IN ('pending','sending'))`,
		botInstanceID, s.location.String()).Scan(&a.Users, &a.ActiveUsers24H, &a.TotalOTPs, &a.CountedOTPs,
		&a.OTPsToday, &a.AvailableNumbers, &a.AssignedNumbers, &a.ActivePanels, &a.TotalPanels,
		&a.DeliveryPending, &a.DeliveryFailed, &a.WebhookPending, &a.ScheduledPending)
	return a, err
}

func (s *Store) AddTutorial(ctx context.Context, tutorial domain.Tutorial, createdBy int64) (int64, error) {
	if strings.TrimSpace(tutorial.Title) == "" || strings.TrimSpace(tutorial.Body) == "" {
		return 0, errors.New("tutorial title and body are required")
	}
	if tutorial.ContentType == "" {
		tutorial.ContentType = "text"
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO tutorials(bot_instance_id,title,description,body,content_type,media_file_id,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, instanceID(tutorial.BotInstanceID), tutorial.Title,
		tutorial.Description, tutorial.Body, tutorial.ContentType, tutorial.MediaFileID, createdBy).Scan(&id)
	return id, err
}

func (s *Store) ListTutorials(ctx context.Context, botInstanceID int64) ([]domain.Tutorial, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,bot_instance_id,title,description,body,content_type,media_file_id
		FROM tutorials WHERE bot_instance_id=$1 AND enabled ORDER BY id`, instanceID(botInstanceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Tutorial
	for rows.Next() {
		var item domain.Tutorial
		if err := rows.Scan(&item.ID, &item.BotInstanceID, &item.Title, &item.Description, &item.Body,
			&item.ContentType, &item.MediaFileID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) Tutorial(ctx context.Context, botInstanceID, id int64) (domain.Tutorial, error) {
	var item domain.Tutorial
	err := s.pool.QueryRow(ctx, `SELECT id,bot_instance_id,title,description,body,content_type,media_file_id
		FROM tutorials WHERE bot_instance_id=$1 AND id=$2 AND enabled`, instanceID(botInstanceID), id).
		Scan(&item.ID, &item.BotInstanceID, &item.Title, &item.Description, &item.Body, &item.ContentType, &item.MediaFileID)
	return item, err
}

func (s *Store) DeleteTutorial(ctx context.Context, botInstanceID, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tutorials SET enabled=false WHERE bot_instance_id=$1 AND id=$2`, instanceID(botInstanceID), id)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) Audit(ctx context.Context, botInstanceID, actorID int64, action, targetType, targetID string, metadata any) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_log(bot_instance_id,actor_user_id,action,target_type,target_id,metadata)
		VALUES($1,NULLIF($2::bigint,0),$3,$4,$5,$6)`, instanceID(botInstanceID), actorID, action, targetType, targetID, raw)
	return err
}

func (s *Store) CountPanelsForInstance(ctx context.Context, botInstanceID int64) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM panels WHERE bot_instance_id=$1`, instanceID(botInstanceID)).Scan(&count)
	return count, err
}

func (s *Store) BotInstanceTier(ctx context.Context, botInstanceID int64) (string, error) {
	var tier string
	err := s.pool.QueryRow(ctx, `SELECT tier FROM bot_instances WHERE id=$1`, instanceID(botInstanceID)).Scan(&tier)
	return tier, err
}

func (s *Store) SetBotInstancePrivacy(ctx context.Context, botInstanceID int64, privacy string) error {
	privacy = strings.ToLower(strings.TrimSpace(privacy))
	if privacy != "visible" && privacy != "masked" && privacy != "hidden" {
		return fmt.Errorf("privacy must be visible, masked, or hidden")
	}
	_, err := s.pool.Exec(ctx, `UPDATE bot_instances SET default_group_privacy=$2 WHERE id=$1`, instanceID(botInstanceID), privacy)
	return err
}

func (s *Store) AddCustomOTPPattern(ctx context.Context, botInstanceID, createdBy int64, name, pattern string) (int64, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(pattern) == "" || len(pattern) > 500 {
		return 0, errors.New("pattern name and a regex of at most 500 characters are required")
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO custom_otp_patterns(bot_instance_id,name,pattern,created_by)
		VALUES($1,$2,$3,$4) ON CONFLICT(bot_instance_id,name) DO UPDATE SET pattern=EXCLUDED.pattern,
		enabled=true,created_by=EXCLUDED.created_by RETURNING id`, instanceID(botInstanceID), name, pattern, createdBy).Scan(&id)
	return id, err
}

func (s *Store) ListCustomOTPPatterns(ctx context.Context, botInstanceID int64) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT pattern FROM custom_otp_patterns WHERE bot_instance_id=$1 AND enabled ORDER BY id`, instanceID(botInstanceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pattern string
		if err := rows.Scan(&pattern); err != nil {
			return nil, err
		}
		out = append(out, pattern)
	}
	return out, rows.Err()
}

type CustomOTPPattern struct {
	ID      int64
	Name    string
	Pattern string
}

func (s *Store) ListCustomOTPPatternRows(ctx context.Context, botInstanceID int64) ([]CustomOTPPattern, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,pattern FROM custom_otp_patterns WHERE bot_instance_id=$1 AND enabled ORDER BY id`, instanceID(botInstanceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomOTPPattern
	for rows.Next() {
		var item CustomOTPPattern
		if err := rows.Scan(&item.ID, &item.Name, &item.Pattern); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) RemoveCustomOTPPattern(ctx context.Context, botInstanceID, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE custom_otp_patterns SET enabled=false WHERE bot_instance_id=$1 AND id=$2`, instanceID(botInstanceID), id)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

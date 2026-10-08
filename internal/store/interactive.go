package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type TelegramFlow struct {
	Kind      string
	Step      string
	Data      map[string]string
	ExpiresAt time.Time
}

func (s *Store) SetTelegramFlow(ctx context.Context, botInstanceID, userID int64, flow TelegramFlow) error {
	if strings.TrimSpace(flow.Kind) == "" || strings.TrimSpace(flow.Step) == "" {
		return errors.New("flow kind and step are required")
	}
	if flow.Data == nil {
		flow.Data = map[string]string{}
	}
	if flow.ExpiresAt.IsZero() {
		flow.ExpiresAt = time.Now().Add(30 * time.Minute)
	}
	raw, err := json.Marshal(flow.Data)
	if err != nil {
		return err
	}
	encrypted, err := s.cipher.Encrypt(raw)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(map[string]string{"encrypted": encrypted})
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO telegram_flows(bot_instance_id,user_id,kind,step,data_config,expires_at)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(bot_instance_id,user_id) DO UPDATE SET
		kind=EXCLUDED.kind,step=EXCLUDED.step,data_config=EXCLUDED.data_config,
		expires_at=EXCLUDED.expires_at,updated_at=now()`, instanceID(botInstanceID), userID,
		flow.Kind, flow.Step, envelope, flow.ExpiresAt)
	return err
}

func (s *Store) TelegramFlow(ctx context.Context, botInstanceID, userID int64) (TelegramFlow, error) {
	var flow TelegramFlow
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT kind,step,data_config,expires_at FROM telegram_flows
		WHERE bot_instance_id=$1 AND user_id=$2 AND expires_at>now()`, instanceID(botInstanceID), userID).
		Scan(&flow.Kind, &flow.Step, &raw, &flow.ExpiresAt)
	if err != nil {
		return flow, err
	}
	var envelope struct {
		Encrypted string `json:"encrypted"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Encrypted == "" {
		return flow, errors.New("interactive flow has invalid encrypted data")
	}
	plaintext, err := s.cipher.Decrypt(envelope.Encrypted)
	if err != nil {
		return flow, err
	}
	if err := json.Unmarshal(plaintext, &flow.Data); err != nil {
		return flow, err
	}
	return flow, nil
}

func (s *Store) ClearTelegramFlow(ctx context.Context, botInstanceID, userID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM telegram_flows WHERE bot_instance_id=$1 AND user_id=$2`, instanceID(botInstanceID), userID)
	return err
}

type WithdrawalAccount struct {
	ID          int64
	Method      string
	DisplayHint string
	Details     string
	Enabled     bool
}

func ValidWithdrawalMethod(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "jazzcash", "easypaisa", "binance", "usdt_bep20":
		return true
	default:
		return false
	}
}

func (s *Store) AddWithdrawalAccount(ctx context.Context, botInstanceID, userID int64, method, displayHint, details string) (int64, error) {
	method = strings.ToLower(strings.TrimSpace(method))
	details = strings.TrimSpace(details)
	if !ValidWithdrawalMethod(method) || details == "" || len(details) > 500 {
		return 0, errors.New("valid withdrawal method and details are required")
	}
	encrypted, err := s.cipher.Encrypt([]byte(details))
	if err != nil {
		return 0, err
	}
	envelope, err := json.Marshal(map[string]string{"encrypted": encrypted})
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256([]byte(method + "\x00" + strings.ToLower(details)))
	var id int64
	err = s.pool.QueryRow(ctx, `INSERT INTO withdrawal_accounts(
		bot_instance_id,user_id,method,display_hint,details_hash,details_config)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(bot_instance_id,user_id,method,details_hash) DO UPDATE SET
		display_hint=EXCLUDED.display_hint,details_config=EXCLUDED.details_config,enabled=true,updated_at=now()
		RETURNING id`, instanceID(botInstanceID), userID, method, displayHint,
		hex.EncodeToString(digest[:]), envelope).Scan(&id)
	return id, err
}

func (s *Store) ListWithdrawalAccounts(ctx context.Context, botInstanceID, userID int64) ([]WithdrawalAccount, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,method,display_hint,enabled FROM withdrawal_accounts
		WHERE bot_instance_id=$1 AND user_id=$2 AND enabled ORDER BY id`, instanceID(botInstanceID), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WithdrawalAccount
	for rows.Next() {
		var account WithdrawalAccount
		if err := rows.Scan(&account.ID, &account.Method, &account.DisplayHint, &account.Enabled); err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

func (s *Store) WithdrawalAccount(ctx context.Context, botInstanceID, userID, accountID int64) (WithdrawalAccount, error) {
	var account WithdrawalAccount
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id,method,display_hint,details_config,enabled FROM withdrawal_accounts
		WHERE bot_instance_id=$1 AND user_id=$2 AND id=$3 AND enabled`, instanceID(botInstanceID), userID, accountID).
		Scan(&account.ID, &account.Method, &account.DisplayHint, &raw, &account.Enabled)
	if err != nil {
		return account, err
	}
	var envelope struct {
		Encrypted string `json:"encrypted"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Encrypted == "" {
		return account, errors.New("withdrawal account has invalid encrypted details")
	}
	plaintext, err := s.cipher.Decrypt(envelope.Encrypted)
	account.Details = string(plaintext)
	return account, err
}

func (s *Store) RemoveWithdrawalAccount(ctx context.Context, botInstanceID, userID, accountID int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE withdrawal_accounts SET enabled=false,updated_at=now()
		WHERE bot_instance_id=$1 AND user_id=$2 AND id=$3`, instanceID(botInstanceID), userID, accountID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) UpsertServiceProfile(ctx context.Context, botInstanceID, createdBy int64, service, customEmojiID string) error {
	service = strings.TrimSpace(service)
	customEmojiID = strings.TrimSpace(customEmojiID)
	if service == "" || len(service) > 64 || customEmojiID == "" || len(customEmojiID) > 32 {
		return errors.New("service name and custom emoji ID are required")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO service_profiles(bot_instance_id,service_key,display_name,custom_emoji_id,created_by)
		VALUES($1,$2,$3,$4,NULLIF($5::bigint,0)) ON CONFLICT(bot_instance_id,service_key) DO UPDATE SET
		display_name=EXCLUDED.display_name,custom_emoji_id=EXCLUDED.custom_emoji_id,
		created_by=EXCLUDED.created_by,updated_at=now()`, instanceID(botInstanceID), strings.ToLower(service), service, customEmojiID, createdBy)
	return err
}

func (s *Store) ServiceEmojiID(ctx context.Context, botInstanceID int64, service string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT custom_emoji_id FROM service_profiles
		WHERE bot_instance_id=$1 AND service_key=$2`, instanceID(botInstanceID), strings.ToLower(strings.TrimSpace(service))).Scan(&id)
	return id, err
}

func decryptEnvelope(ciphertext []byte, decrypt func(string) ([]byte, error)) (string, error) {
	var envelope struct {
		Encrypted string `json:"encrypted"`
	}
	if err := json.Unmarshal(ciphertext, &envelope); err != nil || envelope.Encrypted == "" {
		return "", fmt.Errorf("invalid encrypted envelope")
	}
	plain, err := decrypt(envelope.Encrypted)
	return string(plain), err
}

// ClaimTelegramFlow consumes a particular form once, even across processes.
// Compare decrypted data because each save uses a new encryption nonce.
func (s *Store) ClaimTelegramFlow(ctx context.Context, botInstanceID, userID int64, expected TelegramFlow) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var kind, step string
	var raw []byte
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT kind,step,data_config,expires_at FROM telegram_flows
 WHERE bot_instance_id=$1 AND user_id=$2 AND expires_at>now() FOR UPDATE`, instanceID(botInstanceID), userID).Scan(&kind, &step, &raw, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if kind != expected.Kind || step != expected.Step || !expires.Equal(expected.ExpiresAt) {
		return false, nil
	}
	var envelope map[string]string
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return false, err
	}
	plaintext, err := s.cipher.Decrypt(envelope["encrypted"])
	if err != nil {
		return false, err
	}
	var data map[string]string
	if err = json.Unmarshal(plaintext, &data); err != nil {
		return false, err
	}
	if len(data) != len(expected.Data) {
		return false, nil
	}
	for key, value := range expected.Data {
		if actual, ok := data[key]; !ok || actual != value {
			return false, nil
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM telegram_flows WHERE bot_instance_id=$1 AND user_id=$2`, instanceID(botInstanceID), userID); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

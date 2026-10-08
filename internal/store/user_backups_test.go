package store_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
)

func backupAdmin(t *testing.T, repo *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := repo.EnsureUserForInstance(ctx, 1, 1, "admin", "Admin", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddInstanceAdmin(ctx, 1, 1, []string{"*"}); err != nil {
		t.Fatal(err)
	}
}
func TestUserBackupRoundTripPreservesCurrentMoneyAndRecoversMissingUsers(t *testing.T) {
	source, src := providerStore(t)
	destination, dst := providerStore(t)
	ctx := context.Background()
	backupAdmin(t, source)
	backupAdmin(t, destination)
	for _, id := range []int64{501, 502, 503} {
		if err := source.EnsureUserForInstance(ctx, 1, id, "user", "User", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := source.AddNumbers(ctx, "WhatsApp", "United States", "US", 1, 0, 1, []string{"+12025550123", "+12025550124"}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `INSERT INTO reward_schedules(id,name,enabled) VALUES(1,'Original reward',true);INSERT INTO reward_rules(id,schedule_id,threshold,reward_pkr) VALUES(1,1,1,3)`); err != nil {
		t.Fatal(err)
	}
	a, err := source.AssignNumbers(ctx, 502, "WhatsApp", "United States", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.OTPEvent{BotInstanceID: 1, Phone: a.Numbers[0].Phone, NormalizedPhone: a.Numbers[0].NormalizedPhone, Service: "WhatsApp", Message: "Code 123456", Code: "123456", DedupKey: "backup-sms", ReceivedAt: time.Now()}
	if result, err := source.AcceptOTP(ctx, event); err != nil || !result.Counted {
		t.Fatal(result, err)
	}
	if _, err = source.CreateWithdrawalForInstance(ctx, 1, 502, "PKR", 1, 0, "fixture account"); err != nil {
		t.Fatal(err)
	}
	active, err := source.AssignNumbers(ctx, 503, "WhatsApp", "United States", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = src.Exec(ctx, `INSERT INTO user_preferences(bot_instance_id,user_id,theme_id,timezone) VALUES(1,502,4,'Asia/Karachi');INSERT INTO user_favorites(bot_instance_id,user_id,service,country) VALUES(1,502,'WhatsApp','United States');INSERT INTO user_subscriptions(bot_instance_id,user_id,tier) VALUES(1,502,'pro')`); err != nil {
		t.Fatal(err)
	}
	// Sensitive credentials must never enter a user archive.
	panelSource, _ := source.SavePanelSource(ctx, 1, 0, "Private provider", "token_api", "https://panel.example/crapi")
	if _, err = source.SavePanelAccount(ctx, 1, panelSource, 0, "Private", map[string]any{"token": "provider-secret"}, false); err != nil {
		t.Fatal(err)
	}
	parts, err := source.ExportUserBackup(ctx, 1)
	if err != nil || len(parts) != 1 {
		t.Fatal(len(parts), err)
	}
	if strings.Contains(string(parts[0].File), "123456") || strings.Contains(string(parts[0].File), "fixture account") {
		t.Fatal("archive is not encrypted")
	}
	archive, err := destination.DecodeUserBackup([][]byte{parts[0].File})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archive.Tables["panels"]; ok {
		t.Fatal("provider credentials included")
	}
	// A backup made before the USD/limited-reward migration has no new fields.
	for table, columns := range map[string][]string{"reward_rules": {"reward_usd", "max_users"}, "reward_awards": {"amount_usd"}, "user_daily_progress": {"reward_earnings_usd"}} {
		for i, raw := range archive.Tables[table] {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			for _, column := range columns {
				delete(fields, column)
			}
			archive.Tables[table][i], err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = destination.EnsureUserForInstance(ctx, 1, 501, "current", "Current", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = dst.Exec(ctx, `UPDATE users SET balance_pkr=100,total_otps=8 WHERE id=501;INSERT INTO balance_ledger(id,user_id,currency,amount,entry_type,reference_type,reference_id) VALUES(100,501,'PKR',100,'fixture','fixture','current');INSERT INTO reward_schedules(id,name,enabled) VALUES(100,'Current reward',true);INSERT INTO reward_rules(id,schedule_id,threshold,reward_pkr) VALUES(100,100,10,5)`); err != nil {
		t.Fatal(err)
	}
	// A phone from an archived active assignment is currently assigned elsewhere.
	if _, err = destination.AddNumbers(ctx, "WhatsApp", "United States", "US", 9, 0, 1, []string{active.Numbers[0].Phone}); err != nil {
		t.Fatal(err)
	}
	current, err := destination.AssignNumbers(ctx, 700, "WhatsApp", "United States", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := destination.PreviewUserBackup(ctx, 1, archive)
	if err != nil || preview.NewUsers != 2 || preview.ExistingUsers != 2 {
		t.Fatal(preview, err)
	}
	result, err := destination.RestoreUserBackup(ctx, 1, archive)
	if err != nil || result.NewUsers != 2 {
		t.Fatal(result, err)
	}
	pkr, _, total, err := destination.UserBalance(ctx, 501)
	if err != nil || pkr != 100 || total != 8 {
		t.Fatal("current finances changed", pkr, total, err)
	}
	pkr, _, total, err = destination.UserBalance(ctx, 502)
	if err != nil || pkr != 3 || total != 1 {
		t.Fatal("missing user finances not recovered", pkr, total, err)
	}
	history, err := destination.OTPHistory(ctx, 1, 502, 10, 0)
	if err != nil || len(history) != 1 || history[0].Code != "123456" {
		t.Fatal("OTP history not restored", history, err)
	}
	var assigned, closed string
	var delivery, awards, withdrawals int
	if err = dst.QueryRow(ctx, `SELECT (SELECT state::text FROM numbers WHERE id=$1),(SELECT state::text FROM assignments WHERE id=$2),(SELECT count(*) FROM delivery_jobs),(SELECT count(*) FROM reward_awards WHERE user_id=502),(SELECT count(*) FROM withdrawals WHERE user_id=502)`, current.Numbers[0].ID, active.ID).Scan(&assigned, &closed, &delivery, &awards, &withdrawals); err != nil || assigned != "assigned" || closed != "expired" || delivery != 0 || awards != 1 || withdrawals != 1 {
		t.Fatal("history activated jobs or changed current inventory", assigned, closed, delivery, awards, withdrawals, err)
	}
	if repeated, err := destination.RestoreUserBackup(ctx, 1, archive); err != nil || !repeated.AlreadyRestored {
		t.Fatal("restore is not idempotent", repeated, err)
	}
	pkr, _, total, err = destination.UserBalance(ctx, 502)
	if err != nil || pkr != 3 || total != 1 {
		t.Fatal("duplicate restore changed money", pkr, total, err)
	}
	if _, err = destination.ExportUserBackup(ctx, 502); err == nil {
		t.Fatal("user exported administrator backup")
	}
}
func TestBackupIntegrityAndAtomicConflict(t *testing.T) {
	source, src := providerStore(t)
	destination, dst := providerStore(t)
	ctx := context.Background()
	backupAdmin(t, source)
	backupAdmin(t, destination)
	if err := source.EnsureUserForInstance(ctx, 1, 502, "user", "User", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `UPDATE users SET balance_pkr=7 WHERE id=502;INSERT INTO balance_ledger(id,user_id,currency,amount,entry_type,reference_type,reference_id) VALUES(1,502,'PKR',7,'fixture','fixture','backup')`); err != nil {
		t.Fatal(err)
	}
	parts, err := source.ExportUserBackup(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := append([]byte(nil), parts[0].File...)
	corrupted[len(corrupted)-5] ^= 1
	if _, err = destination.DecodeUserBackup([][]byte{corrupted}); err == nil {
		t.Fatal("tampered archive accepted")
	}
	if _, err = destination.DecodeUserBackup([][]byte{parts[0].File, parts[0].File}); err == nil {
		t.Fatal("duplicate parts accepted")
	}
	archive, err := destination.DecodeUserBackup([][]byte{parts[0].File})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dst.Exec(ctx, `INSERT INTO balance_ledger(id,user_id,currency,amount,entry_type,reference_type,reference_id) VALUES(1,1,'PKR',10,'fixture','fixture','live')`); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.RestoreUserBackup(ctx, 1, archive); err == nil {
		t.Fatal("history ID conflict ignored")
	}
	var users, restores int
	if err = dst.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE id=502),(SELECT count(*) FROM user_backup_restores)`).Scan(&users, &restores); err != nil || users != 0 || restores != 0 {
		t.Fatal("failed restore partially applied", users, restores, err)
	}
}

func TestMultipartBackupAcceptsReverseOrderAndRejectsMissingParts(t *testing.T) {
	repo, pool := providerStore(t)
	ctx := context.Background()
	backupAdmin(t, repo)
	random := make([]byte, 8<<20)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET last_name=$1 WHERE id=1`, base64.StdEncoding.EncodeToString(random)); err != nil {
		t.Fatal(err)
	}
	parts, err := repo.ExportUserBackup(ctx, 1)
	if err != nil || len(parts) < 2 {
		t.Fatal("large backup was not split", len(parts), err)
	}
	if _, err = repo.DecodeUserBackup([][]byte{parts[0].File}); err == nil {
		t.Fatal("incomplete multipart accepted")
	}
	files := [][]byte{}
	for i := len(parts) - 1; i >= 0; i-- {
		if len(parts[i].File) > store.BackupFileLimit {
			t.Fatal("part cannot be uploaded")
		}
		files = append(files, parts[i].File)
	}
	decoded, err := repo.DecodeUserBackup(files)
	if err != nil || len(decoded.Tables["users"]) != 1 {
		t.Fatal("reversed parts not recovered", err)
	}
}

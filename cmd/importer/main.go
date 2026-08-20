package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adnan-dogar/cracksms-vnext/internal/db"
	"github.com/adnan-dogar/cracksms-vnext/internal/domain"
	"github.com/adnan-dogar/cracksms-vnext/internal/secure"
	"github.com/adnan-dogar/cracksms-vnext/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/mattn/go-sqlite3"
)

type legacyData struct {
	Services        map[string]legacyService    `json:"services"`
	Users           map[string]legacyUser       `json:"users"`
	Withdrawals     map[string]legacyWithdrawal `json:"withdrawals"`
	ExtraAdmins     []int64                     `json:"extra_admins"`
	DynamicPanels   []legacyDynamicPanel        `json:"dynamic_panels"`
	StaticAPIPanels []legacyStaticPanel         `json:"static_api_panels"`
}
type legacyService struct {
	Name      string                   `json:"name"`
	Countries map[string]legacyCountry `json:"countries"`
}
type legacyCountry struct {
	Name            string   `json:"name"`
	Code            string   `json:"code"`
	Price           float64  `json:"price"`
	PriceDollar     float64  `json:"price_dollar"`
	Numbers         []string `json:"numbers"`
	NumbersPerCycle int      `json:"numbers_per_cycle"`
}
type legacyUser struct {
	ID                  int64   `json:"id"`
	Username            string  `json:"username"`
	FirstName           string  `json:"first_name"`
	LastName            string  `json:"last_name"`
	Balance             float64 `json:"balance"`
	BalanceDollar       float64 `json:"balance_dollar"`
	TotalOTPs           int64   `json:"total_otps"`
	ReferredBy          int64   `json:"referred_by"`
	ReferralRewardGiven bool    `json:"referral_reward_given"`
	JoinedAt            string  `json:"joined_at"`
}
type legacyWithdrawal struct {
	ID        int64   `json:"id"`
	UserID    int64   `json:"user_id"`
	Method    string  `json:"method"`
	Amount    float64 `json:"amount"`
	AmountUSD float64 `json:"amount_usd"`
	Details   string  `json:"details"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
}
type legacyDynamicPanel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
}
type legacyStaticPanel struct {
	Name       string `json:"name"`
	NumbersAPI string `json:"numbers_api"`
	SmsAPI     string `json:"sms_api"`
	Enabled    bool   `json:"enabled"`
	TokenBased bool   `json:"token_based"`
	APIURL     string `json:"api_url"`
	Token      string `json:"token"`
	APIType    string `json:"api_type"`
	Records    int    `json:"records"`
}

func main() {
	dataPath := flag.String("data", "data.json", "legacy data.json path")
	referralPath := flag.String("referral-db", "", "optional legacy referral SQLite database")
	apply := flag.Bool("apply", false, "write the import; without this flag the command is a dry run")
	backupDir := flag.String("backup-dir", "legacy-backup", "backup directory used before apply")
	flag.Parse()
	if err := run(*dataPath, *referralPath, *apply, *backupDir); err != nil {
		slog.Error("legacy import failed", "error", err)
		os.Exit(1)
	}
}

func run(dataPath, referralPath string, apply bool, backupDir string) error {
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		return err
	}
	var legacy legacyData
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("parse legacy JSON: %w", err)
	}
	numberCount := 0
	for _, service := range legacy.Services {
		for _, country := range service.Countries {
			numberCount += len(country.Numbers)
		}
	}
	fmt.Printf("Legacy summary: users=%d services=%d numbers=%d withdrawals=%d dynamic_panels=%d api_panels=%d admins=%d\n",
		len(legacy.Users), len(legacy.Services), numberCount, len(legacy.Withdrawals), len(legacy.DynamicPanels),
		len(legacy.StaticAPIPanels), len(legacy.ExtraAdmins))
	if !apply {
		fmt.Println("Dry run complete. Re-run with --apply after reviewing the counts.")
		return nil
	}
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}
	if err := copyFile(dataPath, filepath.Join(backupDir, filepath.Base(dataPath))); err != nil {
		return err
	}
	if referralPath != "" {
		if err := copyFile(referralPath, filepath.Join(backupDir, filepath.Base(referralPath))); err != nil {
			return err
		}
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("PANEL_CONFIG_KEY")))
	if err != nil || len(key) != 32 {
		return errors.New("PANEL_CONFIG_KEY must be a base64-encoded 32-byte key")
	}
	cipher, err := secure.NewCipher(key)
	if err != nil {
		return err
	}
	location, _ := time.LoadLocation("Asia/Karachi")
	ctx := context.Background()
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	repo := store.New(pool, location, cipher)

	for _, user := range legacy.Users {
		if user.ID == 0 {
			continue
		}
		if err := repo.EnsureUser(ctx, user.ID, user.Username, user.FirstName, user.LastName); err != nil {
			return err
		}
	}
	for _, user := range legacy.Users {
		if user.ID == 0 {
			continue
		}
		_, err := pool.Exec(ctx, `UPDATE users SET balance_pkr=$2,balance_usd=$3,total_otps=$4,
			referred_by=NULLIF($5,0),referral_reward_given=$6 WHERE id=$1`, user.ID, user.Balance, user.BalanceDollar,
			user.TotalOTPs, user.ReferredBy, user.ReferralRewardGiven)
		if err != nil {
			return err
		}
	}
	for serviceKey, service := range legacy.Services {
		serviceName := service.Name
		if serviceName == "" {
			serviceName = serviceKey
		}
		for countryKey, country := range service.Countries {
			countryName := country.Name
			if countryName == "" {
				countryName = countryKey
			}
			perCycle := country.NumbersPerCycle
			if perCycle <= 0 {
				perCycle = 3
			}
			if _, err := repo.AddNumbers(ctx, serviceName, countryName, country.Code, country.Price, country.PriceDollar, perCycle, country.Numbers); err != nil {
				return err
			}
		}
	}
	for _, adminID := range legacy.ExtraAdmins {
		if err := repo.AddAdmin(ctx, adminID, []string{"*"}); err != nil {
			return err
		}
	}
	for _, panel := range legacy.DynamicPanels {
		_, err := repo.UpsertPanel(ctx, domain.Panel{Name: panel.Name, Kind: "login", Enabled: panel.Enabled,
			PollInterval: 2 * time.Second, Config: map[string]any{"base_url": panel.BaseURL, "username": panel.Username, "password": panel.Password}})
		if err != nil {
			return err
		}
	}
	for _, panel := range legacy.StaticAPIPanels {
		kind := "legacy_api"
		config := map[string]any{"sms_url": panel.SmsAPI, "numbers_url": panel.NumbersAPI}
		if panel.TokenBased {
			kind = "token_api"
			config = map[string]any{"url": panel.APIURL, "token": panel.Token, "api_type": panel.APIType, "records": panel.Records}
		}
		_, err := repo.UpsertPanel(ctx, domain.Panel{Name: panel.Name, Kind: kind, Enabled: panel.Enabled, PollInterval: 2 * time.Second, Config: config})
		if err != nil {
			return err
		}
	}
	for _, withdrawal := range legacy.Withdrawals {
		state := normalizeWithdrawalState(withdrawal.Status)
		_, err := pool.Exec(ctx, `INSERT INTO withdrawals(id,user_id,method,amount_pkr,amount_usd,details,state,created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,COALESCE(NULLIF($8,'')::timestamptz,now())) ON CONFLICT(id) DO NOTHING`,
			withdrawal.ID, withdrawal.UserID, withdrawal.Method, withdrawal.Amount, withdrawal.AmountUSD, withdrawal.Details, state, withdrawal.CreatedAt)
		if err != nil {
			return err
		}
	}
	if referralPath != "" {
		if err := importReferrals(ctx, pool, referralPath); err != nil {
			return err
		}
	}
	fmt.Println("Import committed successfully. Active holds and legacy OTP dedup history were intentionally not imported.")
	return nil
}

func importReferrals(ctx context.Context, pool *pgxpool.Pool, path string) error {
	// Kept separate from the bot runtime: the importer is the only binary linked with SQLite.
	dbFile, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer dbFile.Close()
	rows, err := dbFile.Query(`SELECT user_id,username,referred_by,referral_reward FROM referral_users`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, referredBy int64
		var username string
		var reward float64
		if err := rows.Scan(&userID, &username, &referredBy, &reward); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,username) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET username=EXCLUDED.username`, userID, username); err != nil {
			return err
		}
		if referredBy != 0 {
			if _, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES($1) ON CONFLICT DO NOTHING`, referredBy); err != nil {
				return err
			}
			if _, err := pool.Exec(ctx, `INSERT INTO referrals(user_id,referrer_id,qualified,reward_pkr)
				VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET referrer_id=EXCLUDED.referrer_id,
				qualified=EXCLUDED.qualified,reward_pkr=EXCLUDED.reward_pkr`, userID, referredBy, reward > 0, reward); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func normalizeWithdrawalState(value string) string {
	switch strings.ToLower(value) {
	case "approved":
		return "approved"
	case "rejected":
		return "rejected"
	case "paid":
		return "paid"
	default:
		return "pending"
	}
}

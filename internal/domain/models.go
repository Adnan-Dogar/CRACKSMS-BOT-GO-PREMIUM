package domain

import "time"

type OTPEvent struct {
	ID                string
	DedupKey          string
	BotInstanceID     int64
	PanelID           int64
	PanelName         string
	Phone             string
	NormalizedPhone   string
	Service           string
	Country           string
	Message           string
	Code              string
	ProviderTimestamp *time.Time
	ReceivedAt        time.Time
}

type AcceptedOTP struct {
	EventID          string
	Duplicate        bool
	AssignedUserID   int64
	Counted          bool
	DailyCount       int
	BaseCreditPKR    float64
	RewardCreditPKR  float64
	NewBalancePKR    float64
	TriggeredRewards []RewardAward
}

type RewardAward struct {
	Threshold int
	AmountPKR float64
}

type Number struct {
	ID              int64
	Phone           string
	NormalizedPhone string
	Service         string
	Country         string
}

type Assignment struct {
	ID            string
	BotInstanceID int64
	UserID        int64
	Service       string
	Country       string
	Numbers       []Number
	ExpiresAt     time.Time
}

type RecycleResult struct {
	Assignments int
	Returned    int
	Consumed    int
}

type OTPGroupDestination struct {
	BotInstanceID  int64
	ChatID         int64
	Title          string
	ButtonsEnabled bool
	Enabled        bool
	Healthy        bool
	LastError      string
	LastSuccessAt  *time.Time
	OTPVisibility  string
	ThemeID        *int
}

type DeliveryJob struct {
	ID             int64
	BotInstanceID  int64
	Event          OTPEvent
	TargetKind     string
	TargetID       int64
	ButtonsEnabled bool
	ThemeID        int
	OTPVisibility  string
	Attempts       int
}

type RewardRule struct {
	Threshold int
	AmountPKR float64
}

type RewardSchedule struct {
	ID            int64
	Name          string
	UserID        *int64
	Enabled       bool
	EffectiveFrom time.Time
	Rules         []RewardRule
}

type Panel struct {
	ID                  int64
	BotInstanceID       int64
	Name                string
	Kind                string
	Config              map[string]any
	PollInterval        time.Duration
	Enabled             bool
	Healthy             bool
	ConsecutiveFailures int
	LastCursor          string
}

type IngestJob struct {
	ID       int64
	Event    OTPEvent
	Attempts int
}

type BotInstance struct {
	ID                  int64
	ParentID            *int64
	OwnerUserID         *int64
	Name                string
	Username            string
	Tier                string
	Status              string
	Enabled             bool
	IsMain              bool
	DefaultTheme        int
	DefaultGroupPrivacy string
	Settings            map[string]any
	LastError           string
	CreatedAt           time.Time
}

type UserPreference struct {
	BotInstanceID int64
	UserID        int64
	ThemeID       *int
	Language      string
	CompactMenu   bool
	Timezone      string
}

type OTPHistoryItem struct {
	EventID    string
	PanelName  string
	Phone      string
	Service    string
	Country    string
	Message    string
	Code       string
	ReceivedAt time.Time
	Counted    bool
	BasePKR    float64
	RewardPKR  float64
}

type Analytics struct {
	Users            int64
	ActiveUsers24H   int64
	TotalOTPs        int64
	CountedOTPs      int64
	OTPsToday        int64
	AvailableNumbers int64
	AssignedNumbers  int64
	ActivePanels     int64
	TotalPanels      int64
	DeliveryPending  int64
	DeliveryFailed   int64
	WebhookPending   int64
	ScheduledPending int64
}

type WebhookEndpoint struct {
	ID                  int64
	BotInstanceID       int64
	UserID              int64
	URL                 string
	Secret              string
	Events              []string
	Enabled             bool
	ConsecutiveFailures int
	LastError           string
}

type WebhookDelivery struct {
	ID        int64
	Endpoint  WebhookEndpoint
	Event     OTPEvent
	EventName string
	Attempts  int
}

type ScheduledMessage struct {
	ID            int64
	BotInstanceID int64
	CreatorUserID int64
	TargetKind    string
	TargetID      *int64
	Body          string
	ParseMode     string
	DeliverAt     time.Time
	Attempts      int
}

type APIKey struct {
	ID            int64
	BotInstanceID int64
	UserID        int64
	Name          string
	Prefix        string
	Scopes        []string
	Enabled       bool
	ExpiresAt     *time.Time
	LastUsedAt    *time.Time
}

type Tutorial struct {
	ID            int64
	BotInstanceID int64
	Title         string
	Description   string
	Body          string
	ContentType   string
	MediaFileID   string
}

package engine

import "time"

// Level is where a rule or limit is configured (§6.1, §7.1).
type Level string

const (
	LevelService Level = "SERVICE"
	LevelTier    Level = "TIER"
	LevelAccount Level = "ACCOUNT"
)

// Metric: money or count (§6.2).
type Metric string

const (
	MetricAmount Metric = "AMOUNT"
	MetricCount  Metric = "COUNT"
)

// Period: calendar-based windows (§6.4). No rolling windows.
type Period string

const (
	PeriodPerTxn  Period = "PER_TXN"
	PeriodDaily   Period = "DAILY"
	PeriodWeekly  Period = "WEEKLY"
	PeriodMonthly Period = "MONTHLY"
)

// AccountType: GENERAL or SPECIAL (§5).
type AccountType string

const (
	AccountTypeGeneral AccountType = "GENERAL_ACCOUNT"
	AccountTypeSpecial AccountType = "SPECIAL_ACCOUNT"
)

// LimitKey identifies a limit uniquely: same operation+metric+period
// limits at different levels are compared with each other (§6.2).
type LimitKey struct {
	Operation string `json:"operation"`
	Metric    Metric `json:"metric"`
	Period    Period `json:"period"`
}

// Limit is a cap on an operation: {level, operation, metric, period, ceiling} (§6.2).
type Limit struct {
	Level      Level `json:"level"`
	LimitKey
	Ceiling int64 `json:"ceiling"`
	// Scope identifies what the limit applies to:
	// SERVICE  -> product name
	// TIER     -> product + "#" + tier
	// ACCOUNT  -> account id
	Scope string `json:"scope"`
}

// Rule is an ON/OFF switch (§7). Kind distinguishes the two jobs:
// PERMISSION — is the operation allowed at all (broader OFF wins, §7.2)
// ENFORCEMENT — is this level's limit actually checked (independent, §7.3)
type Rule struct {
	Level Level `json:"level"`
	Kind  RuleKind `json:"kind"`
	// For PERMISSION: the operation name.
	// For ENFORCEMENT: the LimitKey (operation+metric+period) whose check is switched.
	Operation string `json:"operation"`
	Metric    Metric `json:"metric,omitempty"`
	Period    Period `json:"period,omitempty"`
	Enabled   bool   `json:"enabled"`
	// Scope as per Limit.Scope.
	Scope string `json:"scope"`
}

type RuleKind string

const (
	RuleKindPermission  RuleKind = "PERMISSION"
	RuleKindEnforcement RuleKind = "ENFORCEMENT"
)

// Account (§1.1): identity is [Product] # [AccountType] # [Tier].
type Account struct {
	ID          string      `json:"id"`
	Product     string      `json:"product"`
	AccountType AccountType `json:"accountType"`
	Tier        string      `json:"tier"`
	Balance     int64       `json:"balance"`
	// Config holds Account Config Table attributes (§8): ALLOW_NEGATIVE_BALANCE etc.
	Config map[string]string `json:"config,omitempty"`
}

// Txn is a transaction to simulate.
type Txn struct {
	AccountID string `json:"accountId"`
	Operation string `json:"operation"`
	Amount    int64  `json:"amount"`
	// When defaults to "now"; overridable so the UI can simulate calendar resets.
	When time.Time `json:"when"`
}

// Usage is the consumed amount/count per limit window, keyed by
// account # operation # metric # window-identifier (day/week/month key).
type usageKey struct {
	AccountID string
	Operation string
	Metric    Metric
	Window    string // e.g. 2026-08-14 / 2026-W33 / 2026-08
}

type usageVal struct {
	Amount int64
	Count  int64
}

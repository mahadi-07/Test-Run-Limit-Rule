package engine

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// OperationDef is one entry of the Core Engine Service Configuration (§3).
type OperationDef struct {
	Name        string `json:"name"`
	Direction   string `json:"direction"` // DEBIT | CREDIT | NONE — used only by the §8 balance gate
	Description string `json:"description"`
}

// Config is everything an operator can edit. Rules and limits may only
// reference operations present in Operations (§3.1).
type Config struct {
	Operations []OperationDef        `json:"operations"` // the master menu
	Products   map[string][]string   `json:"products"`   // product -> offered operations (§4)
	Tiers      []string              `json:"tiers"`      // DEFAULT_TIER, TIER_1 ... (§1)
	Limits     []Limit               `json:"limits"`
	Rules      []Rule                `json:"rules"`
}

// State is the whole simulator world.
type State struct {
	mu       sync.RWMutex
	Config   Config
	Accounts map[string]Account
	Usage    map[usageKey]usageVal
	Activity []ActivityEntry
}

// ActivityEntry is one simulated transaction, kept newest-first for the log.
type ActivityEntry struct {
	ID        int64      `json:"id"`
	When      string     `json:"when"`      // simulated transaction time
	AccountID string     `json:"accountId"`
	Operation string     `json:"operation"`
	Amount    int64      `json:"amount"`
	Committed bool       `json:"committed"` // executed vs dry-run
	Allowed   bool       `json:"allowed"`
	Reason    string     `json:"reason"`
	Recorded  string     `json:"recorded"` // wall-clock time of the simulate call
}

func NewState() *State {
	s := &State{
		Accounts: map[string]Account{},
		Usage:    map[usageKey]usageVal{},
		Activity: []ActivityEntry{},
	}
	s.Reset()
	return s
}

// Reset restores the seeded scenario world from the PDF.
func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config = seedConfig()
	s.Accounts = seedAccounts()
	s.Usage = map[usageKey]usageVal{}
	s.Activity = []ActivityEntry{}
}

// ResetBlank clears the world to day 0: no operations, products, limits,
// rules or accounts — only DEFAULT_TIER, which always exists because it is
// the tier automatically assigned when nothing specific is configured (§1).
func (s *State) ResetBlank() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config = Config{
		Operations: []OperationDef{},
		Products:   map[string][]string{},
		Tiers:      []string{"DEFAULT_TIER"},
		Limits:     []Limit{},
		Rules:      []Rule{},
	}

	s.Accounts = map[string]Account{}
	s.Usage = map[usageKey]usageVal{}
	s.Activity = []ActivityEntry{}
}

// Log records one simulated transaction in the activity log.
func (s *State) Log(e ActivityEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.ID = int64(len(s.Activity)) + 1
	s.Activity = append([]ActivityEntry{e}, s.Activity...) // newest first
	if len(s.Activity) > 200 {
		s.Activity = s.Activity[:200]
	}
}

// ActivitySnapshot returns the log, newest first.
func (s *State) ActivitySnapshot() []ActivityEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]ActivityEntry(nil), s.Activity...)
}

func seedConfig() Config {
	return Config{
		Operations: []OperationDef{
			{Name: "TRANSFER_IN", Direction: "CREDIT", Description: "Money coming into the account"},
			{Name: "TRANSFER_OUT", Direction: "DEBIT", Description: "Money leaving the account"},
			{Name: "RTGS", Direction: "DEBIT", Description: "Real Time Gross Settlement"},
			{Name: "BFTN", Direction: "DEBIT", Description: "Bangladesh Fund Transfer Network"},
			{Name: "NPSB", Direction: "DEBIT", Description: "National Payment Switch Bangladesh"},
			{Name: "AGENT_POINT_DEPOSIT", Direction: "CREDIT", Description: "Cash deposit at agent point"},
			{Name: "AGENT_POINT_WITHDRAWAL", Direction: "DEBIT", Description: "Cash withdrawal at agent point"},
		},
		Products: map[string][]string{
			"CURRENT_ACCOUNT": {"TRANSFER_IN", "TRANSFER_OUT", "RTGS", "BFTN", "NPSB", "AGENT_POINT_DEPOSIT", "AGENT_POINT_WITHDRAWAL"},
			"DPS_SAVINGS_ACCOUNT": {"TRANSFER_IN", "TRANSFER_OUT", "AGENT_POINT_DEPOSIT"},
			"GENERAL_SAVINGS_ACCOUNT": {"TRANSFER_IN", "TRANSFER_OUT", "NPSB", "AGENT_POINT_DEPOSIT", "AGENT_POINT_WITHDRAWAL"},
			"LOAN_ACCOUNT": {"TRANSFER_IN", "TRANSFER_OUT", "RTGS"},
		},
		Tiers: []string{"DEFAULT_TIER", "TIER_1", "TIER_2", "TIER_3"},
		Limits: []Limit{
			// §6.5 worked example — TRANSFER_IN for CURRENT_ACCOUNT / TIER_1 / Aisha
			{Level: LevelService, LimitKey: LimitKey{Operation: "TRANSFER_IN", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 10_000_000, Scope: "CURRENT_ACCOUNT"},
			{Level: LevelTier, LimitKey: LimitKey{Operation: "TRANSFER_IN", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 10_000, Scope: "CURRENT_ACCOUNT#TIER_1"},
			{Level: LevelTier, LimitKey: LimitKey{Operation: "TRANSFER_IN", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 15_000, Scope: "CURRENT_ACCOUNT#TIER_1"},
			{Level: LevelTier, LimitKey: LimitKey{Operation: "TRANSFER_IN", Metric: MetricCount, Period: PeriodDaily}, Ceiling: 2, Scope: "CURRENT_ACCOUNT#TIER_1"},
			{Level: LevelAccount, LimitKey: LimitKey{Operation: "TRANSFER_IN", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 20_000, Scope: "ACC-001"},

			// §6.2 example — TRANSFER_OUT carries three limits at once
			{Level: LevelService, LimitKey: LimitKey{Operation: "TRANSFER_OUT", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 100_000, Scope: "CURRENT_ACCOUNT"},
			{Level: LevelTier, LimitKey: LimitKey{Operation: "TRANSFER_OUT", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 10_000, Scope: "CURRENT_ACCOUNT#TIER_1"},
			{Level: LevelAccount, LimitKey: LimitKey{Operation: "TRANSFER_OUT", Metric: MetricCount, Period: PeriodDaily}, Ceiling: 5, Scope: "ACC-001"},

			// §0 Day 60 + §7.4.2 — Aisha's withdrawal caps
			{Level: LevelTier, LimitKey: LimitKey{Operation: "AGENT_POINT_WITHDRAWAL", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 10_000, Scope: "CURRENT_ACCOUNT#TIER_1"},
			{Level: LevelTier, LimitKey: LimitKey{Operation: "AGENT_POINT_WITHDRAWAL", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 10_000, Scope: "CURRENT_ACCOUNT#TIER_1"},

			// §6.7 Q2 — NPSB individual vs corporate via tiers
			{Level: LevelService, LimitKey: LimitKey{Operation: "NPSB", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 1_000_000, Scope: "CURRENT_ACCOUNT"},
			{Level: LevelService, LimitKey: LimitKey{Operation: "NPSB", Metric: MetricCount, Period: PeriodDaily}, Ceiling: 10, Scope: "CURRENT_ACCOUNT"},
			{Level: LevelTier, LimitKey: LimitKey{Operation: "NPSB", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 100_000, Scope: "CURRENT_ACCOUNT#TIER_1"},
			{Level: LevelTier, LimitKey: LimitKey{Operation: "NPSB", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 200_000, Scope: "CURRENT_ACCOUNT#TIER_3"},

			// DPS savings is tighter
			{Level: LevelTier, LimitKey: LimitKey{Operation: "TRANSFER_OUT", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 10_000, Scope: "DPS_SAVINGS_ACCOUNT#TIER_1"},
		},
		Rules: []Rule{
			// §0 Day 30 — compliance blocks agent-point withdrawal for Tier 1 current accounts
			{Level: LevelTier, Kind: RuleKindPermission, Operation: "AGENT_POINT_WITHDRAWAL", Enabled: false, Scope: "CURRENT_ACCOUNT#TIER_1"},
			// §0 Day 45 — the failed exception: account-level ON cannot override a broader OFF
			{Level: LevelAccount, Kind: RuleKindPermission, Operation: "AGENT_POINT_WITHDRAWAL", Enabled: true, Scope: "ACC-001"},
		},
	}
}

func seedAccounts() map[string]Account {
	return map[string]Account{
		"ACC-001": {
			ID: "ACC-001", Product: "CURRENT_ACCOUNT", AccountType: AccountTypeGeneral, Tier: "TIER_1",
			Balance: 50_000, Config: map[string]string{"ALLOW_NEGATIVE_BALANCE": "NO", "ALLOW_BACKDATED_TRANSACTION": "NO"},
		},
		"ACC-002": {
			ID: "ACC-002", Product: "CURRENT_ACCOUNT", AccountType: AccountTypeGeneral, Tier: "TIER_2",
			Balance: 80_000, Config: map[string]string{"ALLOW_NEGATIVE_BALANCE": "NO"},
		},
		"ACC-003": {
			ID: "ACC-003", Product: "CURRENT_ACCOUNT", AccountType: AccountTypeSpecial, Tier: "DEFAULT_TIER",
			Balance: 0, Config: map[string]string{"ALLOW_NEGATIVE_BALANCE": "YES", "ALLOW_BACKDATED_TRANSACTION": "YES"},
		},
		"ACC-004": {
			ID: "ACC-004", Product: "DPS_SAVINGS_ACCOUNT", AccountType: AccountTypeGeneral, Tier: "TIER_1",
			Balance: 25_000, Config: map[string]string{"ALLOW_NEGATIVE_BALANCE": "NO"},
		},
		"ACC-005": {
			ID: "ACC-005", Product: "CURRENT_ACCOUNT", AccountType: AccountTypeGeneral, Tier: "TIER_3",
			Balance: 500_000, Config: map[string]string{"ALLOW_NEGATIVE_BALANCE": "NO"},
		},
	}
}

// ---- state accessors ----

func (s *State) Snapshot() (Config, map[string]Account) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.Config
	// copy into non-nil slices so empty collections marshal as [], not null
	cfg.Operations = make([]OperationDef, len(s.Config.Operations))
	copy(cfg.Operations, s.Config.Operations)
	cfg.Tiers = make([]string, len(s.Config.Tiers))
	copy(cfg.Tiers, s.Config.Tiers)
	cfg.Limits = make([]Limit, len(s.Config.Limits))
	copy(cfg.Limits, s.Config.Limits)
	cfg.Rules = make([]Rule, len(s.Config.Rules))
	copy(cfg.Rules, s.Config.Rules)
	products := map[string][]string{}
	for k, v := range s.Config.Products {
		p := make([]string, len(v))
		copy(p, v)
		products[k] = p
	}
	cfg.Products = products
	accounts := map[string]Account{}
	for k, v := range s.Accounts {
		if v.Config == nil {
			v.Config = map[string]string{}
		} else {
			v.Config = copyConfigMap(v.Config)
		}
		accounts[k] = v
	}
	return cfg, accounts
}

func copyConfigMap(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ReplaceConfig validates and swaps in a new config (§3.1: rules & limits may
// only reference operations from the master menu).
func (s *State) ReplaceConfig(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ops := map[string]bool{}
	for _, o := range cfg.Operations {
		if o.Name == "" {
			return fmt.Errorf("operation with empty name")
		}
		ops[o.Name] = true
	}
	for p, list := range cfg.Products {
		for _, op := range list {
			if !ops[op] {
				return fmt.Errorf("product %s references %s which is not in the Core Engine Service Configuration", p, op)
			}
		}
	}
	for _, l := range cfg.Limits {
		if !ops[l.Operation] {
			return fmt.Errorf("limit %s·%s·%s references unknown operation %s (§3.1)", l.Operation, l.Metric, l.Period, l.Operation)
		}
	}
	for _, r := range cfg.Rules {
		if !ops[r.Operation] {
			return fmt.Errorf("rule %s·%s references unknown operation %s (§3.1)", r.Kind, r.Operation, r.Operation)
		}
	}
	cfg.Products = cloneProducts(cfg.Products)
	s.Config = cfg
	return nil
}

func cloneProducts(m map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range m {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// UpsertAccount validates and stores an account. Used for onboarding and for
// tier moves. On tier change (§6.6.2) account-level limit overrides persist —
// they must be re-configured explicitly if the business wants them changed.
func (s *State) UpsertAccount(a Account) error {
	if a.ID == "" {
		return fmt.Errorf("account id required")
	}
	if a.AccountType != AccountTypeGeneral && a.AccountType != AccountTypeSpecial {
		return fmt.Errorf("account type must be GENERAL_ACCOUNT or SPECIAL_ACCOUNT (§5)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Config.Products[a.Product]; !ok {
		return fmt.Errorf("unknown product %q — pick from the product configuration (§4)", a.Product)
	}
	known := false
	for _, t := range s.Config.Tiers {
		if t == a.Tier {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unknown tier %q — pick from the tier list (§1)", a.Tier)
	}
	if a.Config == nil {
		a.Config = map[string]string{}
	}
	// §8: the Account Config Table is exclusively for SPECIAL accounts.
	if a.AccountType == AccountTypeGeneral {
		for k := range a.Config {
			if k == "ALLOW_NEGATIVE_BALANCE" || k == "ALLOW_BACKDATED_TRANSACTION" {
				delete(a.Config, k)
			}
		}
	}
	s.Accounts[a.ID] = a
	return nil
}

func (s *State) DeleteAccount(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Accounts, id)
	for k := range s.Usage {
		if k.AccountID == id {
			delete(s.Usage, k)
		}
	}
}

// ResetUsage clears consumption (a "new day" convenience).
func (s *State) ResetUsage(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if accountID == "" {
		s.Usage = map[usageKey]usageVal{}
		return
	}
	for k := range s.Usage {
		if k.AccountID == accountID {
			delete(s.Usage, k)
		}
	}
}

// WindowKey returns the calendar-window identifier for a period at time t
// (§6.4: calendar boundaries, never rolling).
func WindowKey(p Period, t time.Time) string {
	switch p {
	case PeriodDaily:
		return t.Format("2006-01-02")
	case PeriodWeekly:
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	case PeriodMonthly:
		return t.Format("2006-01")
	}
	return ""
}

// UsageSnapshot returns all usage rows for one account, sorted.
type UsageRow struct {
	Operation string `json:"operation"`
	Metric    Metric `json:"metric"`
	Window    string `json:"window"`
	Amount    int64  `json:"amount"`
	Count     int64  `json:"count"`
}

func (s *State) UsageSnapshot(accountID string) []UsageRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []UsageRow{}
	for k, v := range s.Usage {
		if accountID != "" && k.AccountID != accountID {
			continue
		}
		rows = append(rows, UsageRow{Operation: k.Operation, Metric: k.Metric, Window: k.Window, Amount: v.Amount, Count: v.Count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Operation != rows[j].Operation {
			return rows[i].Operation < rows[j].Operation
		}
		if rows[i].Window != rows[j].Window {
			return rows[i].Window < rows[j].Window
		}
		return rows[i].Metric < rows[j].Metric
	})
	return rows
}

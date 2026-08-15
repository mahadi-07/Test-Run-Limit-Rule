package engine

// Scenario is one guided walkthrough from the doc. Each scenario carries its
// own minimal World — only the operations, products, tiers, accounts and
// config that example needs — so the Configuration view shows nothing that
// isn't part of the story.
type Scenario struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	DocRef  string         `json:"docRef"`
	Summary string         `json:"summary"` // one line: what this teaches
	Story   string         `json:"story"`   // the narrative, doc words
	World   ScenarioWorld  `json:"world"`   // the minimal configuration to load
	Steps   []ScenarioStep `json:"steps"`   // config changes + transactions, in order
}

// ScenarioWorld is a complete, minimal simulator state for one example.
type ScenarioWorld struct {
	Operations []OperationDef        `json:"operations"`
	Products   map[string][]string   `json:"products"`
	Tiers      []string              `json:"tiers"`
	Accounts   []Account             `json:"accounts"`
	Limits     []Limit               `json:"limits"`
	Rules      []Rule                `json:"rules"`
}

type ScenarioStep struct {
	// exactly one is set
	Txn    *ScenarioTxn    `json:"txn,omitempty"`
	Action *ScenarioAction `json:"action,omitempty"`
}

type ScenarioTxn struct {
	AccountID string `json:"accountId"`
	Operation string `json:"operation"`
	Amount    int64  `json:"amount"`
	When      string `json:"when,omitempty"`
	Expect    string `json:"expect"`
	Why       string `json:"why"`
}

// ScenarioAction is a visible config change between transactions.
type ScenarioAction struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
	Level     string `json:"level,omitempty"`
	Operation string `json:"operation,omitempty"`
	Metric    string `json:"metric,omitempty"`
	Period    string `json:"period,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
	Ceiling   *int64 `json:"ceiling,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	Tier      string `json:"tier,omitempty"`
}

// LoadScenario installs a scenario's minimal world: blank slate first, so no
// configuration from any other example survives.
func (s *State) LoadScenario(w ScenarioWorld) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config = Config{
		Operations: append([]OperationDef{}, w.Operations...),
		Products:   cloneProducts(w.Products),
		Tiers:      append([]string{}, w.Tiers...),
		Limits:     append([]Limit{}, w.Limits...),
		Rules:      append([]Rule{}, w.Rules...),
	}
	s.Accounts = map[string]Account{}
	for _, a := range w.Accounts {
		s.Accounts[a.ID] = a
	}
	s.Usage = map[usageKey]usageVal{}
	s.Activity = []ActivityEntry{}
}

// Scenarios returns the doc's walkthroughs ordered as a tutorial.
func Scenarios() []Scenario {
	out := scenarioList()
	for i := range out {
		// marshal empty collections as [] / entries, never null
		if out[i].World.Operations == nil {
			out[i].World.Operations = []OperationDef{}
		}
		if out[i].World.Products == nil {
			out[i].World.Products = map[string][]string{}
		}
		if out[i].World.Tiers == nil {
			out[i].World.Tiers = []string{}
		}
		if out[i].World.Accounts == nil {
			out[i].World.Accounts = []Account{}
		}
		if out[i].World.Limits == nil {
			out[i].World.Limits = []Limit{}
		}
		if out[i].World.Rules == nil {
			out[i].World.Rules = []Rule{}
		}
	}
	return out
}

func scenarioList() []Scenario {
	t := func(accountID, operation string, amount int64, expect, why string) ScenarioStep {
		return ScenarioStep{Txn: &ScenarioTxn{AccountID: accountID, Operation: operation, Amount: amount, Expect: expect, Why: why}}
	}
	tw := func(accountID, operation string, amount int64, when, expect, why string) ScenarioStep {
		return ScenarioStep{Txn: &ScenarioTxn{AccountID: accountID, Operation: operation, Amount: amount, When: when, Expect: expect, Why: why}}
	}
	perm := func(level, scope, operation string, enabled bool, label, detail string) ScenarioStep {
		return ScenarioStep{Action: &ScenarioAction{Kind: "permission", Level: level, Scope: scope, Operation: operation, Enabled: &enabled, Label: label, Detail: detail}}
	}
	lim := func(level, scope, operation, metric, period string, ceiling int64, label, detail string) ScenarioStep {
		return ScenarioStep{Action: &ScenarioAction{Kind: "limit", Level: level, Scope: scope, Operation: operation, Metric: metric, Period: period, Ceiling: &ceiling, Label: label, Detail: detail}}
	}

	// shared, tiny world pieces examples can compose
	debit := OperationDef{Name: "TRANSFER_OUT", Direction: "DEBIT", Description: "Money leaving the account"}
	credit := OperationDef{Name: "TRANSFER_IN", Direction: "CREDIT", Description: "Money coming into the account"}
	acct := func(id string, balance int64) Account {
		return Account{ID: id, Product: "CURRENT_ACCOUNT", AccountType: AccountTypeGeneral, Tier: "TIER_1", Balance: balance}
	}

	return []Scenario{
		{
			ID: "start-here", Title: "Start here — the check order, in four gates", DocRef: "§1.2, §9",
			Summary: "Every transaction is checked against four things, in order. The first \"no\" wins.",
			Story: "A transaction must survive all four gates, in this order: (1) Core Engine Service Configuration — does the engine even offer this operation? (2) Product Configuration — does this product offer it? (3) Rules — is it allowed at every level? (4) Limits — is the amount within the number? Watch one transaction pass all four, then two that each stop at a different gate.",
			World: ScenarioWorld{
				Operations: []OperationDef{debit, credit},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"TRANSFER_IN", "TRANSFER_OUT"}, "DPS_SAVINGS_ACCOUNT": {"TRANSFER_IN"}},
				Tiers:      []string{"TIER_1"},
				Accounts:   []Account{acct("ACC-001", 20000)},
				Limits:     []Limit{},
				Rules:      []Rule{},
			},
			Steps: []ScenarioStep{
				t("ACC-001", "TRANSFER_OUT", 5000, "ALLOWED",
					"All four gates said yes: the engine offers TRANSFER_OUT, CURRENT_ACCOUNT offers it, every permission switch is ON (the default when no rule row exists), and no limit is configured at all — a missing limit means no restriction (§6.3 note)."),
				t("ACC-001", "TRANSFER_IN", 3000, "ALLOWED",
					"Both products offer TRANSFER_IN. Again all gates pass — the money moves in."),
				t("ACC-001", "CREDIT_CARD_BILL_PAYMENT", 100, "BLOCKED",
					"CREDIT_CARD_BILL_PAYMENT is not in the Core Engine Service Configuration — the master menu of everything the platform can do. Gate 1 fails; a Rule or Limit cannot even reference an operation that isn't on this menu (§3.1). Nothing else is consulted."),
			},
		},
		{
			ID: "product-menu", Title: "The product menu decides what's even possible", DocRef: "§4",
			Summary: "If a product never included an operation, no account of that type can ever perform it. There's nothing to switch on.",
			Story: "Two products, one operation difference: CURRENT_ACCOUNT offers TRANSFER_OUT, DPS_SAVINGS_ACCOUNT does not. Same operation, same amount, same tier — different outcomes, decided entirely at gate 2.",
			World: ScenarioWorld{
				Operations: []OperationDef{debit, credit},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"TRANSFER_IN", "TRANSFER_OUT"}, "DPS_SAVINGS_ACCOUNT": {"TRANSFER_IN"}},
				Tiers:      []string{"TIER_1"},
				Accounts: []Account{
					acct("ACC-001", 20000),
					{ID: "ACC-002", Product: "DPS_SAVINGS_ACCOUNT", AccountType: AccountTypeGeneral, Tier: "TIER_1", Balance: 20000},
				},
			},
			Steps: []ScenarioStep{
				t("ACC-001", "TRANSFER_OUT", 5000, "ALLOWED",
					"CURRENT_ACCOUNT includes TRANSFER_OUT in its product configuration. Gates 3 and 4 have nothing to say (no rules, no limits) — allowed."),
				t("ACC-002", "TRANSFER_OUT", 5000, "BLOCKED",
					"DPS_SAVINGS_ACCOUNT does not include TRANSFER_OUT — it was never on that product's menu. This fails gate 2; the rules and limits are never even consulted. No switch anywhere can turn this on: the operation would have to be added to the product itself."),
				t("ACC-002", "TRANSFER_IN", 5000, "ALLOWED",
					"TRANSFER_IN is on the DPS menu — the same account, one operation later, sails through. The menu is per-operation, not per-account."),
			},
		},
		{
			ID: "aisha-story", Title: "Aisha — a broader OFF always wins", DocRef: "§0, §7.2",
			Summary: "An account-level ON can never override a tier-level OFF. To let Aisha withdraw, the bank must lift the block at the tier.",
			Story: "Aisha's account: CURRENT_ACCOUNT # GENERAL_ACCOUNT # TIER_1. Compliance switches AGENT_POINT_WITHDRAWAL OFF for every Tier-1 current account (one switch, everyone blocked). A manager tries to grant it back to Aisha alone — the account-level ON is already set, and it fails. Day 60: the tier block is lifted, and the per-transaction limit takes over.",
			World: ScenarioWorld{
				Operations: []OperationDef{debit, {Name: "AGENT_POINT_WITHDRAWAL", Direction: "DEBIT", Description: "Cash withdrawal at agent point"}},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"TRANSFER_OUT", "AGENT_POINT_WITHDRAWAL"}},
				Tiers:      []string{"TIER_1"},
				Accounts:   []Account{acct("ACC-001", 20000)},
				Limits: []Limit{
					{Level: LevelTier, LimitKey: LimitKey{Operation: "AGENT_POINT_WITHDRAWAL", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 10000, Scope: "CURRENT_ACCOUNT#TIER_1"},
				},
				Rules: []Rule{
					// Day 30: the tier block
					{Level: LevelTier, Kind: RuleKindPermission, Operation: "AGENT_POINT_WITHDRAWAL", Enabled: false, Scope: "CURRENT_ACCOUNT#TIER_1"},
					// Day 45: the failed exception (already in place — and it does nothing)
					{Level: LevelAccount, Kind: RuleKindPermission, Operation: "AGENT_POINT_WITHDRAWAL", Enabled: true, Scope: "ACC-001"},
				},
			},
			Steps: []ScenarioStep{
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 5000, "BLOCKED",
					"Day 30–45. Permission rules resolve top-down: SERVICE ON → TIER OFF — stop. Aisha's account-level ON (visible in the status panel) is never reached, because a narrower ON can never reopen a gate a broader level has shut (§7.4.1)."),
				perm("TIER", "CURRENT_ACCOUNT#TIER_1", "AGENT_POINT_WITHDRAWAL", true,
					"Day 60 — the bank lifts the block at the tier level",
					"The only fix: flip the tier switch back ON (or move Aisha to a tier where it's already ON). Watch the status panel flip to allowed the moment this applies."),
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 12000, "BLOCKED",
					"The operation is permitted now — but limits are a separate question. 12,000 in one go exceeds the tier's per-transaction ceiling of 10,000."),
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 9000, "ALLOWED",
					"9,000 is within the 10,000 per-transaction ceiling, and her 20,000 balance covers it. All gates pass."),
			},
		},
		{
			ID: "limits-basics", Title: "Limits — the numbers, and how they're checked", DocRef: "§6.2, §6.5",
			Summary: "A limit is a cap: operation · metric · period · ceiling. The transaction must pass every one that applies.",
			Story: "Deposits (TRANSFER_IN) for Tier-1 current accounts carry two limits: at most 10,000 per transaction, and at most 2 transactions per day. Aisha deposits 9,000, then 9,000 again, then tries a small 1,000 top-up — watch which limit catches which attempt.",
			World: ScenarioWorld{
				Operations: []OperationDef{credit},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"TRANSFER_IN"}},
				Tiers:      []string{"TIER_1"},
				Accounts:   []Account{acct("ACC-001", 0)},
				Limits: []Limit{
					{Level: LevelTier, LimitKey: LimitKey{Operation: "TRANSFER_IN", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 10000, Scope: "CURRENT_ACCOUNT#TIER_1"},
					{Level: LevelTier, LimitKey: LimitKey{Operation: "TRANSFER_IN", Metric: MetricCount, Period: PeriodDaily}, Ceiling: 2, Scope: "CURRENT_ACCOUNT#TIER_1"},
				},
			},
			Steps: []ScenarioStep{
				t("ACC-001", "TRANSFER_IN", 12000, "BLOCKED",
					"A single deposit of 12,000 is over the per-transaction ceiling of 10,000 — the AMOUNT · PER_TXN limit. Usage is not consumed by a rejected transaction."),
				t("ACC-001", "TRANSFER_IN", 9000, "ALLOWED",
					"9,000 fits the per-txn ceiling. It's the 1st deposit today: count 1 of 2."),
				t("ACC-001", "TRANSFER_IN", 9000, "ALLOWED",
					"2nd deposit: count 2 of 2. The amount limits don't accumulate anything (no daily-amount limit exists in this world), so both pass."),
				t("ACC-001", "TRANSFER_IN", 1000, "BLOCKED",
					"Even a small 1,000 deposit is the 3rd transaction of the day — over the COUNT · DAILY limit of 2. The amount was fine; the count wasn't. Two different metrics, two independent checks."),
			},
		},
		{
			ID: "limit-precedence", Title: "Limit precedence — account overrides tier, service caps all", DocRef: "§6.3",
			Summary: "Start at the tier, apply the account override, then apply the service ceiling. The effective limit can never exceed the service number.",
			Story: "BFTN per-transaction amount for CURRENT_ACCOUNT · TIER_1: the tier sets 5,000. Aisha's account gets an 8,000 override — higher than the tier, allowed. Then a service-wide ceiling of 6,000 arrives. Effective for Aisha: min(8,000, 6,000) = 6,000. We watch the same 7,000 transfer get rejected, then allowed, then rejected again as each layer lands.",
			World: ScenarioWorld{
				Operations: []OperationDef{{Name: "BFTN", Direction: "DEBIT", Description: "Bangladesh Fund Transfer Network"}},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"BFTN"}},
				Tiers:      []string{"TIER_1"},
				Accounts:   []Account{acct("ACC-001", 20000)},
				Limits: []Limit{
					{Level: LevelTier, LimitKey: LimitKey{Operation: "BFTN", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 5000, Scope: "CURRENT_ACCOUNT#TIER_1"},
				},
			},
			Steps: []ScenarioStep{
				t("ACC-001", "BFTN", 4500, "ALLOWED",
					"4,500 is within the tier's 5,000. Right now the effective limit IS the tier number — the status panel shows TIER · 5,000."),
				t("ACC-001", "BFTN", 7000, "BLOCKED",
					"7,000 exceeds the tier's 5,000 per-transaction limit. Still no override — the tier number is the wall."),
				lim("ACCOUNT", "ACC-001", "BFTN", "AMOUNT", "PER_TXN", 8000,
					"Account level: BFTN · AMOUNT · PER_TXN = 8,000 (override for ACC-001)",
					"An Account-Level override can set a limit higher OR lower than the tier's — it replaces the tier's 5,000 for this account only (§6.3 note)."),
				t("ACC-001", "BFTN", 7000, "ALLOWED",
					"The same 7,000 that was just rejected — now allowed. The override replaced 5,000 with 8,000. The status panel now shows ACCOUNT · 8,000."),
				lim("SERVICE", "CURRENT_ACCOUNT", "BFTN", "AMOUNT", "PER_TXN", 6000,
					"Service level: BFTN · AMOUNT · PER_TXN = 6,000 (the ceiling arrives)",
					"The service-wide ceiling. It applies to every account of this product, regardless of tier or overrides (§6.3)."),
				t("ACC-001", "BFTN", 7000, "BLOCKED",
					"The override still says 8,000, but the effective limit is now min(8,000, 6,000) = 6,000. An account cannot override the service level (§6.3.1)."),
				t("ACC-001", "BFTN", 5500, "ALLOWED",
					"5,500 fits under the 6,000 ceiling. Full resolution for this account: tier 5,000 → overridden to 8,000 → capped at 6,000."),
			},
		},
		{
			ID: "enforcement-switch", Title: "The enforcement switch — the same number, asleep or awake", DocRef: "§7.3, §7.4.2",
			Summary: "A rule decides whether to check a limit; the limit is the number to check against. OFF makes just that check dormant.",
			Story: "Aisha may withdraw (permission ON). Her tier defines a daily amount limit of 10,000 on withdrawals. Morning: 8,000 passes. Afternoon: 5,000 would make 13,000 for the day — blocked. Then the tier's enforcement switch for that one limit is set OFF. The 10,000 stays configured, but nobody reads it — and the same 5,000 passes.",
			World: ScenarioWorld{
				Operations: []OperationDef{{Name: "AGENT_POINT_WITHDRAWAL", Direction: "DEBIT", Description: "Cash withdrawal at agent point"}},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"AGENT_POINT_WITHDRAWAL"}},
				Tiers:      []string{"TIER_1"},
				Accounts:   []Account{acct("ACC-001", 20000)},
				Limits: []Limit{
					{Level: LevelTier, LimitKey: LimitKey{Operation: "AGENT_POINT_WITHDRAWAL", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 10000, Scope: "CURRENT_ACCOUNT#TIER_1"},
				},
			},
			Steps: []ScenarioStep{
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 8000, "ALLOWED",
					"Morning: 8,000 is within the 10,000 daily ceiling. Usage for the day is now 8,000 — the enforcement switch is ON (default: no row = checked)."),
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 5000, "BLOCKED",
					"Afternoon: 8,000 + 5,000 = 13,000 for the day — over the 10,000 daily limit, which is currently being enforced."),
				{Action: &ScenarioAction{Kind: "enforcement", Level: "TIER", Scope: "CURRENT_ACCOUNT#TIER_1",
					Operation: "AGENT_POINT_WITHDRAWAL", Metric: "AMOUNT", Period: "DAILY", Enabled: boolPtr(false),
					Label: "Tier enforcement for AGENT_POINT_WITHDRAWAL · AMOUNT · DAILY → OFF",
					Detail: "The 10,000 limit stays configured, but the tier no longer checks it. This is per level: no other level's checks are affected (§7.3). Watch the status panel — the limit flips to dormant."}},
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 5000, "ALLOWED",
					"Same 5,000, same 13,000-for-the-day — but the daily limit is dormant now, so nobody compares against it. The number lives in the limits table; the switch decides whether it is read (§7.4.2)."),
			},
		},
		{
			ID: "calendar-weeks", Title: "Calendar windows — stuck until the calendar flips", DocRef: "§6.4",
			Summary: "Limits reset on calendar boundaries (day, ISO week, month) — not on rolling windows.",
			Story: "A weekly limit of 5,000 on BFTN for Tier-1 current accounts. Aisha exhausts it on Monday 10 Aug 2026 (week 33). On Wednesday she's still stuck — the window hasn't moved. On Monday 17 Aug (week 34) the window is fresh. Each step carries its own simulated date.",
			World: ScenarioWorld{
				Operations: []OperationDef{{Name: "BFTN", Direction: "DEBIT", Description: "Bangladesh Fund Transfer Network"}},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"BFTN"}},
				Tiers:      []string{"TIER_1"},
				Accounts:   []Account{acct("ACC-001", 20000)},
				Limits: []Limit{
					{Level: LevelTier, LimitKey: LimitKey{Operation: "BFTN", Metric: MetricAmount, Period: PeriodWeekly}, Ceiling: 5000, Scope: "CURRENT_ACCOUNT#TIER_1"},
				},
			},
			Steps: []ScenarioStep{
				tw("ACC-001", "BFTN", 5000, "2026-08-10", "ALLOWED",
					"Monday 10 Aug, week 33: the whole 5,000 weekly limit is used up in one transfer."),
				tw("ACC-001", "BFTN", 1, "2026-08-12", "BLOCKED",
					"Wednesday 12 Aug, still week 33: even 1 BDT is over 5,000 used. She's stuck until the calendar week begins — not 7 days after her last transaction."),
				tw("ACC-001", "BFTN", 5000, "2026-08-17", "ALLOWED",
					"Monday 17 Aug, week 34: a new calendar window opens and the full 5,000 is available again."),
			},
		},
		{
			ID: "mandate-cap", Title: "A central-bank mandate — the service ceiling caps everyone", DocRef: "§6.7 Q1",
			Summary: "Bangladesh Bank says no account may transfer out more than 10,000 a day. Set it once at the service level; nothing below can lift it.",
			Story: "To impose a platform-wide mandate, set a service-level limit: TRANSFER_OUT · AMOUNT · DAILY = 10,000. Even with a 50,000 account-level override in place for Aisha, the effective limit cannot exceed 10,000 — the override is silently capped.",
			World: ScenarioWorld{
				Operations: []OperationDef{debit},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"TRANSFER_OUT"}},
				Tiers:      []string{"TIER_1"},
				Accounts:   []Account{acct("ACC-001", 20000)},
				Limits: []Limit{
					{Level: LevelService, LimitKey: LimitKey{Operation: "TRANSFER_OUT", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 10000, Scope: "CURRENT_ACCOUNT"},
					{Level: LevelAccount, LimitKey: LimitKey{Operation: "TRANSFER_OUT", Metric: MetricAmount, Period: PeriodDaily}, Ceiling: 50000, Scope: "ACC-001"},
				},
			},
			Steps: []ScenarioStep{
				t("ACC-001", "TRANSFER_OUT", 6000, "ALLOWED",
					"First 6,000 of the day: within the effective limit of min(50,000 override, 10,000 mandate) = 10,000."),
				t("ACC-001", "TRANSFER_OUT", 6000, "BLOCKED",
					"6,000 + 6,000 = 12,000 for the day — over the 10,000 service ceiling. The 50,000 override changes nothing: an account cannot override the service level (§6.3.1). This is exactly how the Bangladesh Bank mandate from §6.7 Q1 is enforced."),
			},
		},
		{
			ID: "special-account", Title: "Special accounts — going negative on purpose", DocRef: "§5, §8",
			Summary: "SPECIAL accounts consult the Account Config Table: ALLOW_NEGATIVE_BALANCE=YES lets the balance go below zero.",
			Story: "ACC-002 is a SPECIAL account (think: a GL account) with ALLOW_NEGATIVE_BALANCE=YES in its Account Config Table. It transfers out 5,000 with a zero balance — allowed, balance goes to −5,000. The same request on Aisha's GENERAL account is refused: insufficient funds. The Account Config Table stores account-specific attributes that are neither Rules nor Limits (§8).",
			World: ScenarioWorld{
				Operations: []OperationDef{debit},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"TRANSFER_OUT"}},
				Tiers:      []string{"TIER_1"},
				Accounts: []Account{
					acct("ACC-001", 5000),
					{ID: "ACC-002", Product: "CURRENT_ACCOUNT", AccountType: AccountTypeSpecial, Tier: "TIER_1", Balance: 0,
						Config: map[string]string{"ALLOW_NEGATIVE_BALANCE": "YES"}},
				},
			},
			Steps: []ScenarioStep{
				t("ACC-002", "TRANSFER_OUT", 5000, "ALLOWED",
					"Balance is 0 and this is a DEBIT of 5,000 — but the account is SPECIAL with ALLOW_NEGATIVE_BALANCE=YES, so the balance gate lets it through. New balance: −5,000. Notice there are no limits or rules in this world at all — only the balance gate can speak."),
				t("ACC-001", "TRANSFER_OUT", 6000, "BLOCKED",
					"Aisha's account is GENERAL — no negative balance allowed. She holds 5,000, so a 6,000 transfer fails on insufficient funds. Same product, same tier, same operation — the Account Type is the only difference."),
			},
		},
		{
			ID: "tier-move", Title: "Moving tiers — limits re-base, overrides stay", DocRef: "§6.6.2",
			Summary: "An account that moves tier uses the new tier's limits — unless an account-level override exists for that exact combination.",
			Story: "ACC-001 sits in TIER_1, where NPSB per-transaction is 5,000. A 4,000 transfer passes. The account then moves to TIER_2 (per-transaction 2,000): the same transfer is now blocked. If the business still wants 4,000 for this account, an override must be configured for the new tier — the old tier's numbers are gone.",
			World: ScenarioWorld{
				Operations: []OperationDef{{Name: "NPSB", Direction: "DEBIT", Description: "National Payment Switch Bangladesh"}},
				Products:   map[string][]string{"CURRENT_ACCOUNT": {"NPSB"}},
				Tiers:      []string{"TIER_1", "TIER_2"},
				Accounts:   []Account{acct("ACC-001", 20000)},
				Limits: []Limit{
					{Level: LevelTier, LimitKey: LimitKey{Operation: "NPSB", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 5000, Scope: "CURRENT_ACCOUNT#TIER_1"},
					{Level: LevelTier, LimitKey: LimitKey{Operation: "NPSB", Metric: MetricAmount, Period: PeriodPerTxn}, Ceiling: 2000, Scope: "CURRENT_ACCOUNT#TIER_2"},
				},
			},
			Steps: []ScenarioStep{
				t("ACC-001", "NPSB", 4000, "ALLOWED",
					"TIER_1's per-transaction ceiling for NPSB is 5,000. 4,000 passes."),
				{Action: &ScenarioAction{Kind: "tier-move", AccountID: "ACC-001", Tier: "TIER_2",
					Label: "Move ACC-001 from TIER_1 to TIER_2",
					Detail: "The account's identity tuple changes to CURRENT_ACCOUNT # GENERAL_ACCOUNT # TIER_2, and its limits re-base to the new tier (§6.6.2). Watch the status panel: TIER · 5,000 becomes TIER · 2,000."}},
				t("ACC-001", "NPSB", 4000, "BLOCKED",
					"Same 4,000, new tier: TIER_2's ceiling is 2,000. If the business still wants 4,000 for this one account, an account-level override must be configured again for the new tier — a tier change does not carry overrides over."),
			},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

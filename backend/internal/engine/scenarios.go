package engine

// Scenario is one guided walkthrough from the doc. Steps run in order; a step
// is either a config change the user applies (Action) or a transaction (Txn).
// The UI renders both as numbered steps so the story never depends on prose.
type Scenario struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	DocRef  string         `json:"docRef"`
	Summary string         `json:"summary"` // one line: what this teaches
	Story   string         `json:"story"`   // the narrative, doc words
	Setup   []ScenarioStep `json:"setup"`   // config pre-applied before step 1 (shown, not hidden)
	Steps   []ScenarioStep `json:"steps"`
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
	Why       string `json:"why"` // plain-language explanation of the outcome
}

// ScenarioAction is a visible config change between transactions.
type ScenarioAction struct {
	Kind   string `json:"kind"`   // permission | enforcement | limit | tier-move
	Label  string `json:"label"`  // what changes, in doc words
	Detail string `json:"detail"` // why the story needs it
	// payloads
	Level     string `json:"level,omitempty"`
	Operation string `json:"operation,omitempty"`
	Metric    string `json:"metric,omitempty"`
	Period    string `json:"period,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
	Ceiling   *int64 `json:"ceiling,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	Tier      string `json:"tier,omitempty"`
	AutoApply bool   `json:"autoApply"` // true = runner applies with the click
}

// Scenarios returns the doc's walkthroughs ordered as a tutorial: each one
// assumes only what earlier ones taught. The UI applies Setup automatically
// and shows it as "the world you start in" — nothing hidden.
func Scenarios() []Scenario {
	out := scenarioList()
	for i := range out {
		if out[i].Setup == nil {
			out[i].Setup = []ScenarioStep{} // marshal as [], never null
		}
	}
	return out
}

func scenarioList() []Scenario {
	t := func(accountID, operation string, amount int64, expect, why, when string) ScenarioStep {
		return ScenarioStep{Txn: &ScenarioTxn{AccountID: accountID, Operation: operation, Amount: amount, Expect: expect, Why: why, When: when}}
	}
	perm := func(level, scope, operation string, enabled bool, label, detail string) ScenarioStep {
		return ScenarioStep{Action: &ScenarioAction{Kind: "permission", Level: level, Scope: scope, Operation: operation, Enabled: &enabled, Label: label, Detail: detail}}
	}
	lim := func(level, scope, operation, metric, period string, ceiling int64, label, detail string) ScenarioStep {
		return ScenarioStep{Action: &ScenarioAction{Kind: "limit", Level: level, Scope: scope, Operation: operation, Metric: metric, Period: period, Ceiling: &ceiling, Label: label, Detail: detail}}
	}

	return []Scenario{
		{
			ID: "start-here", Title: "Start here — the check order, in four gates", DocRef: "§1.2, §9",
			Summary: "Every transaction is checked against four things, in order. The first \"no\" wins.",
			Story: "A transaction must survive all four gates, in this order: (1) Core Engine Service Configuration — does the engine even offer this operation? (2) Product Configuration — does this product offer it? (3) Rules — is it allowed at every level? (4) Limits — is the amount within the number? Watch one transaction sail through all four, then two that each die at a different gate.",
			Steps: []ScenarioStep{
				t("ACC-002", "TRANSFER_OUT", 5000, "ALLOWED",
					"All four gates said yes: the engine offers TRANSFER_OUT, CURRENT_ACCOUNT offers it, every permission switch is ON, and 5,000 is within every limit.", ""),
				t("ACC-004", "BFTN", 100, "BLOCKED",
					"BFTN is not in DPS_SAVINGS_ACCOUNT's product configuration — it was never on that product's menu. There is nothing to switch on; the operation simply isn't offered. This fails gate 2, so rules and limits are never even consulted.", ""),
				t("ACC-002", "CREDIT_CARD_BILL_PAYMENT", 100, "BLOCKED",
					"CREDIT_CARD_BILL_PAYMENT is not in the Core Engine Service Configuration — the master menu of everything the platform can do. Gate 1 fails; a Rule or Limit cannot reference an operation that isn't on this menu (§3.1).", ""),
			},
		},
		{
			ID: "aisha-story", Title: "Aisha's first 60 days — a broader OFF always wins", DocRef: "§0, §7.2",
			Summary: "An account-level ON can never override a tier-level OFF. To let Aisha withdraw, the bank must lift the block at the tier.",
			Story: "Aisha opens a Current Account and lands in TIER_1. Day 30: compliance switches AGENT_POINT_WITHDRAWAL OFF for every Tier-1 current account. Day 45: a manager tries to grant it back to Aisha alone — it fails. Day 60: the tier block is lifted, and instead the tier's per-transaction limit of 10,000 stops her 12,000 request.",
			Steps: []ScenarioStep{
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 5000, "BLOCKED",
					"Day 30. The tier switch is OFF. Permission rules resolve top-down: SERVICE ON → TIER OFF — stop. Aisha's own account switch says ON, but a narrower ON can never reopen a gate a broader level has shut (§7.4.1). Every Tier-1 customer is blocked by this one switch.", ""),
				perm("TIER", "CURRENT_ACCOUNT#TIER_1", "AGENT_POINT_WITHDRAWAL", true,
					"Day 60 — the bank lifts the block at the tier level",
					"The only way to let Aisha withdraw: flip the tier switch back ON (or move her to a tier where it's already ON). Granting her account alone can never work."),
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 12000, "BLOCKED",
					"The operation is permitted now, but limits are a separate question. 12,000 in one go exceeds the tier's per-transaction ceiling of 10,000. A 9,000 request would have gone through.", ""),
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 9000, "ALLOWED",
					"9,000 is within the 10,000 per-transaction ceiling, within the 10,000 daily ceiling, and her balance covers it. All gates pass.", ""),
			},
		},
		{
			ID: "transfer-in-limits", Title: "One operation, several limits at once", DocRef: "§6.2, §6.5",
			Summary: "Limits are compared only when operation + metric + period all match — and the transaction must pass every one of them.",
			Story: "TRANSFER_IN for Aisha's account carries: a service-level daily cap of 1,00,00,000, a tier per-transaction limit of 10,000, a tier daily-amount limit of 15,000, a tier daily count of 2, and an account-level override that raises the daily amount to 20,000. Each combination is checked independently.",
			Steps: []ScenarioStep{
				t("ACC-001", "TRANSFER_IN", 12000, "BLOCKED",
					"A single deposit of 12,000 is over the per-transaction ceiling of 10,000. The daily limits don't matter — the transaction dies at the first combination it breaches.", ""),
				t("ACC-001", "TRANSFER_IN", 9000, "ALLOWED",
					"9,000 is under the 10,000 per-txn ceiling. It's the 1st transaction today: count 1/2, daily amount 9,000 of the effective 20,000.", ""),
				t("ACC-001", "TRANSFER_IN", 9000, "ALLOWED",
					"2nd deposit: count 2/2, and 18,000 total is within the effective daily ceiling of 20,000 — the account-level override. Without that override the tier's 15,000 would have blocked it here.", ""),
				t("ACC-001", "TRANSFER_IN", 1000, "BLOCKED",
					"Even a small 1,000 deposit is the 3rd transaction of the day — over the daily count of 2. The amount was fine; the count wasn't.", ""),
			},
		},
		{
			ID: "limit-precedence", Title: "Limit precedence — account overrides tier, service caps all", DocRef: "§6.3",
			Summary: "Start at the tier, apply the account override, then apply the service ceiling. The effective limit can never exceed the service number.",
			Story: "BFTN per-transaction amount limits for CURRENT_ACCOUNT · TIER_1: the tier sets 5,000. Aisha's account gets an override of 8,000 — higher than the tier, allowed. Then a service-wide ceiling of 6,000 arrives. Effective for Aisha: min(8,000 override, 6,000 service) = 6,000 — the override raised her above the tier, but never past the service. We watch all three numbers through three transactions.",
			Setup: []ScenarioStep{
				lim("TIER", "CURRENT_ACCOUNT#TIER_1", "BFTN", "AMOUNT", "PER_TXN", 5000,
					"Tier level: BFTN · AMOUNT · PER_TXN = 5,000",
					"The starting point for every Tier-1 current account — no override exists yet, so this is everyone's effective limit."),
			},
			Steps: []ScenarioStep{
				t("ACC-001", "BFTN", 4500, "ALLOWED",
					"4,500 is within the tier's 5,000. At this moment the effective limit for ACC-001 is exactly the tier number — the status panel shows TIER · 5,000.", ""),
				t("ACC-001", "BFTN", 7000, "BLOCKED",
					"7,000 exceeds the tier's 5,000 per-transaction limit. Still no override — the tier number is the wall.", ""),
				lim("ACCOUNT", "ACC-001", "BFTN", "AMOUNT", "PER_TXN", 8000,
					"Account level: BFTN · AMOUNT · PER_TXN = 8,000 (override for ACC-001)",
					"An Account-Level override can set a limit higher OR lower than the tier's (§6.3 note) — it replaces the tier's 5,000 for this account only."),
				t("ACC-001", "BFTN", 7000, "ALLOWED",
					"Same 7,000 that was just rejected — now allowed. The override replaced the tier's 5,000 with 8,000, so the effective limit rose. The status panel now shows ACCOUNT · 8,000.", ""),
				lim("SERVICE", "CURRENT_ACCOUNT", "BFTN", "AMOUNT", "PER_TXN", 6000,
					"Service level: BFTN · AMOUNT · PER_TXN = 6,000 (the ceiling arrives)",
					"The service-wide ceiling. It applies to every account under CURRENT_ACCOUNT, regardless of tier or overrides (§6.3)."),
				t("ACC-001", "BFTN", 7000, "BLOCKED",
					"The override still says 8,000, but the effective limit is now min(8,000, 6,000) = 6,000. The service ceiling is a hard cap: an account cannot override the service level (§6.3.1). A 5,500 transfer would pass.", ""),
				t("ACC-001", "BFTN", 5500, "ALLOWED",
					"5,500 fits under the 6,000 service ceiling. Final resolution for this account: tier 5,000 → overridden to 8,000 → capped at 6,000.", ""),
			},
		},
		{
			ID: "enforcement-switch", Title: "The enforcement switch — the same number, asleep or awake", DocRef: "§7.3, §7.4.2",
			Summary: "A rule decides whether to check a limit; the limit is the number to check against. OFF makes the number dormant — other levels stay live.",
			Story: "Aisha is permitted to withdraw (tier ON). Her tier defines a daily amount limit of 10,000. She withdraws 8,000 in the morning; the afternoon's 5,000 breaches 10,000. Then the tier's enforcement switch for that one limit is set OFF — the 10,000 stays on the wall, but nobody reads it, and the same 5,000 passes. The switch sits next to its own limit: turning it off does not touch any other level's checks.",
			Setup: []ScenarioStep{
				perm("TIER", "CURRENT_ACCOUNT#TIER_1", "AGENT_POINT_WITHDRAWAL", true,
					"Tier permission for AGENT_POINT_WITHDRAWAL → ON",
					"Withdrawal is re-enabled for the tier (Day 60 of Aisha's story)."),
			},
			Steps: []ScenarioStep{
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 8000, "ALLOWED",
					"Morning: 8,000 within the 10,000 daily ceiling and 10,000 per-txn ceiling. Usage for the day is now 8,000.", ""),
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 5000, "BLOCKED",
					"Afternoon: 8,000 + 5,000 = 13,000 for the day — over the tier's 10,000 daily limit, which is being enforced.", ""),
				{Action: &ScenarioAction{Kind: "enforcement", Level: "TIER", Scope: "CURRENT_ACCOUNT#TIER_1",
					Operation: "AGENT_POINT_WITHDRAWAL", Metric: "AMOUNT", Period: "DAILY", Enabled: boolPtr(false),
					Label: "Tier enforcement for AGENT_POINT_WITHDRAWAL · AMOUNT · DAILY → OFF",
					Detail: "The 10,000 limit stays configured, but the tier no longer checks it. This is per level: the per-transaction limit and the service ceiling are untouched (§7.3).", AutoApply: false}},
				t("ACC-001", "AGENT_POINT_WITHDRAWAL", 5000, "ALLOWED",
					"Same 5,000, same 13,000-for-the-day — but the daily limit is dormant now, so nobody compares against it. The number lives in the limits table; the switch decides whether it is read (§7.4.2).", ""),
			},
		},
		{
			ID: "calendar-weeks", Title: "Calendar windows — stuck until the calendar flips", DocRef: "§6.4",
			Summary: "Limits reset on calendar boundaries (day, ISO week, month) — not on rolling windows.",
			Story: "A weekly limit of 5,000 on BFTN for Tier-1 current accounts. Aisha exhausts it on Monday 10 Aug 2026 (week 33). On Wednesday she's still stuck — the window hasn't moved. On Monday 17 Aug (week 34) the window is fresh. Each step carries its own simulated date.",
			Setup: []ScenarioStep{
				lim("TIER", "CURRENT_ACCOUNT#TIER_1", "BFTN", "AMOUNT", "WEEKLY", 5000,
					"Tier level: BFTN · AMOUNT · WEEKLY = 5,000",
					"A weekly limit tied to the calendar week number (12 Aug 2026 → week 33, 19 Aug → week 34)."),
			},
			Steps: []ScenarioStep{
				t("ACC-001", "BFTN", 5000, "ALLOWED",
					"Monday 10 Aug, week 33: the whole 5,000 weekly limit is used up.", "2026-08-10"),
				t("ACC-001", "BFTN", 1, "BLOCKED",
					"Wednesday 12 Aug, still week 33: even 1 BDT is blocked. She's stuck until the calendar week begins — not 7 days after her last transaction.", "2026-08-12"),
				t("ACC-001", "BFTN", 5000, "ALLOWED",
					"Monday 17 Aug, week 34: a new calendar window opens and the full 5,000 is available again.", "2026-08-17"),
			},
		},
		{
			ID: "mandate-cap", Title: "A central-bank mandate — the service ceiling caps everyone", DocRef: "§6.7 Q1",
			Summary: "Bangladesh Bank says no account may transfer out more than 10,000 a day. Set it once at the service level; nothing below can lift it.",
			Story: "To impose a platform-wide mandate, set a service-level limit: TRANSFER_OUT · AMOUNT · DAILY = 10,000. Even with a 50,000 account-level override in place, the effective limit cannot exceed 10,000 — the override is silently capped.",
			Setup: []ScenarioStep{
				lim("SERVICE", "CURRENT_ACCOUNT", "TRANSFER_OUT", "AMOUNT", "DAILY", 10000,
					"Service level: TRANSFER_OUT · AMOUNT · DAILY = 10,000",
					"The mandate. Service-level limits are always enforced and act as a hard cap (§6.3)."),
				lim("ACCOUNT", "ACC-001", "TRANSFER_OUT", "AMOUNT", "DAILY", 50000,
					"Account level: TRANSFER_OUT · AMOUNT · DAILY = 50,000 (override)",
					"An account override trying to raise the ceiling — it will be capped at the service number."),
			},
			Steps: []ScenarioStep{
				t("ACC-001", "TRANSFER_OUT", 6000, "ALLOWED",
					"First 6,000 of the day: within the effective 10,000.", ""),
				t("ACC-001", "TRANSFER_OUT", 6000, "BLOCKED",
					"6,000 + 6,000 = 12,000 for the day — over the service ceiling. The 50,000 override changes nothing here: an account cannot override the service level (§6.3.1).", ""),
			},
		},
		{
			ID: "special-account", Title: "Special accounts — going negative on purpose", DocRef: "§5, §8",
			Summary: "SPECIAL accounts consult the Account Config Table: ALLOW_NEGATIVE_BALANCE=YES lets the balance go below zero.",
			Story: "ACC-003 is a SPECIAL account (think: a GL account) with ALLOW_NEGATIVE_BALANCE=YES in its Account Config Table. It transfers out 5,000 with a zero balance — allowed, balance goes to −5,000. The same request on a general account is refused: insufficient funds. The Account Config Table stores account-specific attributes that are neither Rules nor Limits (§8).",
			Steps: []ScenarioStep{
				t("ACC-003", "RTGS", 5000, "ALLOWED",
					"Balance is 0 and this is a DEBIT of 5,000 — but the account is SPECIAL with ALLOW_NEGATIVE_BALANCE=YES, so the balance gate lets it through. New balance: −5,000.", ""),
				t("ACC-001", "RTGS", 60000, "BLOCKED",
					"Aisha's account is GENERAL — no negative balance. She holds 50,000 and RTGS has no limits configured, so the only thing stopping a 60,000 transfer is the money itself: insufficient funds.", ""),
			},
		},
		{
			ID: "tier-move", Title: "Moving tiers — limits re-base, overrides stay", DocRef: "§6.6.2",
			Summary: "An account that moves tier uses the new tier's limits — unless an account-level override exists for that exact combination.",
			Story: "ACC-005 sits in TIER_3, where NPSB per-transaction is 200,000 (the doc's corporate tier). A 150,000 transfer passes. The account then moves to TIER_1 (per-transaction 100,000): the same transfer is now blocked. Existing account-level overrides would survive the move (§6.8.1) — but here there are none, so the new tier's numbers apply cleanly.",
			Steps: []ScenarioStep{
				t("ACC-005", "NPSB", 150000, "ALLOWED",
					"TIER_3's per-transaction ceiling for NPSB is 200,000 (the corporate tier, §6.7 Q2). 150,000 passes.", ""),
				{Action: &ScenarioAction{Kind: "tier-move", AccountID: "ACC-005", Tier: "TIER_1",
					Label: "Move ACC-005 from TIER_3 to TIER_1",
					Detail: "The account's identity tuple changes to CURRENT_ACCOUNT # GENERAL_ACCOUNT # TIER_1. Its limits re-base to the new tier (§6.6.2). An existing account-level override would persist and need reconfiguring — there is none here.", AutoApply: false}},
				t("ACC-005", "NPSB", 150000, "BLOCKED",
					"Same 150,000, new tier: TIER_1's per-transaction ceiling is 100,000. If the business still wants 150,000 for this account, an account-level override must be configured again for the new tier.", ""),
			},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

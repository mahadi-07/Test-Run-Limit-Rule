package engine

// Scenario is one clickable walkthrough from the doc. Apply mutates the world
// into the scenario's starting state; the UI then prompts the user to fire
// the suggested transactions.
type Scenario struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	DocRef      string        `json:"docRef"`
	Story       string        `json:"story"`
	Txns        []ScenarioTxn `json:"txns"` // suggested transactions in order
}

type ScenarioTxn struct {
	AccountID string `json:"accountId"`
	Operation string `json:"operation"`
	Amount    int64  `json:"amount"`
	Expect    string `json:"expect"` // ALLOWED | BLOCKED
	Comment   string `json:"comment"`
}

// Scenarios returns the doc's walkthroughs. Pure data; the UI applies them by
// resetting the world and (for scenarios that need config changes) calling the
// normal config endpoints.
func Scenarios() []Scenario {
	return []Scenario{
		{
			ID: "aisha-story", Title: "Aisha's first 60 days (§0)", DocRef: "§0",
			Story: "Aisha opens a Current Account (Tier 1). Day 30: compliance switches AGENT_POINT_WITHDRAWAL OFF for all Tier 1 current accounts. Day 45: an account-level ON fails to override. Day 60: the tier is re-enabled, but the per-transaction cap of 10,000 stops her 12,000 withdrawal.",
			Txns: []ScenarioTxn{
				{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 5000, Expect: "BLOCKED", Comment: "Day 30–45: tier OFF blocks; account ON is never reached (§7.4.1). After this, flip the TIER switch back ON in Rules to see Day 60."},
				{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 12000, Expect: "BLOCKED", Comment: "Day 60 (tier re-enabled): over the 10,000 per-txn cap"},
				{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 9000, Expect: "ALLOWED", Comment: "Day 60: 9,000 would have gone through"},
			},
		},
		{
			ID: "transfer-in-limits", Title: "TRANSFER_IN effective limits (§6.5)", DocRef: "§6.5",
			Story: "The worked example: a service cap of 1,00,00,000, tier per-txn 10,000, tier daily 15,000, tier daily count 2, and an account override raising daily amount to 20,000. Watch which limit catches which transaction.",
			Txns: []ScenarioTxn{
				{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 12000, Expect: "BLOCKED", Comment: "single deposit over the per-txn 10,000"},
				{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 9000, Expect: "ALLOWED", Comment: "1st deposit of the day"},
				{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 9000, Expect: "ALLOWED", Comment: "2nd deposit: 18,000 total ≤ 20,000 override, count 2/2"},
				{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 1000, Expect: "BLOCKED", Comment: "3rd deposit: daily count 3 > 2"},
			},
		},
		{
			ID: "enforcement-switch", Title: "Enforcement switch: same number, dormant (§7.4.2)", DocRef: "§7.4.2",
			Story: "Aisha withdraws 8,000 in the morning; the afternoon 5,000 breaches the 10,000 daily cap. Then flip the tier enforcement switch OFF for AGENT_POINT_WITHDRAWAL·AMOUNT·DAILY — the same 5,000 passes because the limit is dormant.",
			Txns: []ScenarioTxn{
				{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 8000, Expect: "ALLOWED", Comment: "morning withdrawal (requires the tier permission ON — see Aisha story)"},
				{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 5000, Expect: "BLOCKED", Comment: "13,000 for the day > 10,000"},
				{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 5000, Expect: "ALLOWED", Comment: "same txn after the enforcement switch is set OFF — the number stays, nobody checks it"},
			},
		},
		{
			ID: "calendar-weeks", Title: "Calendar weeks, not rolling (§6.4)", DocRef: "§6.4",
			Story: "A weekly limit of 5,000 on BFTN for Tier 1. Exhaust it on Monday 10 Aug 2026 (week 33) — a 1 BDT transaction the same week is blocked, but the same amount on Monday 17 Aug (week 34) passes. Use the date field to hop weeks.",
			Txns: []ScenarioTxn{
				{AccountID: "ACC-001", Operation: "BFTN", Amount: 5000, Expect: "ALLOWED", Comment: "set the simulated date to 2026-08-10 — week 33 exhausted"},
				{AccountID: "ACC-001", Operation: "BFTN", Amount: 1, Expect: "BLOCKED", Comment: "same week: stuck until the calendar week flips"},
				{AccountID: "ACC-001", Operation: "BFTN", Amount: 5000, Expect: "ALLOWED", Comment: "set the date to 2026-08-17 — week 34, fresh window"},
			},
		},
		{
			ID: "mandate-cap", Title: "Bangladesh Bank mandate (§6.7 Q1)", DocRef: "§6.7",
			Story: "No account may transact more than 10,000/day over TRANSFER_OUT. Set the service-level daily amount limit to 10,000 and add a 50,000 account override — the override cannot lift the mandate.",
			Txns: []ScenarioTxn{
				{AccountID: "ACC-001", Operation: "TRANSFER_OUT", Amount: 6000, Expect: "ALLOWED", Comment: "after setting the service ceiling to 10,000 (Configuration → Limits)"},
				{AccountID: "ACC-001", Operation: "TRANSFER_OUT", Amount: 6000, Expect: "BLOCKED", Comment: "12,000 for the day — the service cap rejects even though the account override says 50,000"},
			},
		},
		{
			ID: "dps-product-menu", Title: "The product menu decides (§4)", DocRef: "§4",
			Story: "A DPS savings account cannot perform BFTN — it was never on the product's menu. There is nothing to switch on; the operation is unavailable at the Product Configuration gate.",
			Txns: []ScenarioTxn{
				{AccountID: "ACC-004", Operation: "BFTN", Amount: 100, Expect: "BLOCKED", Comment: "fails at gate 2: DPS_SAVINGS_ACCOUNT does not offer BFTN"},
				{AccountID: "ACC-004", Operation: "TRANSFER_IN", Amount: 5000, Expect: "ALLOWED", Comment: "TRANSFER_IN is on the DPS menu"},
			},
		},
		{
			ID: "special-account", Title: "Special accounts go negative (§5, §8)", DocRef: "§5, §8",
			Story: "ACC-003 is a SPECIAL account (a GL account) with ALLOW_NEGATIVE_BALANCE=YES in the Account Config Table. It can transfer out more than its balance; a general account cannot.",
			Txns: []ScenarioTxn{
				{AccountID: "ACC-003", Operation: "TRANSFER_OUT", Amount: 5000, Expect: "ALLOWED", Comment: "balance 0 → goes negative, permitted by §8 config"},
				{AccountID: "ACC-001", Operation: "TRANSFER_OUT", Amount: 60000, Expect: "BLOCKED", Comment: "general account with balance 50,000: insufficient funds"},
			},
		},
		{
			ID: "tier-move", Title: "Moving tiers re-bases limits (§6.6.2)", DocRef: "§6.6.2",
			Story: "ACC-005 sits in TIER_3 (NPSB per-txn 200,000). Move it to TIER_1 (per-txn 100,000) via Accounts → Move tier: the same 150,000 transaction that passed before is now blocked. Account-level overrides persist but tier limits re-base.",
			Txns: []ScenarioTxn{
				{AccountID: "ACC-005", Operation: "NPSB", Amount: 150000, Expect: "ALLOWED", Comment: "TIER_3 corporate per-txn 200,000 allows it"},
				{AccountID: "ACC-005", Operation: "NPSB", Amount: 150000, Expect: "BLOCKED", Comment: "after moving to TIER_1: per-txn 100,000 applies"},
			},
		},
	}
}

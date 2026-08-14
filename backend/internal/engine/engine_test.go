package engine

import (
	"testing"
	"time"
)

// fixed clock so calendar windows are deterministic
var t0 = time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC) // a Friday, week 33

func eval(s *State, t Txn, commit bool) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evaluateLocked(t, commit, t0)
}

func TestAishaDay30TierBlock(t *testing.T) {
	// §0 Day 30: CURRENT_ACCOUNT#TIER_1 AGENT_POINT_WITHDRAWAL is OFF at tier.
	// §0 Day 45: account-level ON cannot override the broader OFF.
	s := NewState()
	d := eval(s, Txn{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 5000}, false)
	if d.Allowed {
		t.Fatal("expected block: tier-level OFF must win over account-level ON")
	}
	if want := "TIER"; !contains(d.Reason, want) {
		t.Fatalf("reason should point at the TIER switch, got: %s", d.Reason)
	}
}

func TestAishaDay60PerTxnCap(t *testing.T) {
	// §0 Day 60: tier re-enabled (service ON, account ON, tier must be flipped).
	s := NewState()
	cfg, _ := s.Snapshot()
	for i := range cfg.Rules {
		if cfg.Rules[i].Kind == RuleKindPermission && cfg.Rules[i].Operation == "AGENT_POINT_WITHDRAWAL" &&
			cfg.Rules[i].Level == LevelTier {
			cfg.Rules[i].Enabled = true // "Withdrawal is re-enabled for her tier"
		}
	}
	if err := s.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}

	// 12,000 in one go → over the 10,000 per-txn cap → declined
	d := eval(s, Txn{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 12000}, false)
	if d.Allowed {
		t.Fatal("12,000 must be declined (per-txn cap 10,000)")
	}
	// 9,000 would go through
	d = eval(s, Txn{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 9000}, false)
	if !d.Allowed {
		t.Fatalf("9,000 must pass, got: %s", d.Reason)
	}
}

func TestDailyAmountAccumulationAndEnforcementSwitch(t *testing.T) {
	// §7.4.2: 8,000 in the morning + 5,000 afternoon = 13,000 > 10,000 daily cap.
	s := NewState()
	cfg, _ := s.Snapshot()
	for i := range cfg.Rules {
		if cfg.Rules[i].Kind == RuleKindPermission && cfg.Rules[i].Operation == "AGENT_POINT_WITHDRAWAL" &&
			cfg.Rules[i].Level == LevelTier {
			cfg.Rules[i].Enabled = true
		}
	}
	if err := s.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if d := eval(s, Txn{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 8000}, true); !d.Allowed {
		t.Fatalf("morning 8,000 should pass: %s", d.Reason)
	}
	d := eval(s, Txn{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 5000}, false)
	if d.Allowed {
		t.Fatal("afternoon 5,000 must be blocked: 13,000 > 10,000 daily")
	}

	// Now flip the tier enforcement switch OFF for that limit → the number is dormant.
	for i := range cfg.Rules {
		_ = i
	}
	cfg, _ = s.Snapshot()
	cfg.Rules = append(cfg.Rules, Rule{
		Level: LevelTier, Kind: RuleKindEnforcement, Enabled: false,
		Operation: "AGENT_POINT_WITHDRAWAL", Metric: MetricAmount, Period: PeriodDaily,
		Scope: "CURRENT_ACCOUNT#TIER_1",
	})
	// per-txn limit still live, so 5,000 (≤10,000 per txn) passes once the daily check is dormant
	if err := s.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	d = eval(s, Txn{AccountID: "ACC-001", Operation: "AGENT_POINT_WITHDRAWAL", Amount: 5000}, false)
	if !d.Allowed {
		t.Fatalf("with enforcement OFF the daily 10,000 must be ignored, got: %s", d.Reason)
	}
}

func TestLimitPrecedence(t *testing.T) {
	// §6.3: Service=700, Tier=500, Account=1000 → effective 700 (service is the cap).
	s := NewState()
	cfg, _ := s.Snapshot()
	key := LimitKey{Operation: "TRANSFER_OUT", Metric: MetricCount, Period: PeriodDaily}
	cfg.Limits = append(cfg.Limits,
		Limit{Level: LevelService, LimitKey: key, Ceiling: 700, Scope: "CURRENT_ACCOUNT"},
		Limit{Level: LevelTier, LimitKey: key, Ceiling: 500, Scope: "CURRENT_ACCOUNT#TIER_1"},
		Limit{Level: LevelAccount, LimitKey: key, Ceiling: 1000, Scope: "ACC-002"},
	)
	if err := s.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// ACC-002 is TIER_2; give it an account override too
	acc, _ := s.Snapshot()
	_ = acc
	a := Account{ID: "ACC-002", Product: "CURRENT_ACCOUNT", AccountType: AccountTypeGeneral, Tier: "TIER_2", Balance: 1_000_000}
	_ = s.UpsertAccount(a)
	// ACC-002's tier (TIER_2) has no limit; account=1000, service=700 → effective 700
	for i := 0; i < 700; i++ {
		if d := eval(s, Txn{AccountID: "ACC-002", Operation: "TRANSFER_OUT", Amount: 1}, true); !d.Allowed {
			t.Fatalf("txn %d should pass: %s", i+1, d.Reason)
		}
	}
	d := eval(s, Txn{AccountID: "ACC-002", Operation: "TRANSFER_OUT", Amount: 1}, false)
	if d.Allowed {
		t.Fatal("txn 701 must fail: service ceiling 700 caps the account override of 1000")
	}
}

func TestAccountOverrideReplacesTierPerMetricPeriod(t *testing.T) {
	// §6.5: TRANSFER_IN — account daily-amount 20,000 overrides tier 15,000;
	// per-txn 10,000 and daily-count 2 still come from the tier.
	s := NewState()

	// single 12,000 deposit → over per-txn 10,000 → blocked
	if d := eval(s, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 12000}, false); d.Allowed {
		t.Fatal("12,000 single deposit must be blocked by per-txn 10,000")
	}
	// two deposits of 9,000 → passes per-txn; total 18,000 < 20,000 account daily; count 2/2 ok
	if d := eval(s, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 9000}, true); !d.Allowed {
		t.Fatalf("first 9,000 should pass: %s", d.Reason)
	}
	if d := eval(s, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 9000}, true); !d.Allowed {
		t.Fatalf("second 9,000 should pass (18,000 ≤ 20,000, count 2 ≤ 2): %s", d.Reason)
	}
	// third deposit → daily count 3 > 2 → blocked
	if d := eval(s, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 1000}, false); d.Allowed {
		t.Fatal("third deposit must be blocked by daily count 2")
	}

	// fresh state: the account override raised the daily ceiling from tier's
	// 15,000 to 20,000 (§6.5). Two deposits of 8,000 = 16,000: over the tier
	// number, under the account override → allowed with the override, blocked without.
	s2 := NewState()
	if d := eval(s2, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 8000}, true); !d.Allowed {
		t.Fatalf("first 8,000 should pass: %s", d.Reason)
	}
	if d := eval(s2, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 8000}, true); !d.Allowed {
		t.Fatalf("16,000 total ≤ account override 20,000 should pass: %s", d.Reason)
	}
	// remove the override (§6.6.1) → the tier's 15,000 becomes effective again
	s3 := NewState()
	cfg, _ := s3.Snapshot()
	kept := cfg.Limits[:0]
	for _, l := range cfg.Limits {
		if !(l.Level == LevelAccount && l.Operation == "TRANSFER_IN" && l.Scope == "ACC-001") {
			kept = append(kept, l)
		}
	}
	cfg.Limits = kept
	if err := s3.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if d := eval(s3, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 8000}, true); !d.Allowed {
		t.Fatalf("first 8,000 (no override) should pass: %s", d.Reason)
	}
	if d := eval(s3, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 8000}, false); d.Allowed {
		t.Fatal("16,000 total > tier 15,000 must block once the override is removed")
	}
}

func TestCalendarWeekRollover(t *testing.T) {
	// §6.4: weekly limit resets on the calendar week boundary, not rolling.
	s := NewState()
	key := LimitKey{Operation: "BFTN", Metric: MetricAmount, Period: PeriodWeekly}
	cfg, _ := s.Snapshot()
	cfg.Limits = append(cfg.Limits, Limit{Level: LevelTier, LimitKey: key, Ceiling: 5000, Scope: "CURRENT_ACCOUNT#TIER_1"})
	if err := s.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}

	monday := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC) // week 33
	nextMon := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC) // week 34

	s.mu.Lock()
	d1 := s.evaluateLocked(Txn{AccountID: "ACC-001", Operation: "BFTN", Amount: 5000}, true, monday)
	d2 := s.evaluateLocked(Txn{AccountID: "ACC-001", Operation: "BFTN", Amount: 1}, false, monday)
	d3 := s.evaluateLocked(Txn{AccountID: "ACC-001", Operation: "BFTN", Amount: 5000}, false, nextMon)
	s.mu.Unlock()

	if !d1.Allowed || d2.Allowed {
		t.Fatalf("week 33: first 5,000 ok then blocked; got %v then %v (%s)", d1.Allowed, d2.Allowed, d2.Reason)
	}
	if !d3.Allowed {
		t.Fatalf("next calendar week must reset usage: %s", d3.Reason)
	}
	if got := WindowKey(PeriodWeekly, time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)); got != "2026-W33" {
		t.Fatalf("12 Aug 2026 should be week 33, got %s", got)
	}
}

func TestProductNotOfferingOperation(t *testing.T) {
	// §4: DPS_SAVINGS_ACCOUNT can't do BFTN — not on its product menu.
	s := NewState()
	d := eval(s, Txn{AccountID: "ACC-004", Operation: "BFTN", Amount: 100}, false)
	if d.Allowed {
		t.Fatal("DPS account must not perform BFTN")
	}
	if !contains(d.Reason, "product") && !contains(d.Trace[0].Detail, "product") {
		t.Fatalf("should fail at the product gate, got: %s", d.Reason)
	}
}

func TestUnknownOperationRejectedAtGate1(t *testing.T) {
	// §3.1: CREDIT_CARD_BILL_PAYMENT isn't in the master menu.
	s := NewState()
	d := eval(s, Txn{AccountID: "ACC-001", Operation: "CREDIT_CARD_BILL_PAYMENT", Amount: 100}, false)
	if d.Allowed || !contains(d.Trace[0].Gate, "Core Engine") {
		t.Fatalf("unknown op must fail gate 1, got %+v", d.Trace)
	}
}

func TestEnforcementIndependentPerLevel(t *testing.T) {
	// §7.3: service enforcement OFF does not disable tier/account checks.
	s := NewState()
	cfg, _ := s.Snapshot()
	// service daily TRANSFER_IN amount = 10,000,000 with switch OFF
	for i := range cfg.Limits {
		if cfg.Limits[i].Level == LevelService && cfg.Limits[i].Operation == "TRANSFER_IN" {
			cfg.Limits[i].Ceiling = 1 // absurdly low
		}
	}
	cfg.Rules = append(cfg.Rules, Rule{Level: LevelService, Kind: RuleKindEnforcement, Enabled: false,
		Operation: "TRANSFER_IN", Metric: MetricAmount, Period: PeriodDaily, Scope: "CURRENT_ACCOUNT"})
	if err := s.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// service ceiling dormant; the live constraints are per-txn 10,000 / account-daily 20,000
	d := eval(s, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 9000}, false)
	if !d.Allowed {
		t.Fatalf("service switch OFF must skip its own check while tier/account stay live: %s", d.Reason)
	}
	// but per-txn (tier) still applies
	d = eval(s, Txn{AccountID: "ACC-001", Operation: "TRANSFER_IN", Amount: 11000}, false)
	if d.Allowed {
		t.Fatal("tier per-txn 10,000 must still be checked")
	}
}

func TestServiceCeilingCapsAccountOverride(t *testing.T) {
	// §6.7 Q1: Bangladesh Bank mandate — service daily 10,000 caps everything.
	s := NewState()
	cfg, _ := s.Snapshot()
	key := LimitKey{Operation: "NPSB", Metric: MetricAmount, Period: PeriodDaily}
	for i := range cfg.Limits {
		if cfg.Limits[i].LimitKey == key && cfg.Limits[i].Scope == "CURRENT_ACCOUNT" {
			cfg.Limits[i].Ceiling = 10_000 // the mandate
		}
	}
	// account override of 50,000 cannot exceed it (§6.3.1)
	cfg.Limits = append(cfg.Limits, Limit{Level: LevelAccount, LimitKey: key, Ceiling: 50_000, Scope: "ACC-001"})
	if err := s.ReplaceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	d := eval(s, Txn{AccountID: "ACC-001", Operation: "NPSB", Amount: 12_000}, false)
	if d.Allowed {
		t.Fatal("12,000 must be blocked: service mandate 10,000 caps the 50,000 account override")
	}
}

func TestTierChangeDropsOverride(t *testing.T) {
	// §6.6.2: account moves tier → account override must be re-configured; the
	// override itself is per-account, so it survives, but the new tier's limits
	// apply for combinations the override doesn't cover.
	s := NewState()
	// ACC-005 is TIER_3 with NPSB per-txn 200,000
	d := eval(s, Txn{AccountID: "ACC-005", Operation: "NPSB", Amount: 150_000}, false)
	if !d.Allowed {
		t.Fatalf("TIER_3 corporate per-txn 200,000 should allow 150,000: %s", d.Reason)
	}
	// move ACC-005 to TIER_1 (per-txn 100,000) — §7.5 approach 1
	a := Account{ID: "ACC-005", Product: "CURRENT_ACCOUNT", AccountType: AccountTypeGeneral, Tier: "TIER_1", Balance: 500_000}
	if err := s.UpsertAccount(a); err != nil {
		t.Fatal(err)
	}
	d = eval(s, Txn{AccountID: "ACC-005", Operation: "NPSB", Amount: 150_000}, false)
	if d.Allowed {
		t.Fatal("after moving to TIER_1 the per-txn 100,000 must apply")
	}
}

func TestSpecialAccountNegativeBalance(t *testing.T) {
	// §5/§8: SPECIAL account with ALLOW_NEGATIVE_BALANCE=YES may go negative.
	s := NewState()
	d := eval(s, Txn{AccountID: "ACC-003", Operation: "TRANSFER_OUT", Amount: 5000}, false)
	if !d.Allowed {
		t.Fatalf("special account should be allowed to go negative: %s", d.Reason)
	}
	d = eval(s, Txn{AccountID: "ACC-001", Operation: "TRANSFER_OUT", Amount: 60_000}, false)
	if d.Allowed {
		t.Fatal("general account with balance 50,000 must fail on insufficient funds")
	}
}

func TestMissingLimitMeansNoRestriction(t *testing.T) {
	// §6.3 note: no configured limit for a combination → no restriction.
	s := NewState()
	// RTGS has zero limits seeded — only the balance gate applies (50,000 seeded)
	d := eval(s, Txn{AccountID: "ACC-001", Operation: "RTGS", Amount: 40_000}, false)
	if !d.Allowed {
		t.Fatalf("RTGS has no limits configured — must be unrestricted: %s", d.Reason)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

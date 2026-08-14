package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Decision is the outcome of simulating one transaction.
type Decision struct {
	Allowed bool         `json:"allowed"`
	Reason  string       `json:"reason"`
	Trace   []TraceStep  `json:"trace"`
	Limits  []LimitCheck `json:"limitChecks"`
	Summary Summary      `json:"summary"`
}

// TraceStep is one gate of the §9 pipeline.
type TraceStep struct {
	Gate     string `json:"gate"`
	Question string `json:"question"`
	Result   string `json:"result"` // PASS | FAIL | SKIPPED
	Detail   string `json:"detail"`
}

// LimitCheck shows how one effective limit was resolved and compared.
type LimitCheck struct {
	LimitKey
	Level     Level  `json:"level"`
	From      string `json:"from"`
	Enforced  bool   `json:"enforced"`
	Ceiling   int64  `json:"ceiling"`
	Used      int64  `json:"used"`
	Requested int64  `json:"requested"`
	WouldBe   int64  `json:"wouldBe"`
	Pass      bool   `json:"pass"`
	Note      string `json:"note"`
}

type Summary struct {
	Account     string `json:"account"`
	Product     string `json:"product"`
	AccountType string `json:"accountType"`
	Tier        string `json:"tier"`
	Operation   string `json:"operation"`
	Amount      int64  `json:"amount"`
	When        string `json:"when"`
}

// Evaluate runs the §9 pipeline: Core Engine → Product → Permission rules →
// Enforcement rules → Limit resolution → Limit validation. First failure wins.
// commit=true records usage and applies the balance change on success.
func (s *State) Evaluate(t Txn, commit bool) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evaluateLocked(t, commit, time.Now())
}

func (s *State) evaluateLocked(t Txn, commit bool, now time.Time) Decision {
	d := Decision{Trace: []TraceStep{}, Limits: []LimitCheck{}}

	fail := func(gate, question, detail string) Decision {
		d.Trace = append(d.Trace, TraceStep{Gate: gate, Question: question, Result: "FAIL", Detail: detail})
		d.Allowed = false
		d.Reason = detail
		return d
	}
	pass := func(gate, question, detail string) {
		d.Trace = append(d.Trace, TraceStep{Gate: gate, Question: question, Result: "PASS", Detail: detail})
	}

	acct, ok := s.Accounts[t.AccountID]
	if !ok {
		return fail("0. Account lookup", "Does this account exist?", fmt.Sprintf("account %s not found", t.AccountID))
	}
	when := t.When
	if when.IsZero() {
		when = now
	}
	d.Summary = Summary{
		Account: acct.ID, Product: acct.Product, AccountType: string(acct.AccountType),
		Tier: acct.Tier, Operation: t.Operation, Amount: t.Amount, When: when.Format(time.RFC3339),
	}

	// Gate 1 — Core Engine Service Configuration (§3)
	opDef := s.findOperation(t.Operation)
	if opDef == nil {
		return fail("1. Core Engine Service Configuration", "Is the operation supported by the engine?",
			fmt.Sprintf("%s is not in the master menu of operations", t.Operation))
	}
	pass("1. Core Engine Service Configuration", "Is the operation supported by the engine?",
		fmt.Sprintf("%s is in the master menu — %s", t.Operation, opDef.Description))

	// Gate 2 — Product Configuration (§4)
	offered := false
	for _, op := range s.Config.Products[acct.Product] {
		if op == t.Operation {
			offered = true
			break
		}
	}
	if !offered {
		return fail("2. Product Configuration", "Does this product offer the operation?",
			fmt.Sprintf("%s does not include %s in its product configuration — the operation was never on this product's menu", acct.Product, t.Operation))
	}
	pass("2. Product Configuration", "Does this product offer the operation?",
		fmt.Sprintf("%s offers %s", acct.Product, t.Operation))

	// Gate 3 — Permission rules, resolved top-down; any OFF blocks (§7.2)
	tierScope := acct.Product + "#" + acct.Tier
	permSvc := s.findPermission(LevelService, t.Operation, acct.Product)
	permTier := s.findPermission(LevelTier, t.Operation, tierScope)
	permAcct := s.findPermission(LevelAccount, t.Operation, acct.ID)

	// Default when no rule row exists: ON.
	svcOn, tierOn, acctOn := true, true, true
	svcSrc, tierSrc, acctSrc := "no rule → default ON", "no rule → default ON", "no rule → default ON"
	if permSvc != nil {
		svcOn, svcSrc = permSvc.Enabled, fmt.Sprintf("SERVICE·%s·%s = OFF", acct.Product, t.Operation)
	}
	if permTier != nil {
		tierOn, tierSrc = permTier.Enabled, fmt.Sprintf("TIER·%s·%s = OFF", tierScope, t.Operation)
	}
	if permAcct != nil {
		acctOn, acctSrc = permAcct.Enabled, fmt.Sprintf("ACCOUNT·%s·%s = OFF", acct.ID, t.Operation)
	}

	gate3 := TraceStep{Gate: "3. Permission Rules",
		Question: "Is the operation allowed at every level? A broader OFF can never be switched back on lower down (§7.2)."}
	switch {
	case !svcOn:
		gate3.Result, gate3.Detail = "FAIL", fmt.Sprintf("%s — a broader OFF blocks everyone below it.", svcSrc)
		d.Trace = append(d.Trace, gate3)
		d.Allowed, d.Reason = false, fmt.Sprintf("operation %s is switched OFF at SERVICE level for %s", t.Operation, acct.Product)
		return d
	case !tierOn:
		gate3.Result, gate3.Detail = "FAIL", fmt.Sprintf("%s — the account-level ON is never reached: a narrower ON cannot reopen a broader gate (§7.4.1).", tierSrc)
		d.Trace = append(d.Trace, gate3)
		d.Allowed, d.Reason = false, fmt.Sprintf("operation %s is switched OFF at TIER level for %s", t.Operation, tierScope)
		return d
	case !acctOn:
		gate3.Result, gate3.Detail = "FAIL", fmt.Sprintf("%s — the narrowest OFF also wins.", acctSrc)
		d.Trace = append(d.Trace, gate3)
		d.Allowed, d.Reason = false, fmt.Sprintf("operation %s is switched OFF for account %s", t.Operation, acct.ID)
		return d
	default:
		gate3.Result = "PASS"
		gate3.Detail = fmt.Sprintf("SERVICE ON · TIER ON · ACCOUNT ON — every level must be ON. Rows: service %s | tier %s | account %s",
			ruleRow(permSvc), ruleRow(permTier), ruleRow(permAcct))
	}
	d.Trace = append(d.Trace, gate3)

	// Gates 4–6 — Enforcement rules (§7.3), limit resolution (§6.3), validation.
	// §6.2: every (operation, metric, period) combination configured anywhere in
	// this account's scope chain is evaluated independently, and all must pass.
	keys := map[LimitKey]bool{}
	addKeys := func(level Level, scope string) {
		for _, l := range s.Config.Limits {
			if l.Level == level && l.Scope == scope && l.Operation == t.Operation {
				keys[l.LimitKey] = true
			}
		}
	}
	addKeys(LevelService, acct.Product)
	addKeys(LevelTier, tierScope)
	addKeys(LevelAccount, acct.ID)

	if len(keys) == 0 {
		pass("4–6. Enforcement Rules → Limit Resolution → Limit Validation",
			"Which limits are live, and does the transaction pass every applicable one?",
			"No limit configured for this operation at any level — a missing limit means no restriction (§6.3 note).")
	}

	for key := range keys {
		d.Limits = append(d.Limits, s.checkLimit(acct, key, t, when))
	}
	sort.SliceStable(d.Limits, func(i, j int) bool {
		po := map[Period]int{PeriodPerTxn: 0, PeriodDaily: 1, PeriodWeekly: 2, PeriodMonthly: 3}
		mo := map[Metric]int{MetricAmount: 0, MetricCount: 1}
		if po[d.Limits[i].Period] != po[d.Limits[j].Period] {
			return po[d.Limits[i].Period] < po[d.Limits[j].Period]
		}
		return mo[d.Limits[i].Metric] < mo[d.Limits[j].Metric]
	})

	for _, lc := range d.Limits {
		if lc.Enforced && !lc.Pass {
			d.Allowed, d.Reason = false, fmt.Sprintf(
				"%s %s %s limit breached: %s + %s = %s exceeds ceiling %s (%s from %s level)",
				lc.Operation, displayPeriod(lc.Period), lc.Metric,
				num(lc.Used), num(lc.Requested), num(lc.WouldBe), num(lc.Ceiling), lc.From, lc.Level)
			d.Trace = append(d.Trace, TraceStep{
				Gate: "4–6. Enforcement Rules → Limit Resolution → Limit Validation",
				Question: "Which limits are live, and does the transaction pass every applicable one?",
				Result:  "FAIL", Detail: d.Reason,
			})
			return d
		}
	}
	if len(d.Limits) > 0 {
		live := 0
		for _, lc := range d.Limits {
			if lc.EnformedLive() {
				live++
			}
		}
		pass("4–6. Enforcement Rules → Limit Resolution → Limit Validation",
			"Which limits are live, and does the transaction pass every applicable one?",
			fmt.Sprintf("%d limit combination(s) evaluated: %d live (enforcement ON) and passed, %d dormant (enforcement OFF → skipped, §7.3).",
				len(d.Limits), live, len(d.Limits)-live))
	}

	// Gate 7 — balance sanity for debits. SPECIAL accounts consult the Account
	// Config Table (§8) for ALLOW_NEGATIVE_BALANCE.
	if opDef.Direction == "DEBIT" && t.Amount > acct.Balance {
		if acct.AccountType == AccountTypeSpecial && acct.Config["ALLOW_NEGATIVE_BALANCE"] == "YES" {
			pass("7. Balance", "Is there enough money?",
				fmt.Sprintf("Balance %s would go negative — permitted: SPECIAL account with ALLOW_NEGATIVE_BALANCE=YES (§8).", num(acct.Balance)))
		} else {
			return fail("7. Balance", "Is there enough money?",
				fmt.Sprintf("insufficient funds: balance %s < requested %s", num(acct.Balance), num(t.Amount)))
		}
	}

	d.Allowed = true
	d.Reason = "All gates passed — transaction ALLOWED"
	pass("8. Result", "ALLOW / REJECT?", "ALLOW")
	if commit {
		s.commit(acct, opDef, t, when)
	}
	return d
}

// EnforcedWithNumber distinguishes "a live limit exists" from "no number at all"
// for the summary count.
func (lc LimitCheck) EnformedLive() bool { return lc.Enforced && lc.Ceiling > 0 }

// checkLimit resolves one (operation, metric, period) combination:
// precedence per §6.3, enforcement per §7.3, comparison per §6.5.
func (s *State) checkLimit(acct Account, key LimitKey, t Txn, when time.Time) LimitCheck {
	lc := LimitCheck{LimitKey: key, Requested: requestFor(key.Metric, t.Amount)}

	tierScope := acct.Product + "#" + acct.Tier
	var svcLim, tierLim, acctLim *Limit
	for i := range s.Config.Limits {
		l := &s.Config.Limits[i]
		if l.LimitKey != key || l.Operation != t.Operation {
			continue
		}
		switch {
		case l.Level == LevelService && l.Scope == acct.Product:
			svcLim = l
		case l.Level == LevelTier && l.Scope == tierScope:
			tierLim = l
		case l.Level == LevelAccount && l.Scope == acct.ID:
			acctLim = l
		}
	}

	// Enforcement switches sit at the same level as their limit (§7.4.2).
	// Default when no row exists: ON — a missing row means "check it".
	enforcedSvc := s.enforcementOn(LevelService, key, acct.Product)
	enforcedTier := s.enforcementOn(LevelTier, key, tierScope)
	enforcedAcct := s.enforcementOn(LevelAccount, key, acct.ID)

	// ---- Service-level ceiling: the hard cap (§6.3). Skipped only when its own
	// switch is OFF (§7.3) — that never disables the tier/account checks.
	var svcPart *LimitCheck
	if svcLim != nil && enforcedSvc {
		p := &LimitCheck{LimitKey: key, Level: LevelService, From: "SERVICE·" + acct.Product,
			Enforced: true, Ceiling: svcLim.Ceiling, Requested: lc.Requested}
		u := s.usage(acct.ID, t.Operation, key, when)
		p.Used = metricVal(key.Metric, u)
		p.WouldBe = p.Used + p.Requested
		p.Pass = p.WouldBe <= p.Ceiling
		svcPart = p
	} else if svcLim != nil {
		lc.Note += fmt.Sprintf("service %s %s limit (%s) exists but its enforcement switch is OFF → not checked (§7.3); ", key.Metric, key.Period, num(svcLim.Ceiling))
	}

	// ---- Effective Tier/Account limit: start at tier, apply account override (§6.3).
	var basePart *LimitCheck
	switch {
	case acctLim != nil:
		p := &LimitCheck{LimitKey: key, Level: LevelAccount, From: fmt.Sprintf("ACCOUNT·%s (override)", acct.ID),
			Enforced: enforcedAcct, Ceiling: acctLim.Ceiling, Requested: lc.Requested}
		u := s.usage(acct.ID, t.Operation, key, when)
		p.Used = metricVal(key.Metric, u)
		p.WouldBe = p.Used + p.Requested
		p.Pass = p.WouldBe <= p.Ceiling
		p.Note = fmt.Sprintf("account override %s replaces tier %s for %s·%s (§6.3.1)", num(acctLim.Ceiling), tierNumOrNone(tierLim), key.Metric, key.Period)
		if !enforcedAcct {
			p.Note += " — but its enforcement switch is OFF, so it is dormant (§7.4.2)"
		}
		basePart = p
	case tierLim != nil:
		p := &LimitCheck{LimitKey: key, Level: LevelTier, From: "TIER·" + tierScope,
			Enforced: enforcedTier, Ceiling: tierLim.Ceiling, Requested: lc.Requested}
		u := s.usage(acct.ID, t.Operation, key, when)
		p.Used = metricVal(key.Metric, u)
		p.WouldBe = p.Used + p.Requested
		p.Pass = p.WouldBe <= p.Ceiling
		if !enforcedTier {
			p.Note = "tier enforcement switch is OFF → this limit is dormant (§7.4.2)"
		}
		basePart = p
	}

	// Combine: the txn must pass the service ceiling AND the effective tier/account limit.
	if svcPart != nil && basePart != nil {
		combined := *svcPart
		combined.Pass = svcPart.Pass && basePart.Pass
		combined.Note = strings.TrimSpace(basePart.Note)
		// If the tier/account number is the binding constraint, show its arithmetic.
		if !basePart.Pass || basePart.Ceiling <= svcPart.Ceiling {
			combined.Level, combined.From, combined.Ceiling = basePart.Level, basePart.From, basePart.Ceiling
			combined.Used, combined.WouldBe = basePart.Used, basePart.WouldBe
		}
		combined.Enforced = true
		return combined
	}
	if svcPart != nil {
		return *svcPart
	}
	if basePart != nil {
		return *basePart
	}
	lc.Enforced, lc.Pass = false, true
	lc.Note = "no limit configured at any level → no restriction for this combination (§6.3 note)"
	return lc
}

func (s *State) findOperation(name string) *OperationDef {
	for i := range s.Config.Operations {
		if s.Config.Operations[i].Name == name {
			return &s.Config.Operations[i]
		}
	}
	return nil
}

func (s *State) findPermission(level Level, operation, scope string) *Rule {
	for i := range s.Config.Rules {
		r := &s.Config.Rules[i]
		if r.Kind == RuleKindPermission && r.Level == level && r.Operation == operation && r.Scope == scope {
			return r
		}
	}
	return nil
}

// enforcementOn reports whether the limit at (level, scope, key) has its switch
// ON. Default ON when no row exists (§7.4.2) — the fail-safe direction.
func (s *State) enforcementOn(level Level, key LimitKey, scope string) bool {
	for i := range s.Config.Rules {
		r := &s.Config.Rules[i]
		if r.Kind == RuleKindEnforcement && r.Level == level && r.Scope == scope &&
			r.Operation == key.Operation && r.Metric == key.Metric && r.Period == key.Period {
			return r.Enabled
		}
	}
	return true
}

func (s *State) usage(accountID, operation string, key LimitKey, when time.Time) usageVal {
	if key.Period == PeriodPerTxn {
		return usageVal{}
	}
	return s.Usage[usageKey{AccountID: accountID, Operation: operation, Metric: key.Metric, Window: WindowKey(key.Period, when)}]
}

func (s *State) commit(acct Account, opDef *OperationDef, t Txn, when time.Time) {
	// record usage for every calendar metric/period on this operation (§6.4)
	for _, metric := range []Metric{MetricAmount, MetricCount} {
		for _, period := range []Period{PeriodDaily, PeriodWeekly, PeriodMonthly} {
			k := usageKey{AccountID: acct.ID, Operation: t.Operation, Metric: metric, Window: WindowKey(period, when)}
			v := s.Usage[k]
			v.Amount += t.Amount
			v.Count++
			s.Usage[k] = v
		}
	}
	if opDef.Direction == "DEBIT" {
		acct.Balance -= t.Amount
	} else if opDef.Direction == "CREDIT" {
		acct.Balance += t.Amount
	}
	s.Accounts[acct.ID] = acct
}

// --- display helpers ---

func ruleRow(r *Rule) string {
	if r == nil {
		return "none → default ON"
	}
	return fmt.Sprintf("%s = %s", r.Scope, onOff(r.Enabled))
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

func requestFor(m Metric, amount int64) int64 {
	if m == MetricCount {
		return 1
	}
	return amount
}

func metricVal(m Metric, v usageVal) int64 {
	if m == MetricCount {
		return v.Count
	}
	return v.Amount
}

func tierNumOrNone(l *Limit) string {
	if l == nil {
		return "none"
	}
	return num(l.Ceiling)
}

func displayPeriod(p Period) string {
	switch p {
	case PeriodPerTxn:
		return "per-transaction"
	case PeriodDaily:
		return "daily"
	case PeriodWeekly:
		return "weekly"
	case PeriodMonthly:
		return "monthly"
	}
	return string(p)
}

func displayMetric(m Metric) string {
	if m == MetricCount {
		return "txn(s)"
	}
	return "BDT"
}

func num(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	if neg {
		return "-" + out
	}
	return out
}

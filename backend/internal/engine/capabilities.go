package engine

import (
	"fmt"
	"sort"
)

// Capabilities is the per-account, per-operation answer to "what can this
// account do?" — computed up front, before any transaction is simulated.
// It backs the Accounts view capability matrix in the UI.
type Capabilities struct {
	AccountID string              `json:"accountId"`
	Ops       []CapabilityRow     `json:"ops"`
}

type CapabilityRow struct {
	Operation string        `json:"operation"`
	// Status: OFFERED (product has it), NOT_OFFERED (§4 product menu),
	// BLOCKED_SERVICE / BLOCKED_TIER / BLOCKED_ACCOUNT (§7.2 permission).
	Status string `json:"status"`
	BlockedBy string `json:"blockedBy,omitempty"`
	PermissionTrace string `json:"permissionTrace"` // human summary of the three switches
	EffectiveLimits []EffectiveLimit `json:"effectiveLimits"`
}

// EffectiveLimit is one resolved op·metric·period combination for the account:
// where the number comes from and whether its enforcement switch is live.
type EffectiveLimit struct {
	LimitKey
	ServiceCeiling *int64 `json:"serviceCeiling,omitempty"` // the hard cap, if any
	ServiceLive    bool   `json:"serviceLive"`              // service enforcement switch state
	Level          Level  `json:"level"` // TIER or ACCOUNT — where the effective number comes from
	From           string `json:"from"`
	Ceiling        int64  `json:"ceiling"`
	Enforced       bool   `json:"enforced"`
	Note           string `json:"note,omitempty"`
}

// ResolveCapabilities walks every operation in the master menu for one account
// and reports status + effective limits. Pure read; no locks held on return.
func (s *State) ResolveCapabilities(accountID string) (*Capabilities, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acct, ok := s.Accounts[accountID]
	if !ok {
		return nil, fmt.Errorf("account %s not found", accountID)
	}
	tierScope := acct.Product + "#" + acct.Tier

	ops := append([]OperationDef(nil), s.Config.Operations...)
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })

	caps := &Capabilities{AccountID: accountID, Ops: []CapabilityRow{}}
	for _, op := range ops {
		row := CapabilityRow{Operation: op.Name, EffectiveLimits: []EffectiveLimit{}}

		// §4 product menu
		offered := false
		for _, o := range s.Config.Products[acct.Product] {
			if o == op.Name {
				offered = true
				break
			}
		}
		if !offered {
			row.Status = "NOT_OFFERED"
			row.PermissionTrace = fmt.Sprintf("%s does not include %s in its product configuration (§4)", acct.Product, op.Name)
			caps.Ops = append(caps.Ops, row)
			continue
		}

		// §7.2 permission switches, top-down
		svc := s.findPermissionRLocked(LevelService, op.Name, acct.Product)
		tier := s.findPermissionRLocked(LevelTier, op.Name, tierScope)
		acctR := s.findPermissionRLocked(LevelAccount, op.Name, acct.ID)
		svcOn := svc == nil || svc.Enabled
		tierOn := tier == nil || tier.Enabled
		acctOn := acctR == nil || acctR.Enabled
		switch {
		case !svcOn:
			row.Status, row.BlockedBy = "BLOCKED_SERVICE", fmt.Sprintf("SERVICE·%s", acct.Product)
		case !tierOn:
			row.Status, row.BlockedBy = "BLOCKED_TIER", tierScope
		case !acctOn:
			row.Status, row.BlockedBy = "BLOCKED_ACCOUNT", acct.ID
		default:
			row.Status = "OFFERED"
		}
		row.PermissionTrace = fmt.Sprintf("SERVICE %s · TIER %s · ACCOUNT %s",
			switchLabel(svc), switchLabel(tier), switchLabel(acctR))

		// effective limits for this operation (§6.3 + §7.3)
		keys := map[LimitKey]bool{}
		for _, l := range s.Config.Limits {
			if l.Operation != op.Name {
				continue
			}
			if (l.Level == LevelService && l.Scope == acct.Product) ||
				(l.Level == LevelTier && l.Scope == tierScope) ||
				(l.Level == LevelAccount && l.Scope == acct.ID) {
				keys[l.LimitKey] = true
			}
		}
		for key := range keys {
			row.EffectiveLimits = append(row.EffectiveLimits, s.resolveEffectiveLimitRLocked(acct, tierScope, key))
		}
		sort.Slice(row.EffectiveLimits, func(i, j int) bool {
			po := map[Period]int{PeriodPerTxn: 0, PeriodDaily: 1, PeriodWeekly: 2, PeriodMonthly: 3}
			if po[row.EffectiveLimits[i].Period] != po[row.EffectiveLimits[j].Period] {
				return po[row.EffectiveLimits[i].Period] < po[row.EffectiveLimits[j].Period]
			}
			return row.EffectiveLimits[i].Metric < row.EffectiveLimits[j].Metric
		})
		caps.Ops = append(caps.Ops, row)
	}
	return caps, nil
}

// resolveEffectiveLimitRLocked computes one effective limit: service cap +
// tier-or-account number + enforcement switches. Must hold at least RLock.
func (s *State) resolveEffectiveLimitRLocked(acct Account, tierScope string, key LimitKey) EffectiveLimit {
	el := EffectiveLimit{LimitKey: key}

	var svcLim, tierLim, acctLim *Limit
	for i := range s.Config.Limits {
		l := &s.Config.Limits[i]
		if l.LimitKey != key {
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

	svcLive := true
	if svcLim != nil {
		el.ServiceCeiling = &svcLim.Ceiling
		svcLive = s.enforcementOnRLocked(LevelService, key, acct.Product)
		el.ServiceLive = svcLive
	}

	switch {
	case acctLim != nil:
		el.Level, el.From, el.Ceiling = LevelAccount, fmt.Sprintf("ACCOUNT·%s override", acct.ID), acctLim.Ceiling
		el.Enforced = s.enforcementOnRLocked(LevelAccount, key, acct.ID)
		if tierLim != nil {
			el.Note = fmt.Sprintf("override %s replaces tier %s (§6.3.1); tier changes no longer affect this account for %s·%s (§6.8.1)", num(acctLim.Ceiling), num(tierLim.Ceiling), key.Metric, key.Period)
		}
	case tierLim != nil:
		el.Level, el.From, el.Ceiling = LevelTier, "TIER·" + tierScope, tierLim.Ceiling
		el.Enforced = s.enforcementOnRLocked(LevelTier, key, tierScope)
	default:
		if svcLim == nil {
			el.Note = "no limit at any level → unrestricted (§6.3 note)"
			return el
		}
		// service-only: the service number is also the effective number
		el.Level, el.From, el.Ceiling = LevelService, "SERVICE·"+acct.Product, svcLim.Ceiling
		el.Enforced = svcLive
		return el
	}
	if svcLim == nil {
		return el
	}
	// both exist: report the tier/account number but flag the service cap
	if el.Ceiling > svcLim.Ceiling {
		el.Note = (el.Note + " " + fmt.Sprintf("· service ceiling %s caps this override (§6.3)", num(svcLim.Ceiling)))
	}
	if !svcLive {
		el.Note += " · service enforcement OFF → the ceiling check is skipped (§7.3)"
	}
	return el
}

func (s *State) findPermissionRLocked(level Level, operation, scope string) *Rule {
	for i := range s.Config.Rules {
		r := &s.Config.Rules[i]
		if r.Kind == RuleKindPermission && r.Level == level && r.Operation == operation && r.Scope == scope {
			return r
		}
	}
	return nil
}

func (s *State) enforcementOnRLocked(level Level, key LimitKey, scope string) bool {
	for i := range s.Config.Rules {
		r := &s.Config.Rules[i]
		if r.Kind == RuleKindEnforcement && r.Level == level && r.Scope == scope &&
			r.Operation == key.Operation && r.Metric == key.Metric && r.Period == key.Period {
			return r.Enabled
		}
	}
	return true
}

func switchLabel(r *Rule) string {
	if r == nil {
		return "ON (default)"
	}
	if r.Enabled {
		return "ON"
	}
	return "OFF"
}

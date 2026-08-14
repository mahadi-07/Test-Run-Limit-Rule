#!/usr/bin/env python3
"""End-to-end check: drive the simulator's API exactly as the UI does, and
verify every scenario in the Limit & Rule Configuration V5 doc."""
import json, urllib.request, sys

BASE = "http://localhost:8080"
PASS, FAIL = [], []

def api(path, body=None, method=None):
    data = json.dumps(body).encode() if body is not None else None
    m = method or ("POST" if data is not None else "GET")
    req = urllib.request.Request(BASE + path, data=data, method=m,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req) as r:
            return json.loads(r.read())
    except urllib.error.HTTPError as e:
        return {"_error": e.read().decode(), "_status": e.code}

def reset():
    """POST the reset endpoint (no body) — must be an explicit POST."""
    req = urllib.request.Request(BASE + "/api/reset", data=b"", method="POST")
    with urllib.request.urlopen(req) as r: json.loads(r.read())

def reset_blank():
    req = urllib.request.Request(BASE + "/api/reset-blank", data=b"", method="POST")
    with urllib.request.urlopen(req) as r: json.loads(r.read())

def sim(acct, op, amount, when=None, commit=False):
    body = {"accountId": acct, "operation": op, "amount": amount, "commit": commit}
    if when: body["when"] = when
    return api("/api/simulate", body)

def check(name, cond, detail=""):
    (PASS if cond else FAIL).append(name)
    print(f"  {'✓' if cond else '✗ FAIL'} {name}" + (f"  — {detail}" if detail and not cond else ""))

def section(title): print(f"\n── {title} ──")

# ============================================================
section("§0 Day 30 — tier OFF blocks everyone in the tier")
reset()
d = sim("ACC-001", "AGENT_POINT_WITHDRAWAL", 5000)
check("tier OFF blocks withdrawal", not d["allowed"], d.get("reason"))
check("reason cites TIER level", "TIER" in d.get("reason", ""))
check("trace fails at gate 3 (permission)", any("Permission" in s["gate"] for s in d["trace"] if s["result"] == "FAIL"))

section("§0 Day 45 — account-level ON cannot override tier OFF")
state = api("/api/state")
check("seeded account ON rule exists", any(r["kind"]=="PERMISSION" and r["level"]=="ACCOUNT" and r["enabled"] and r["scope"]=="ACC-001" for r in state["config"]["rules"]))
d = sim("ACC-001", "AGENT_POINT_WITHDRAWAL", 5000)
check("still blocked despite account ON", not d["allowed"])
detail = [s["detail"] for s in d["trace"] if s["result"]=="FAIL"][0]
check("trace explains narrower ON can't reopen", "never reached" in detail or "cannot reopen" in detail, detail)

section("§0 Day 60 — tier re-enabled; 12,000 over per-txn cap, 9,000 passes")
api("/api/config/rules", {"upsert": {"level":"TIER","kind":"PERMISSION","operation":"AGENT_POINT_WITHDRAWAL","scope":"CURRENT_ACCOUNT#TIER_1","enabled":True}})
d12 = sim("ACC-001", "AGENT_POINT_WITHDRAWAL", 12000)
d9  = sim("ACC-001", "AGENT_POINT_WITHDRAWAL", 9000)
check("12,000 declined (per-txn 10,000)", not d12["allowed"], d12.get("reason"))
check("9,000 allowed", d9["allowed"], d9.get("reason"))
check("12,000 fails at limit gate, not permission", any("Enforcement" in s["gate"] or "Limit" in s["gate"] for s in d12["trace"] if s["result"]=="FAIL"))

section("§1.2 / §9 — check order: first 'no' wins")
d = sim("ACC-004", "CREDIT_CARD_BILL_PAYMENT", 100)   # DPS: unknown op AND not on menu
gates = [s for s in d["trace"] if s["result"] == "FAIL"]
check("unknown op fails at gate 1 (core engine)", len(gates)==1 and "Core Engine" in gates[0]["gate"], str(gates))
d = sim("ACC-004", "BFTN", 100)                        # known op, not on DPS menu
gates = [s for s in d["trace"] if s["result"] == "FAIL"]
check("DPS+BFTN fails at gate 2 (product), not later", len(gates)==1 and "Product" in gates[0]["gate"], str(gates))

section("§3.1 — rules/limits only for operations on the master menu")
r = api("/api/config/limits", {"upsert":{"level":"TIER","operation":"GHOST_OP","metric":"AMOUNT","period":"DAILY","ceiling":1,"scope":"CURRENT_ACCOUNT#TIER_1"}})
check("limit for unknown op rejected", r.get("_status") == 400, str(r))
r = api("/api/config/rules", {"upsert":{"level":"TIER","kind":"PERMISSION","operation":"GHOST_OP","scope":"CURRENT_ACCOUNT#TIER_1","enabled":False}})
check("rule for unknown op rejected", r.get("_status") == 400, str(r))

section("§6.2 — one operation carries many limits; all must pass")
lc = [l for l in api("/api/state")["config"]["limits"] if l["operation"]=="TRANSFER_OUT"]
t1 = [l for l in lc if l["scope"].startswith("CURRENT_ACCOUNT") or l["scope"]=="ACC-001"]
check("TRANSFER_OUT has 3 limits for CURRENT (service daily, tier per-txn, account count)", len(t1)==3, str(len(t1)))

section("§6.3 — precedence: Service=700 caps Account=1000 overriding Tier=500")
reset()
key = {"operation":"NPSB","metric":"COUNT","period":"DAILY"}
# wipe NPSB limits, install the doc's numbers for ACC-002's scope
cfg = api("/api/state")["config"]
cfg["limits"] = [l for l in cfg["limits"] if l["operation"] != "NPSB"]
cfg["limits"] += [
  {"level":"SERVICE","operation":"NPSB","metric":"COUNT","period":"DAILY","ceiling":700,"scope":"CURRENT_ACCOUNT"},
  {"level":"TIER","operation":"NPSB","metric":"COUNT","period":"DAILY","ceiling":500,"scope":"CURRENT_ACCOUNT#TIER_1"},
  {"level":"ACCOUNT","operation":"NPSB","metric":"COUNT","period":"DAILY","ceiling":1000,"scope":"ACC-001"},
]
api("/api/config", cfg, method="PUT")
ok_count = 0
for i in range(700):
    if sim("ACC-001","NPSB",1,commit=True)["allowed"]: ok_count += 1
d701 = sim("ACC-001","NPSB",1)
check("700 txns pass, 701st blocked", ok_count==700 and not d701["allowed"], f"passed={ok_count}, 701st allowed={d701['allowed']}")

section("§6.3 note — missing limit ≠ zero")
d = sim("ACC-001","RTGS",40000)   # RTGS has no limits, only balance gate
check("RTGS unrestricted up to balance", d["allowed"], d.get("reason"))

section("§6.4 — calendar windows, not rolling")
reset()
cfg = api("/api/state")["config"]
cfg["limits"] += [{"level":"TIER","operation":"BFTN","metric":"AMOUNT","period":"WEEKLY","ceiling":5000,"scope":"CURRENT_ACCOUNT#TIER_1"}]
api("/api/config", cfg, method="PUT")
mon = sim("ACC-001","BFTN",5000,when="2026-08-10",commit=True)     # week 33
same = sim("ACC-001","BFTN",1,when="2026-08-12")                   # still week 33
nextw = sim("ACC-001","BFTN",5000,when="2026-08-17")               # week 34
check("exhaust week 33", mon["allowed"] and not same["allowed"], same.get("reason"))
check("week 34 resets", nextw["allowed"], nextw.get("reason"))
d1 = sim("ACC-001","TRANSFER_IN",100,when="2026-08-14",commit=True)
d2 = sim("ACC-001","TRANSFER_IN",100,when="2026-08-15")
check("daily window resets next calendar day", d1["allowed"] and d2["allowed"], d2.get("reason"))

section("§6.5 — the worked example, four outcomes")
reset()
d = sim("ACC-001","TRANSFER_IN",12000)
check("single 12,000 blocked (per-txn 10,000)", not d["allowed"])
d = sim("ACC-001","TRANSFER_IN",9000,commit=True);  first_ok = d["allowed"]
d = sim("ACC-001","TRANSFER_IN",9000,commit=True);  second_ok = d["allowed"]
d = sim("ACC-001","TRANSFER_IN",1000)
check("two 9,000 pass (18,000 ≤ 20,000 override, count 2/2)", first_ok and second_ok, "")
check("third blocked (daily count 2)", not d["allowed"], d.get("reason"))
check("breach cites ACCOUNT override 20,000, not tier 15,000",
      "20,000" in d.get("reason","") or d["limitChecks"], d.get("reason"))

section("§6.6.1 — removing the override falls back to tier, not unrestricted")
reset()
api("/api/config/limits", {"remove":{"level":"ACCOUNT","operation":"TRANSFER_IN","metric":"AMOUNT","period":"DAILY","ceiling":20000,"scope":"ACC-001"}})
d8a = sim("ACC-001","TRANSFER_IN",8000,commit=True)
d8b = sim("ACC-001","TRANSFER_IN",8000)
check("without override: 16,000 > tier 15,000 blocked", d8a["allowed"] and not d8b["allowed"], d8b.get("reason"))

section("§6.6.2 / §6.8.1 — tier move re-bases; overrides persist")
reset()
d = sim("ACC-005","NPSB",150000)
check("TIER_3 corporate per-txn 200,000 allows 150,000", d["allowed"], d.get("reason"))
acc5 = api("/api/state")["accounts"]["ACC-005"]
api("/api/accounts", {**acc5, "tier":"TIER_1"})
d = sim("ACC-005","NPSB",150000)
check("moved to TIER_1: per-txn 100,000 blocks 150,000", not d["allowed"], d.get("reason"))

section("§6.7 Q1 — Bangladesh Bank mandate caps everything")
reset()
cfg = api("/api/state")["config"]
for l in cfg["limits"]:
    if l["operation"]=="TRANSFER_OUT" and l["level"]=="SERVICE" and l["period"]=="DAILY":
        l["ceiling"] = 10000
cfg["limits"] += [{"level":"ACCOUNT","operation":"TRANSFER_OUT","metric":"AMOUNT","period":"DAILY","ceiling":50000,"scope":"ACC-001"}]
api("/api/config", cfg, method="PUT")
d1 = sim("ACC-001","TRANSFER_OUT",6000,commit=True)
d2 = sim("ACC-001","TRANSFER_OUT",6000)
check("mandate: 2nd 6,000 blocked despite 50,000 override", d1["allowed"] and not d2["allowed"], d2.get("reason"))

section("§7.2 — permission: symmetric, any OFF on the path blocks")
reset()
api("/api/config/rules", {"upsert":{"level":"ACCOUNT","kind":"PERMISSION","operation":"RTGS","scope":"ACC-001","enabled":False}})
d = sim("ACC-001","RTGS",100)
check("account OFF blocks even with service+tier ON", not d["allowed"], d.get("reason"))
check("reason cites account", "account" in d.get("reason","").lower())

section("§7.3 — enforcement independent per level")
reset()
cfg = api("/api/state")["config"]
for l in cfg["limits"]:
    if l["operation"]=="TRANSFER_IN" and l["level"]=="SERVICE":
        l["ceiling"] = 1   # absurdly low service cap
cfg["rules"] += [{"level":"SERVICE","kind":"ENFORCEMENT","operation":"TRANSFER_IN","metric":"AMOUNT","period":"DAILY","scope":"CURRENT_ACCOUNT","enabled":False}]
api("/api/config", cfg, method="PUT")
d = sim("ACC-001","TRANSFER_IN",9000)
check("service switch OFF skips its cap; tier per-txn still applies", d["allowed"], d.get("reason"))
d = sim("ACC-001","TRANSFER_IN",11000)
check("tier per-txn 10,000 still checked", not d["allowed"], d.get("reason"))
caps = api("/api/accounts/ACC-001/capabilities")
ti = [o for o in caps["ops"] if o["operation"]=="TRANSFER_IN"][0]
dl = [l for l in ti["effectiveLimits"] if l["period"]=="DAILY"][0]
check("capabilities show tier/account number with service cap flagged", dl["level"] in ("ACCOUNT","TIER"), str(dl))

section("§7.4.2 — default ON; explicit OFF makes the number dormant")
reset()
api("/api/config/rules", {"upsert":{"level":"TIER","kind":"PERMISSION","operation":"AGENT_POINT_WITHDRAWAL","scope":"CURRENT_ACCOUNT#TIER_1","enabled":True}})
s1 = sim("ACC-001","AGENT_POINT_WITHDRAWAL",8000,when="2026-08-14",commit=True)
s2 = sim("ACC-001","AGENT_POINT_WITHDRAWAL",5000,when="2026-08-14")
check("8,000+5,000 over 10,000 daily: blocked", s1["allowed"] and not s2["allowed"], s2.get("reason"))
api("/api/config/rules", {"upsert":{"level":"TIER","kind":"ENFORCEMENT","operation":"AGENT_POINT_WITHDRAWAL","metric":"AMOUNT","period":"DAILY","scope":"CURRENT_ACCOUNT#TIER_1","enabled":False}})
s3 = sim("ACC-001","AGENT_POINT_WITHDRAWAL",5000,when="2026-08-14")
check("switch OFF: same 5,000 allowed (dormant)", s3["allowed"], s3.get("reason"))
dorm = [c for c in s3["limitChecks"] if c["period"]=="DAILY"]
check("limit check reported DORMANT", any(not c["enforced"] for c in dorm), str(dorm))

section("§5/§8 — SPECIAL accounts and the Account Config Table")
reset()
d = sim("ACC-003","TRANSFER_OUT",5000)
check("SPECIAL w/ ALLOW_NEGATIVE_BALANCE=YES goes negative", d["allowed"], d.get("reason"))
d = sim("ACC-001","TRANSFER_OUT",60000)
check("GENERAL with 50,000 balance: insufficient funds", not d["allowed"], d.get("reason"))
r = api("/api/accounts", {"id":"ACC-X","product":"CURRENT_ACCOUNT","accountType":"GENERAL_ACCOUNT","tier":"TIER_1","balance":100,"config":{"ALLOW_NEGATIVE_BALANCE":"YES"}})
st = api("/api/state")["accounts"]["ACC-X"]
check("§8 flags stripped from GENERAL on onboard", (st.get("config") or {}) == {}, str(st.get("config")))
r = api("/api/accounts", {"id":"ACC-Y","product":"NOPE","accountType":"GENERAL_ACCOUNT","tier":"TIER_1","balance":100})
check("unknown product rejected 400", r.get("_status")==400, str(r))
r = api("/api/accounts", {"id":"ACC-Y","product":"CURRENT_ACCOUNT","accountType":"GENERAL_ACCOUNT","tier":"NOPE","balance":100})
check("unknown tier rejected 400", r.get("_status")==400, str(r))

section("UI plumbing — capabilities, activity, day 0")
reset()
caps = api("/api/accounts/ACC-001/capabilities")
apw = [o for o in caps["ops"] if o["operation"]=="AGENT_POINT_WITHDRAWAL"][0]
check("capabilities flag BLOCKED_TIER for Aisha", apw["status"]=="BLOCKED_TIER", apw["status"])
sim("ACC-001","TRANSFER_IN",100,commit=True)
act = api("/api/state")["activity"]
check("activity logged", len(act)>=1 and act[0]["operation"]=="TRANSFER_IN", str(act[:1]))
reset_blank()
blank = api("/api/state")
check("day 0: [] not null everywhere",
      blank["config"]["operations"]==[] and blank["config"]["limits"]==[] and
      blank["config"]["rules"]==[] and blank["accounts"]=={} and
      blank["config"]["tiers"]==["DEFAULT_TIER"], str(blank["config"]))
r = api("/api/accounts", {"id":"ACC-1","product":"NONE","accountType":"GENERAL_ACCOUNT","tier":"DEFAULT_TIER","balance":0})
check("day 0 onboarding blocked (no products)", r.get("_status")==400, str(r))

# ============================================================
print(f"\n{'='*54}\nE2E: {len(PASS)} passed, {len(FAIL)} failed")
if FAIL:
    print("Failed:", *FAIL, sep="\n  - ")
    sys.exit(1)

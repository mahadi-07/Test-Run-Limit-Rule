package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"limit-sim/internal/engine"
)

func main() {
	state := engine.NewState()

	mux := http.NewServeMux()

	// full world snapshot for the UI
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		cfg, accounts := state.Snapshot()
		writeJSON(w, map[string]any{
			"config":   cfg,
			"accounts": accounts,
			"usage":    state.UsageSnapshot(""),
			"activity": state.ActivitySnapshot(),
		})
	})

	// per-account capability matrix: what every operation resolves to (§4/§7.2/§6.3)
	mux.HandleFunc("GET /api/accounts/{id}/capabilities", func(w http.ResponseWriter, r *http.Request) {
		caps, err := state.ResolveCapabilities(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, caps)
	})

	// ---- master menu / products / tiers CRUD (§3, §4) ----

	mux.HandleFunc("POST /api/config/operations", func(w http.ResponseWriter, r *http.Request) {
		var op engine.OperationDef
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		cfg, _ := state.Snapshot()
		for i := range cfg.Operations {
			if cfg.Operations[i].Name == op.Name {
				cfg.Operations[i] = op // edit in place (description/direction)
				writeJSONorErr(w, state.ReplaceConfig(cfg))
				return
			}
		}
		cfg.Operations = append(cfg.Operations, op)
		writeJSONorErr(w, state.ReplaceConfig(cfg))
	})

	mux.HandleFunc("DELETE /api/config/operations/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		cfg, _ := state.Snapshot()
		kept := cfg.Operations[:0]
		for _, o := range cfg.Operations {
			if o.Name != name {
				kept = append(kept, o)
			}
		}
		cfg.Operations = kept
		writeJSONorErr(w, state.ReplaceConfig(cfg))
	})

	mux.HandleFunc("PUT /api/config/products/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var ops []string
		if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		cfg, _ := state.Snapshot()
		// create or replace: a brand-new product may legitimately start empty
		// (the UI's "+ Add product" creates it, then ticks operations)
		cfg.Products[name] = ops
		writeJSONorErr(w, state.ReplaceConfig(cfg))
	})

	mux.HandleFunc("DELETE /api/config/products/{name}", func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := state.Snapshot()
		_, accounts := state.Snapshot()
		for _, a := range accounts {
			if a.Product == r.PathValue("name") {
				http.Error(w, "cannot delete: accounts still use this product", http.StatusConflict)
				return
			}
		}
		delete(cfg.Products, r.PathValue("name"))
		writeJSONorErr(w, state.ReplaceConfig(cfg))
	})

	mux.HandleFunc("POST /api/config/tiers", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name string `json:"name"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		cfg, _ := state.Snapshot()
		for _, t := range cfg.Tiers {
			if t == req.Name {
				writeJSON(w, map[string]string{"status": "already exists"})
				return
			}
		}
		cfg.Tiers = append(cfg.Tiers, req.Name)
		writeJSONorErr(w, state.ReplaceConfig(cfg))
	})

	mux.HandleFunc("DELETE /api/config/tiers/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		cfg, _ := state.Snapshot()
		_, accounts := state.Snapshot()
		for _, a := range accounts {
			if a.Tier == name {
				http.Error(w, "cannot delete: accounts still in this tier", http.StatusConflict)
				return
			}
		}
		kept := cfg.Tiers[:0]
		for _, t := range cfg.Tiers {
			if t != name {
				kept = append(kept, t)
			}
		}
		cfg.Tiers = kept
		writeJSONorErr(w, state.ReplaceConfig(cfg))
	})

	// replace the whole config (operations / products / tiers / limits / rules)
	mux.HandleFunc("PUT /api/config", func(w http.ResponseWriter, r *http.Request) {
		var cfg engine.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := state.ReplaceConfig(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// individual config mutations (nicer than whole-config PUT for small edits)
	mux.HandleFunc("POST /api/config/limits", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Remove *engine.Limit `json:"remove"`
			Upsert *engine.Limit `json:"upsert"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		cfg, _ := state.Snapshot()
		if req.Remove != nil {
			out := cfg.Limits[:0]
			for _, l := range cfg.Limits {
				if !(l.Level == req.Remove.Level && l.LimitKey == req.Remove.LimitKey && l.Scope == req.Remove.Scope) {
					out = append(out, l)
				}
			}
			cfg.Limits = out
		}
		if req.Upsert != nil {
			replaced := false
			for i := range cfg.Limits {
				if cfg.Limits[i].Level == req.Upsert.Level && cfg.Limits[i].LimitKey == req.Upsert.LimitKey && cfg.Limits[i].Scope == req.Upsert.Scope {
					cfg.Limits[i] = *req.Upsert
					replaced = true
				}
			}
			if !replaced {
				cfg.Limits = append(cfg.Limits, *req.Upsert)
			}
		}
		if err := state.ReplaceConfig(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/config/rules", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Remove *engine.Rule `json:"remove"`
			Upsert *engine.Rule `json:"upsert"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		cfg, _ := state.Snapshot()
		if req.Remove != nil {
			out := cfg.Rules[:0]
			for _, x := range cfg.Rules {
				if !(x.Level == req.Remove.Level && x.Kind == req.Remove.Kind && x.Operation == req.Remove.Operation &&
					x.Metric == req.Remove.Metric && x.Period == req.Remove.Period && x.Scope == req.Remove.Scope) {
					out = append(out, x)
				}
			}
			cfg.Rules = out
		}
		if req.Upsert != nil {
			replaced := false
			for i := range cfg.Rules {
				x := &cfg.Rules[i]
				if x.Level == req.Upsert.Level && x.Kind == req.Upsert.Kind && x.Operation == req.Upsert.Operation &&
					x.Metric == req.Upsert.Metric && x.Period == req.Upsert.Period && x.Scope == req.Upsert.Scope {
					*x = *req.Upsert
					replaced = true
				}
			}
			if !replaced {
				cfg.Rules = append(cfg.Rules, *req.Upsert)
			}
		}
		if err := state.ReplaceConfig(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// accounts
	mux.HandleFunc("POST /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		var a engine.Account
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := state.UpsertAccount(a); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("DELETE /api/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		state.DeleteAccount(r.PathValue("id"))
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/usage/reset", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"accountId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		state.ResetUsage(req.AccountID)
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// simulate a transaction: dry-run (commit=false, default) or commit=true
	mux.HandleFunc("POST /api/simulate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"accountId"`
			Operation string `json:"operation"`
			Amount    int64  `json:"amount"`
			Commit    bool   `json:"commit"`
			// optional simulated clock: "2026-08-14" or full RFC3339
			When string `json:"when"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		t := engine.Txn{AccountID: req.AccountID, Operation: req.Operation, Amount: req.Amount}
		if req.When != "" {
			wt, err := parseWhen(req.When)
			if err != nil {
				http.Error(w, "bad when: "+err.Error(), http.StatusBadRequest)
				return
			}
			t.When = wt
		}
		d := state.Evaluate(t, req.Commit)
		whenLabel := d.Summary.When
		state.Log(engine.ActivityEntry{
			When: whenLabel, AccountID: req.AccountID, Operation: req.Operation,
			Amount: req.Amount, Committed: req.Commit, Allowed: d.Allowed, Reason: d.Reason,
			Recorded: time.Now().Format(time.RFC3339),
		})
		writeJSON(w, d)
	})

	// reset the whole world back to the seeded PDF scenarios
	mux.HandleFunc("POST /api/reset", func(w http.ResponseWriter, r *http.Request) {
		state.Reset()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// clear the world to day 0: nothing configured, build from scratch
	mux.HandleFunc("POST /api/reset-blank", func(w http.ResponseWriter, r *http.Request) {
		state.ResetBlank()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// doc walkthrough scenarios (§0 Aisha's story, §6.5, §6.7 Q&A)
	mux.HandleFunc("GET /api/scenarios", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, engine.Scenarios())
	})

	// serve the frontend
	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "../web"
	}
	if _, err := os.Stat(webDir + "/index.html"); err != nil {
		log.Printf("note: %s/index.html not found — set WEB_DIR", webDir)
	} else {
		mux.Handle("/", http.FileServer(http.Dir(webDir)))
	}

	addr := "localhost:8080"
	log.Printf("Limit & Rule Configuration simulator — http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func parseWhen(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errBadWhen(s)
}

type whenError struct{ s string }

func (e *whenError) Error() string { return `unrecognized time "` + e.s + `" — use YYYY-MM-DD or RFC3339` }

func errBadWhen(s string) error { return &whenError{s} }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONorErr(w http.ResponseWriter, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

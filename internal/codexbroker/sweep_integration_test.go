//go:build integration

package codexbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSweepIntegration_RealBroker_DryRunThenApply — spawns a real
// app-server-broker.mjs against a tmpdir cwd, runs a dry-run sweep against
// a freshly-constructed SweepHandler, asserts the broker is classified as
// foreign_quarantine + AnomalyForeignOwner (registry empty in test setup),
// then confirms unfiltered apply issues ZERO kills (mass-kill safety),
// then runs filtered apply&brokerKey to verify the operator-override path
// produces an audit dump.
//
// Coverage split per round-4 finding #1: alive-protection invariant is NOT
// covered here because making predicate A true would require submitting a
// real codex thread/job through the broker's RPC, which is out of P2
// integration test budget. That invariant is fully covered by unit tests
// (TestSweepHandler_BrokerKeyApply_AlivePredicate_NoKill +
// TestSweepHandler_BrokerKeyApply_IdleNotExpired_NoKill).
//
// Build tag `integration` so this file is excluded from the default test
// run. Invoke via:
//
//	go test -tags=integration ./internal/codexbroker/...
//
// Requires `node` on PATH and the codex broker script discoverable at one
// of the well-known plugin install paths.
func TestSweepIntegration_RealBroker_DryRunThenApply(t *testing.T) {
	brokerScript := findBrokerScriptForIntegration(t)
	if brokerScript == "" {
		t.Skip("integration test skipped: app-server-broker.mjs not found")
	}

	cwdDir := t.TempDir()
	stateRoot := t.TempDir()
	auditDir := filepath.Join(stateRoot, "audit")
	if err := os.MkdirAll(auditDir, 0o755); err != nil {
		t.Fatalf("audit dir: %v", err)
	}

	// Spawn the broker against the tmp cwd.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", brokerScript, "serve", "--cwd", cwdDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // own pgrp so kill-pgid works
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn broker: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()
	})

	// Wait for the broker to start producing state. We poll ps for our pid
	// and a `app-server-broker.mjs` cmdline as the readiness signal.
	if !waitForBrokerLive(cmd.Process.Pid, 10*time.Second) {
		t.Fatalf("broker did not appear in ps within 10s")
	}
	// Give the broker an extra moment to write its state dir + broker.json
	// so the scanner picks it up on the first dry-run.
	time.Sleep(2 * time.Second)

	// Build a SweepHandler against the live process. We use the scanner's
	// production path so the integration test exercises the real argv parse
	// + cwd resolution; registry is empty (P2 mlab steady state).
	// Use the production plugin data root because the spawned broker writes
	// state into the canonical ~/.claude/plugins/data/codex-openai-codex/...
	// path; pointing the scanner at our tmpdir would never find it.
	prodRoot, err := PluginDataRoot()
	if err != nil {
		t.Fatalf("PluginDataRoot: %v", err)
	}
	prodSocketRoots, _ := SocketGlobRoots()
	scanner := NewScanner(ScannerOpts{
		FS:             NewOsFS(),
		Lister:         NewPsLister(),
		PluginDataRoot: prodRoot,
		SocketRoots:    prodSocketRoots,
	})
	handler := &SweepHandler{
		ScanFn: func(ctx context.Context) ([]BrokerRecord, error) {
			res, err := scanner.Scan(ctx)
			if err != nil {
				return nil, err
			}
			return res.Brokers, nil
		},
		EvalFn: EvalDecision,
		KillerFactory: func(rec BrokerRecord) KillRunner {
			return &KillSequence{
				Rec:       rec,
				Lister:    NewPsLister(),
				FS:        NewOsFS(),
				Signaller: realSignaller{},
				AuditDir:  auditDir,
				// Tighter budgets so the integration test stays fast.
				GracefulTimeout: 1 * time.Second,
				TermTimeout:     2 * time.Second,
				KillTimeout:     2 * time.Second,
			}
		},
		Quarantine: &QuarantineFile{Version: 1},
		Registry:   &LaunchRegistryFile{Version: 1}, // empty — mlab pre-spawn-hook
		E1Tracker:  NewE1Tracker(),
		BaseDecisionOpts: DecisionOpts{
			FS:     NewOsFS(),
			Lister: NewPsLister(),
		},
	}

	// 1. Dry-run.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run", nil)
	handler.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200", w.Code)
	}
	var dryResp SweepResponse
	if err := json.NewDecoder(w.Body).Decode(&dryResp); err != nil {
		t.Fatal(err)
	}
	// Find our broker in the evaluated list. We match on cwd suffix because
	// macOS EvalSymlinks rewrites /var → /private/var. Suffix-match on the
	// final path component is robust against that.
	resolvedCwd, _ := filepath.EvalSymlinks(cwdDir)
	var ourKey string
	for _, ev := range dryResp.Evaluated {
		if ev.Broker.Cwd == cwdDir ||
			ev.Broker.CwdResolved == cwdDir ||
			ev.Broker.CwdResolved == resolvedCwd ||
			(resolvedCwd != "" && strings.HasSuffix(ev.Broker.Cwd, resolvedCwd)) ||
			(strings.HasSuffix(ev.Broker.Cwd, filepath.Base(cwdDir))) {
			ourKey = ev.Broker.Key
			if ev.Decision.Reason != "foreign_quarantine" {
				t.Errorf("expected reason=foreign_quarantine for empty-registry broker, got %q", ev.Decision.Reason)
			}
			if !containsAnomalyCode(ev.Decision.AnomaliesAdded, AnomalyForeignOwner) {
				t.Errorf("expected AnomalyForeignOwner in AnomaliesAdded, got %v", ev.Decision.AnomaliesAdded)
			}
			break
		}
	}
	if ourKey == "" {
		// PR review finding G: the spawned broker NOT appearing in the
		// scanner inventory IS a Task S regression scenario — the whole
		// point of this integration test is to catch breakage in the
		// scanner discovery + production wiring path. Skipping here masked
		// real regressions.
		//
		// Allow opt-out via PDX_INTEGRATION_SKIP_ON_DISCOVERY=1 for CI
		// machines where a codex broker can be spawned but its state-dir
		// genuinely lives outside the canonical PluginDataRoot (e.g.
		// container sandboxes). Default behaviour is t.Fatalf.
		if os.Getenv("PDX_INTEGRATION_SKIP_ON_DISCOVERY") == "1" {
			t.Skipf("scanner-discovery skip requested via env: spawned broker (cwd=%s) not in evaluated list", cwdDir)
		}
		t.Fatalf("REGRESSION: spawned broker (cwd=%s) did not appear in scanner inventory; "+
			"this indicates a break in scanner discovery, production wiring, or PluginDataRoot resolution. "+
			"Set PDX_INTEGRATION_SKIP_ON_DISCOVERY=1 to opt out on environments where the broker lives outside the canonical data root.",
			cwdDir)
	}

	// Kill the broker externally so it becomes process-dead. The state-dir
	// will keep the broker classified as foreign (no registry entry), and
	// baseline Kill should be true for the dead process.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	_ = cmd.Wait()
	time.Sleep(500 * time.Millisecond)

	// 2. Mass-kill safety: unfiltered mode=apply must NOT kill.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply", nil)
	handler.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unfiltered apply status = %d, want 200", w.Code)
	}
	var unfilteredResp SweepResponse
	if err := json.NewDecoder(w.Body).Decode(&unfilteredResp); err != nil {
		t.Fatal(err)
	}
	if len(unfilteredResp.Applied) != 0 {
		t.Errorf("MASS-KILL: unfiltered apply applied %d brokers; expected zero", len(unfilteredResp.Applied))
	}

	// 3. Operator override: filtered apply on the foreign brokerKey.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey="+ourKey, nil)
	handler.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("filtered apply status = %d body=%s", w.Code, w.Body.String())
	}
	var filteredResp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&filteredResp)
	// Audit reason must always flip to manual_sweep_override on filtered
	// apply against a foreign-quarantine broker, regardless of kill outcome.
	found := false
	for _, ev := range filteredResp.Evaluated {
		if ev.Broker.Key == ourKey && ev.Decision.Reason == "manual_sweep_override" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected manual_sweep_override audit reason for filtered apply on %s; got evaluated=%+v", ourKey, filteredResp.Evaluated)
	}

	// PR review finding G: the post-kill outcome MUST satisfy at least one
	// of two acceptance branches; previously only the audit-reason flip was
	// asserted, masking regressions in scanner discovery, real apply,
	// Step 0 behaviour, and audit creation.
	//
	//   Branch 1 (kill applied): rec.Key in resp.Applied, an
	//   audit/orphan-<key>-<unixTs>.json file exists with the
	//   "manual_sweep_override" reason, and the cxc-* socket dir is
	//   removed (or the response surfaces a defer reason in Errors).
	//
	//   Branch 2 (identity mismatch quarantined): rec.Key in
	//   resp.Quarantined — Step 0 saw a drift between scanner snapshot
	//   and live ps row (PID-reuse race). The handler must have appended
	//   to the in-memory quarantine. (Audit file may or may not exist
	//   depending on Step 0 sequence.)
	branch1Applied := false
	for _, k := range filteredResp.Applied {
		if k == ourKey {
			branch1Applied = true
		}
	}
	branch2Quarantined := false
	for _, k := range filteredResp.Quarantined {
		if k == ourKey {
			branch2Quarantined = true
		}
	}
	switch {
	case branch1Applied:
		// Verify audit dump exists with the manual_sweep_override reason.
		entries, _ := os.ReadDir(auditDir)
		auditFound := false
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "orphan-"+ourKey+"-") {
				continue
			}
			body, rerr := os.ReadFile(filepath.Join(auditDir, e.Name()))
			if rerr != nil {
				t.Errorf("audit read: %v", rerr)
				continue
			}
			if !strings.Contains(string(body), "manual_sweep_override") {
				t.Errorf("audit file %s does not contain manual_sweep_override reason; body=%s", e.Name(), string(body))
			}
			auditFound = true
		}
		if !auditFound {
			t.Errorf("REGRESSION: branch 1 (kill applied) but no orphan-%s-*.json audit file in %s", ourKey, auditDir)
		}
	case branch2Quarantined:
		// Identity-mismatch path. No further on-disk assertion required at
		// this level; the unit test
		// TestSweepHandler_IdentityMismatch_PersistsQuarantine covers the
		// in-memory + disk semantics of the quarantine append.
	default:
		t.Fatalf("REGRESSION: filtered apply on %s neither killed (Applied=%v) nor quarantined (Quarantined=%v) — Errors=%v",
			ourKey, filteredResp.Applied, filteredResp.Quarantined, filteredResp.Errors)
	}
}

// findBrokerScriptForIntegration searches the well-known codex plugin paths
// for app-server-broker.mjs. Returns "" if none is reachable; callers
// should t.Skip in that case so the test is not falsely flagged as failed.
func findBrokerScriptForIntegration(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(home, ".claude/plugins/cache/openai-codex/codex/1.0.2/scripts/app-server-broker.mjs"),
		filepath.Join(home, ".claude/plugins/marketplaces/openai-codex/plugins/codex/scripts/app-server-broker.mjs"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	// Fall back to any version under cache/openai-codex/codex/<v>/scripts/.
	matches, _ := filepath.Glob(filepath.Join(home, ".claude/plugins/cache/openai-codex/codex/*/scripts/app-server-broker.mjs"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// waitForBrokerLive polls ps until our pid appears with a broker cmdline.
func waitForBrokerLive(pid int, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	lister := NewPsLister()
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		rows, err := lister.List(ctx)
		cancel()
		if err == nil {
			for _, row := range rows {
				if row.PID == pid && strings.Contains(row.Cmdline, "app-server-broker.mjs") {
					return true
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func containsAnomalyCode(slice []AnomalyCode, want AnomalyCode) bool {
	for _, a := range slice {
		if a == want {
			return true
		}
	}
	return false
}

// _ keeps imports stable.
var _ = errors.New

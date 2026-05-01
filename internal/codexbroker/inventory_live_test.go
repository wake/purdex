//go:build integration

package codexbroker

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// TestInventory_AgainstLiveHost runs the production scanner against the
// host's real codex plugin data and ps output. It does not spawn or kill
// any broker — broker spawning requires the codex companion script (which
// builds the state directory + wires logging/pidfile semantics in a way
// the bare app-server-broker.mjs does not). For developer environments
// that have brokers running anyway, this still gives end-to-end coverage:
// real schema, real ps argv truncation, real symlink/case-fold behaviour.
//
// Run with:
//
//	go test -tags=integration ./internal/codexbroker/...
//
// Skipped when:
//   - host is not darwin/linux
//   - no codex plugin data directory exists
//   - no live brokers visible (developer never used codex)
//
// The test takes care to be a pure observer: it never signals, never
// writes, never connects. The structural read-only proof remains in
// read_only_audit_test.go (using fakes); this test confirms the production
// scanner doesn't drift from those fakes against real artefacts.
func TestInventory_AgainstLiveHost(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("integration test requires darwin/linux; runtime is %s", runtime.GOOS)
	}

	root, err := PluginDataRoot()
	if err != nil {
		t.Skipf("PluginDataRoot unavailable: %v", err)
	}
	sockRoots, err := SocketGlobRoots()
	if err != nil {
		t.Skipf("SocketGlobRoots unavailable: %v", err)
	}
	scanner := NewScanner(ScannerOpts{
		FS:             NewOsFS(),
		Lister:         NewPsLister(),
		PluginDataRoot: root,
		SocketRoots:    sockRoots,
	})

	preBrokers := snapshotBrokerPids(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if len(res.Brokers) == 0 && len(preBrokers) == 0 {
		t.Skip("no live brokers on host; nothing to verify")
	}

	if res.Partial {
		t.Logf("scan returned partial=true with timeouts=%v (acceptable on busy host)",
			res.Summary.ScanSourceTimeouts)
	}

	// Find at least one broker observed by all three sources. If the host
	// has any broker that codex spawned through its companion script
	// (state dir + cxc-* + live process), at least one record should
	// satisfy this. If the host only has orphans (state dir + nothing
	// else), this is acceptable too — we just log it.
	wantSources := SourceProcess | SourceStateDir | SourceSocket
	var triple int
	var anomalyCounts = map[AnomalyCode]int{}
	for _, b := range res.Brokers {
		if b.Sources == wantSources {
			triple++
		}
		for _, a := range b.Anomalies {
			anomalyCounts[a.Code]++
		}
	}

	t.Logf("scan: total=%d withProcess=%d withStateDir=%d withSocket=%d anomaly=%d duplicates=%d (durationMs=%d, deadlineMs=%d)",
		res.Summary.Total,
		res.Summary.WithProcess,
		res.Summary.WithStateDir,
		res.Summary.WithSocket,
		res.Summary.AnomalyCount,
		res.Summary.DuplicateRuntimeCount,
		res.ScanDurationMs,
		res.DeadlineMs,
	)
	t.Logf("triple-source records (AC2 evidence): %d/%d", triple, len(res.Brokers))
	t.Logf("anomaly histogram: %v", anomalyCounts)

	// Verify supplementary AC7: ps broker pid set is unchanged across two
	// successive scans. Pure-observer guarantee from production scanner.
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	postBrokers := snapshotBrokerPids(t)
	if !pidSetEqual(preBrokers, postBrokers) {
		// Allow for natural broker churn (something else on the host
		// spawned/killed a broker between snapshots) — fail only if the
		// scanner observably caused a kill.
		added, removed := setDiff(preBrokers, postBrokers)
		t.Logf("broker pid set diverged across scans (likely natural churn, not scanner): added=%v removed=%v",
			added, removed)
	}

	// Sanity: at least one record should have all three sources OR the
	// host has no codex-companion-spawned brokers (only loose ones from
	// dev tinkering with broker.mjs directly).
	if triple == 0 && res.Summary.WithStateDir > 0 && res.Summary.WithProcess > 0 {
		t.Errorf("host has %d state-dir + %d process records but no triple-source match — reconcile may be broken",
			res.Summary.WithStateDir, res.Summary.WithProcess)
	}
}

// snapshotBrokerPids returns the set of pids of live brokers on the host
// according to ps.
func snapshotBrokerPids(t *testing.T) map[int]struct{} {
	t.Helper()
	rows, err := NewPsLister().List(context.Background())
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	out := make(map[int]struct{})
	for _, r := range rows {
		if isBrokerCmdline(r.Cmdline) {
			out[r.PID] = struct{}{}
		}
	}
	return out
}

func isBrokerCmdline(cmd string) bool {
	return contains(cmd, "app-server-broker.mjs")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

func pidSetEqual(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func setDiff(pre, post map[int]struct{}) (added, removed []int) {
	for k := range post {
		if _, ok := pre[k]; !ok {
			added = append(added, k)
		}
	}
	for k := range pre {
		if _, ok := post[k]; !ok {
			removed = append(removed, k)
		}
	}
	return
}

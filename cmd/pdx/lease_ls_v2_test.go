package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

var lsNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func lsSnapshot() resources.Snapshot {
	snap := fakeSnapshot()
	snap.Host.Zone, snap.Host.Trend = "warning", "rising"
	snap.GapUntil = lsNow.Add(12 * time.Second).UnixMilli()
	snap.Waiters = []resources.WaiterView{
		{ID: "a", Kind: "build", Weight: 35, SessionID: "cc-sid-member-1", Position: 1, WaitedS: 95, DeadlineInS: 445,
			Reason: "gap", ReasonSince: lsNow.Add(-40 * time.Second).UnixMilli()},
		{ID: "b", Kind: "test-full", Weight: 35, SessionID: "0123456789abcdef", Position: 2, WaitedS: 30, DeadlineInS: 510,
			Reason: "behind", ReasonSince: lsNow.Add(-30 * time.Second).UnixMilli()},
	}
	return snap
}

func withLeaseNow(t *testing.T) {
	old := leaseNow
	leaseNow = func() time.Time { return lsNow }
	t.Cleanup(func() { leaseNow = old })
}

// The status line says the zone, where the usage is heading and the gap left;
// the queue shows each waiter's reason in Chinese with how long it has lasted.
func TestLeaseLs_V2HeaderAndReasons(t *testing.T) {
	withLeaseNow(t)
	var out, errOut bytes.Buffer
	require.Equal(t, ExitOK, printLeaseTable(&out, &errOut, lsSnapshot()))
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, "主機：警戒 · 用量上升 · 間隔剩 12s", lines[0])
	assert.Contains(t, lines[1], "host  76/100", "the old host line follows")
	got := out.String()
	assert.Contains(t, got, "REASON")
	assert.Contains(t, got, "間隔中 40s")
	assert.Contains(t, got, "排在後面 30s")
}

func TestLeaseLs_V2ReasonNames(t *testing.T) {
	now := lsNow
	for reason, want := range map[string]string{
		"gap": "間隔中", "danger": "危險區", "warning_flat": "警戒且持平", "rising": "用量上升",
		"behind": "排在後面", "cut": "等待基準", "parked": "保留中",
	} {
		assert.Equal(t, want+" 5s", waitReasonText(resources.WaiterView{Reason: reason, ReasonSince: now.Add(-5 * time.Second).UnixMilli()}, now), reason)
	}
	assert.Equal(t, "-", waitReasonText(resources.WaiterView{}, now), "before the first pass")
	assert.Equal(t, `"a\nb"`, waitReasonText(resources.WaiterView{Reason: "a\nb"}, now), "an unknown name cannot pass for structure")
}

func TestLeaseLs_V2HeaderVariants(t *testing.T) {
	snap := resources.Snapshot{Available: true, Host: resources.HostUse{Zone: "danger", Trend: "flat"}}
	assert.Equal(t, "主機：危險 · 用量持平 · 間隔剩 無", hostStatusLine(snap, lsNow))
	snap.GapUntil = lsNow.Add(-time.Second).UnixMilli()
	assert.Equal(t, "主機：危險 · 用量持平 · 間隔剩 無", hostStatusLine(snap, lsNow), "a gap that has ended")
	snap.GapUntil = lsNow.Add(100 * time.Millisecond).UnixMilli()
	assert.Equal(t, "主機：危險 · 用量持平 · 間隔剩 1s", hostStatusLine(snap, lsNow), "rounded up")
	snap.Host.Zone, snap.Host.Trend = "bogus", ""
	assert.Equal(t, "主機：未知 · 用量未知 · 間隔剩 1s", hostStatusLine(snap, lsNow))
}

// A daemon from before lease v2 sends no zone: no status line, and no REASON column.
func TestLeaseLs_NoZoneNoV2Lines(t *testing.T) {
	snap := fakeSnapshot()
	snap.Waiters = []resources.WaiterView{{ID: "a", Kind: "build", Weight: 35, Position: 1}}
	var out, errOut bytes.Buffer
	require.Equal(t, ExitOK, printLeaseTable(&out, &errOut, snap))
	assert.NotContains(t, out.String(), "主機：")
	assert.NotContains(t, out.String(), "REASON")
}

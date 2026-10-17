package fleetmod

import (
	"context"
	"sort"
	"strings"
	"time"

	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/wwd/wire"
)

// poolStaleAfter is how long a host's facts may stand still before its row reads offline. wwd samples every 5 s and polls its
// peers every 5 s, so a healthy remote host's facts are up to ~10 s old plus jitter; the SSH sources' 5 s would flap them.
const poolStaleAfter = 20 * time.Second

// topProcs is how many processes a host's slow part lists in each ranking.
const topProcs = 5

// readPool takes the pool from wwd once and brings every host row in line with its peer's facts (the pool source, spec §9).
// A host with no peer in the pool, an unreachable peer, or facts that stopped moving reads offline once they are stale, like
// a remote host over SSH; a wwd that cannot be asked leaves every row as it was until the rows go stale.
func (m *Measure) readPool(ctx context.Context, now time.Time) {
	pool, err := m.d.Pool(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.d.Logf("[fleet] reading the pool: %v", err)
	}
	for _, key := range m.order {
		h := m.hosts[key]
		var peer *wire.Peer
		if err == nil {
			peer = peerOfAlias(pool, h.fleetAlias)
		}
		if peer != nil && peer.Reachable && peer.Facts != nil {
			m.applyFactsLocked(h, peer.Facts, now)
		}
		// No new reading for a while: the row reads offline too (it keeps the last values), never ok on an old figure.
		if !h.ownSeen.IsZero() && now.Sub(h.ownSeen) >= poolStaleAfter && h.status == leaf.StatusOK {
			h.status, h.code = leaf.StatusOffline, leaf.CodeTimeout
			h.zone.Reset()
			if h.fast != nil {
				h.fast.Zone = "unknown"
			}
		}
	}
}

func peerOfAlias(p wire.Pool, alias string) *wire.Peer {
	for i := range p.Peers {
		if strings.EqualFold(p.Peers[i].Info.Alias, alias) {
			return &p.Peers[i]
		}
	}
	return nil
}

// applyFactsLocked turns one host's facts into its fast and slow parts when they are newer than the last seen (m.mu held).
// now is the receive time, the same clock every other source stamps its readings with.
func (m *Measure) applyFactsLocked(h *hostRow, f *wire.Facts, now time.Time) {
	if f.At.Equal(h.ownAt) { // the same sample again; any other stamp, an earlier one too, is a new reading (a clock that stepped back must not lock the row)
		return
	}
	h.ownAt, h.ownSeen = f.At, now
	h.os = osWord(f.Platform)
	var diskPct, diskFree float64
	if d := mainDisk(f.Disk); d != nil {
		diskPct, diskFree = d.UsedPct, d.FreeGB
	}
	lf := leaf.LocalFast{At: now, CPU: clamp(100 - f.CPU.IdlePct), Mem: usedMemPct(f.Mem), Level: f.Mem.Pressure,
		Load1: f.CPU.Load1, NCPU: f.CPU.NCPU}
	if f.Mem.PageOutRate != nil {
		lf.PageRate = *f.Mem.PageOutRate
	}
	h.lastFast = lf
	h.fast = &FastView{At: now, CPU: lf.CPU, Mem: lf.Mem, Level: lf.Level, PageRate: lf.PageRate, Disk: diskPct, DiskFreeGB: diskFree,
		Load1: lf.Load1, NCPU: lf.NCPU, Zone: string(f.Derived.Zone), Load: leaf.Composite(lf, diskPct, true)}
	h.slow = &SlowView{At: now, TopCPU: topBy(f.Procs, func(p wire.Proc) float64 { return p.CPUPct }),
		TopMem: topBy(f.Procs, func(p wire.Proc) float64 { return float64(p.MemMB) })}
	h.slowSeq++
	h.status, h.code, h.lastOK = leaf.StatusOK, leaf.CodeNone, now
	m.noteReadingLocked(now)
}

// osWord is the platform in the words a remote frame uses ("Darwin" / "Linux", "" for anything else).
func osWord(platform string) string {
	switch platform {
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	}
	return ""
}

// mainDisk is the root mount's disk, else the first one; nil when the host reports none.
func mainDisk(ds []wire.Disk) *wire.Disk {
	for i := range ds {
		if ds[i].Mount == "/" {
			return &ds[i]
		}
	}
	if len(ds) > 0 {
		return &ds[0]
	}
	return nil
}

func usedMemPct(m wire.Mem) float64 {
	if m.TotalMB <= 0 {
		return 0
	}
	return clamp(100 * (1 - float64(m.AvailableMB)/float64(m.TotalMB)))
}

func clamp(v float64) float64 { return min(max(v, 0), 100) }

// topBy is the first topProcs processes ranked by key, biggest first (never nil).
func topBy(ps []wire.Proc, key func(wire.Proc) float64) []leaf.Proc {
	sorted := append([]wire.Proc(nil), ps...)
	sort.SliceStable(sorted, func(i, j int) bool { return key(sorted[i]) > key(sorted[j]) })
	if len(sorted) > topProcs {
		sorted = sorted[:topProcs]
	}
	out := make([]leaf.Proc, 0, len(sorted))
	for _, p := range sorted {
		out = append(out, leaf.Proc{PID: p.PID, Name: p.Name, CPU: p.CPUPct, RSSMB: float64(p.MemMB), MemMB: float64(p.MemMB), MemKind: p.MemKind})
	}
	return out
}

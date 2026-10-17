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

// readPool takes the pool from wwd once and decides, per host row, whether the pool is its source: a host the pool lists reads
// from its peer's facts (an unreachable peer, or facts that stopped moving, read offline), a host it does not list is left to
// the SSH sessions and the LocalSource. A wwd that cannot be asked keeps the last membership for poolStaleAfter (the rows go
// stale by themselves meanwhile) and then lists nobody, so every row falls back.
func (m *Measure) readPool(ctx context.Context, now time.Time) {
	pool, err := m.d.Pool(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case err == nil:
		m.lastPoolOK = now
		m.inPool = map[string]bool{}
		for _, key := range m.order {
			m.inPool[key] = peerOfAlias(pool, m.hosts[key].fleetAlias) != nil
		}
	default:
		m.d.Logf("[fleet] reading the pool: %v", err)
		if m.lastPoolOK.IsZero() || now.Sub(m.lastPoolOK) >= poolStaleAfter {
			m.inPool = map[string]bool{}
		}
	}
	for _, key := range m.order {
		h := m.hosts[key]
		if !m.inPool[key] {
			m.releaseLocked(h)
			continue
		}
		if _, probing := m.sessions[key]; probing {
			continue // syncSessions stops the session right after this; the facts are taken on the next tick, so a last reading of the session cannot overwrite them
		}
		h.poolOwned = true
		if err == nil {
			if peer := peerOfAlias(pool, h.fleetAlias); peer != nil && peer.Reachable && peer.Facts != nil {
				m.applyFactsLocked(h, peer.Facts, now)
			}
		}
		if h.poolSeen.IsZero() {
			m.offlineLocked(h) // the pool lists the host but has nothing to read from it: never keep an old source's ok or error
		}
		// No new reading for a while: the row reads offline too (it keeps the last values), never ok on an old figure.
		if !h.poolSeen.IsZero() && now.Sub(h.poolSeen) >= poolStaleAfter && h.status == leaf.StatusOK {
			m.offlineLocked(h)
		}
	}
}

func (m *Measure) offlineLocked(h *hostRow) {
	h.status, h.code = leaf.StatusOffline, leaf.CodeTimeout
	h.zone.Reset()
	if h.fast != nil {
		h.fast.Zone = "unknown"
	}
}

// releaseLocked hands a row the pool was reading back to the old sources: it reads offline until one of them reads it (m.mu held).
func (m *Measure) releaseLocked(h *hostRow) {
	if !h.poolOwned {
		return
	}
	h.poolOwned = false
	h.poolAt, h.poolSeen = time.Time{}, time.Time{}
	m.offlineLocked(h)
}

// syncSessions brings the SSH sessions in line with the pool: a host with an ssh alias that the pool does not list is probed,
// one it lists is not. Run by the tick after readPool, so it follows a host joining or leaving the pool within a second.
func (m *Measure) syncSessions(ctx context.Context) {
	var stop []*session
	var start []desired
	for _, w := range m.want {
		if w.ssh == "" {
			continue
		}
		ls, running := m.sessions[w.key]
		switch {
		case running && m.inPool[w.key]:
			stop = append(stop, ls.s)
			delete(m.sessions, w.key)
		case !running && !m.inPool[w.key]:
			start = append(start, w)
		}
	}
	m.stopSessions(stop)
	m.startSessions(ctx, start)
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
	if f.At.Equal(h.poolAt) { // the same sample again; any other stamp, an earlier one too, is a new reading (a clock that stepped back must not lock the row)
		return
	}
	h.poolAt, h.poolSeen = f.At, now
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

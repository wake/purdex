package facts

import "github.com/wake/purdex/internal/wwd/wire"

// Clone returns a copy of f that shares no pointer or slice with it. The ring
// stores and returns clones so history cannot be rewritten through an alias.
// (TestCloneCoversEveryReferenceField fails when a field is added to the
// contract and not copied here.)
func Clone(f wire.Facts) wire.Facts {
	out := f
	out.CPU = cloneCPU(f.CPU)
	out.Mem = cloneMem(f.Mem)
	out.Disk = cloneSlice(f.Disk)
	out.Procs = make([]wire.Proc, len(f.Procs))
	for i, p := range f.Procs {
		p.Owner = clonePtr(p.Owner)
		out.Procs[i] = p
	}
	if f.Procs == nil {
		out.Procs = nil
	}
	out.Residents = cloneSlice(f.Residents)
	out.Tools = wire.Tools{
		Go: clonePtr(f.Tools.Go), Node: clonePtr(f.Tools.Node), Pnpm: clonePtr(f.Tools.Pnpm),
		PHP: cloneSlice(f.Tools.PHP), Xcode: clonePtr(f.Tools.Xcode), Claude: clonePtr(f.Tools.Claude),
	}
	out.Owner = clonePtr(f.Owner)
	if f.Stats != nil {
		st := *f.Stats
		st.GateWaitingMax = clonePtr(st.GateWaitingMax)
		out.Stats = &st
	}
	out.Queue = clonePtr(f.Queue)
	out.Worker.Waiting = clonePtr(f.Worker.Waiting)
	out.ProcsTotal, out.BootAt = clonePtr(f.ProcsTotal), clonePtr(f.BootAt)
	if f.Self != nil {
		self := wire.Self{CPUPct: clonePtr(f.Self.CPUPct), RSSMB: clonePtr(f.Self.RSSMB)}
		out.Self = &self
	}
	if f.DiskIO != nil {
		out.DiskIO = make([]wire.DiskIO, len(f.DiskIO))
		for i, d := range f.DiskIO {
			out.DiskIO[i] = cloneDiskIO(d)
		}
	}
	out.Exception = make([]wire.FactExc, len(f.Exception))
	for i, e := range f.Exception {
		e.Until = clonePtr(e.Until)
		if e.Allow != nil {
			a := wire.HoldAllow{Classes: cloneSlice(e.Allow.Classes), Repos: cloneSlice(e.Allow.Repos)}
			e.Allow = &a
		}
		e.Flags = cloneSlice(e.Flags)
		out.Exception[i] = e
	}
	if f.Exception == nil {
		out.Exception = nil
	}
	return out
}

func cloneCPU(c wire.CPU) wire.CPU {
	c.Load15, c.UserPct, c.SystemPct = clonePtr(c.Load15), clonePtr(c.UserPct), clonePtr(c.SystemPct)
	if c.Cores != nil {
		cores := make([]wire.Core, len(c.Cores))
		for i, k := range c.Cores {
			k.UserPct, k.SystemPct, k.IdlePct, k.Kind = clonePtr(k.UserPct), clonePtr(k.SystemPct), clonePtr(k.IdlePct), clonePtr(k.Kind)
			cores[i] = k
		}
		c.Cores = cores
	}
	return c
}

func cloneDiskIO(d wire.DiskIO) wire.DiskIO {
	d.ReadBps, d.WriteBps = clonePtr(d.ReadBps), clonePtr(d.WriteBps)
	d.ReadIOPS, d.WriteIOPS = clonePtr(d.ReadIOPS), clonePtr(d.WriteIOPS)
	return d
}

func cloneMem(m wire.Mem) wire.Mem {
	m.PageOutRate, m.SwapOutRate = clonePtr(m.PageOutRate), clonePtr(m.SwapOutRate)
	m.SwapUsedMB, m.CompressorMB = clonePtr(m.SwapUsedMB), clonePtr(m.CompressorMB)
	m.PSI = clonePtr(m.PSI)
	m.DecompressRate = clonePtr(m.DecompressRate)
	m.SwapTotalMB, m.WiredMB, m.ActiveMB = clonePtr(m.SwapTotalMB), clonePtr(m.WiredMB), clonePtr(m.ActiveMB)
	m.InactiveMB, m.FileCacheMB = clonePtr(m.InactiveMB), clonePtr(m.FileCacheMB)
	return m
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneSlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	return append([]T{}, s...)
}

import { describe, it, expect } from 'vitest'
import { parseFleetPush } from './parse'

describe('top procs: mem_mb / mem_kind (#3057)', () => {
  const slowProcs = (mem: unknown[]) => {
    const p = parseFleetPush({
      term: 1, primary: 'air26',
      hosts: [{ id: 'air26:def456', kind: 'daemon', alias: 'air26', status: 'ok', slow: { at: '2026-10-16T12:00:00Z', top_cpu: [], top_mem: mem } }],
    })
    return p?.hosts[0].slow?.top_mem
  }
  const base = { pid: 1, name: 'a', cpu: 1, rss_mb: 10 }

  it('reads mem_mb and mem_kind', () => {
    expect(slowProcs([{ ...base, mem_mb: 2048.5, mem_kind: 'footprint' }, { ...base, pid: 2, mem_mb: 5, mem_kind: 'rss' }])).toEqual([
      { ...base, mem_mb: 2048.5, mem_kind: 'footprint' }, { ...base, pid: 2, mem_mb: 5, mem_kind: 'rss' },
    ])
  })
  it('drops a bad mem_kind and a non-number mem_mb, keeping the proc', () => {
    expect(slowProcs([{ ...base, mem_mb: '9', mem_kind: 'swap' }, { ...base, pid: 2, mem_mb: Infinity, mem_kind: 7 }])).toEqual([base, { ...base, pid: 2 }])
  })
  it('the old shape parses exactly as before', () => {
    expect(slowProcs([base])).toEqual([base])
  })
})

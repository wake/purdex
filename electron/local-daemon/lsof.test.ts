import { describe, expect, it } from 'vitest'
import { parseLsofF0, txtPaths, listenersOn, decideOwnership } from './lsof'

// lsof -F0pfn: each field is <letter><value>\0; a process set ends with \n.
const NUL = '\0'
const txtOut =
  `p7520${NUL}\n` +
  `ftxt${NUL}n/Users/wake/Workspace/wake/purdex/bin/pdx${NUL}\n` +
  `ftxt${NUL}n/usr/lib/dyld${NUL}\n`
const listenOut =
  `p7520${NUL}\n` +
  `f8${NUL}n100.64.0.2:7860${NUL}\n` +
  `p9999${NUL}\n` +
  `f5${NUL}n*:8080${NUL}\n`

describe('parseLsofF0', () => {
  it('groups NUL-terminated fields into processes and files', () => {
    const procs = parseLsofF0(txtOut)
    expect(procs).toEqual([{ pid: 7520, files: [
      { fd: 'txt', name: '/Users/wake/Workspace/wake/purdex/bin/pdx' },
      { fd: 'txt', name: '/usr/lib/dyld' },
    ] }])
  })
  it('handles several processes and empty output', () => {
    expect(parseLsofF0('')).toEqual([])
    expect(parseLsofF0(listenOut).map((p) => p.pid)).toEqual([7520, 9999])
  })
  it('tolerates a trailing set without newline', () => {
    expect(parseLsofF0(`p1${NUL}\nftxt${NUL}n/a${NUL}`)).toEqual([{ pid: 1, files: [{ fd: 'txt', name: '/a' }] }])
  })
})

describe('txtPaths / listenersOn', () => {
  it('returns every txt name for the pid', () => {
    expect(txtPaths(parseLsofF0(txtOut), 7520)).toEqual(['/Users/wake/Workspace/wake/purdex/bin/pdx', '/usr/lib/dyld'])
    expect(txtPaths(parseLsofF0(txtOut), 1)).toEqual([])
  })
  it('matches bind:port and *:port only', () => {
    const procs = parseLsofF0(listenOut)
    expect(listenersOn(procs, '100.64.0.2', 7860)).toEqual([7520])
    expect(listenersOn(procs, '127.0.0.1', 7860)).toEqual([])
    expect(listenersOn(procs, '127.0.0.1', 8080)).toEqual([9999])
  })
})

describe('decideOwnership (spec §3.1 step 3)', () => {
  const base = { candidatePid: 7520, candidateIsOurs: true, listenerPids: [7520], listenerBinaries: {}, binExists: true }
  it('ours + listener ours → managed, alive', () => {
    expect(decideOwnership(base)).toEqual({ managed: 'managed', alive: { pid: 7520 } })
  })
  it('ours + no listener → managed, alive (unhealthy, restart offered)', () => {
    expect(decideOwnership({ ...base, listenerPids: [] })).toEqual({ managed: 'managed', alive: { pid: 7520 } })
  })
  it('stale pid (not ours), no listener → managed, stopped', () => {
    expect(decideOwnership({ ...base, candidateIsOurs: false, listenerPids: [] })).toEqual({ managed: 'managed', alive: null })
  })
  it('listener owned by another pid → external with the pid', () => {
    const r = decideOwnership({ ...base, listenerPids: [4242] })
    expect(r.managed).toBe('external')
    expect((r as { reason: string }).reason).toBe('port is served by pid 4242')
  })
  it('foreign listener with a known binary names it', () => {
    const r = decideOwnership({ ...base, candidatePid: null, candidateIsOurs: false, listenerPids: [4242], listenerBinaries: { 4242: '/repo/bin/pdx' } })
    expect((r as { reason: string }).reason).toBe('running daemon is /repo/bin/pdx')
  })
  it('nothing alive, no binary → none', () => {
    expect(decideOwnership({ ...base, candidatePid: null, candidateIsOurs: false, listenerPids: [], binExists: false })).toEqual({ managed: 'none', alive: null })
  })
})

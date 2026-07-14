import { beforeEach, describe, expect, it } from 'vitest'
import {
  SNAPSHOT_KEY,
  SNAPSHOT_PREV_KEY,
  readSnapshot,
  writeSnapshot,
  readPrevSnapshot,
  writePrevSnapshot,
  setSessionMetaCwd,
} from './storage'
import type { SessionMeta, WorkspaceSnapshot } from './types'

function makeSnapshot(overrides?: Partial<WorkspaceSnapshot>): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: 1000,
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    workspaces: [],
    activeWorkspaceId: null,
    sessionMeta: {},
    ...overrides,
  }
}

describe('snapshot storage', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('round-trips a minimal valid snapshot', () => {
    const snap = makeSnapshot()
    writeSnapshot(snap)
    expect(readSnapshot()).toEqual(snap)
  })

  it('returns null when key is empty', () => {
    expect(readSnapshot()).toBeNull()
  })

  it('returns null (never throws) on malformed JSON', () => {
    localStorage.setItem(SNAPSHOT_KEY, '{not json')
    expect(() => readSnapshot()).not.toThrow()
    expect(readSnapshot()).toBeNull()
  })

  it('returns null when version is not 1', () => {
    localStorage.setItem(SNAPSHOT_KEY, JSON.stringify({ version: 2, capturedAt: 1000 }))
    expect(readSnapshot()).toBeNull()
  })

  describe('shape guard (version:1 but malformed → null, never crash downstream)', () => {
    // Each blob is version:1 (passes the version check) but corrupt in one basic
    // top-level field, so the read boundary must reject it rather than let it
    // flow into restore and crash AFTER daemon side effects.
    const base = () => makeSnapshot() as unknown as Record<string, unknown>

    const cases: Array<[string, Record<string, unknown>]> = [
      ['tabs missing', (() => { const b = base(); delete b.tabs; return b })()],
      ['tabs is an array', { ...base(), tabs: [] }],
      ['tabOrder not an array', { ...base(), tabOrder: {} }],
      ['workspaces not an array', { ...base(), workspaces: {} }],
      ['sessionMeta missing', (() => { const b = base(); delete b.sessionMeta; return b })()],
      ['activeTabId a number', { ...base(), activeTabId: 42 }],
      ['activeWorkspaceId a number', { ...base(), activeWorkspaceId: 7 }],
    ]

    for (const [label, blob] of cases) {
      it(`returns null when ${label}`, () => {
        localStorage.setItem(SNAPSHOT_KEY, JSON.stringify(blob))
        expect(readSnapshot()).toBeNull()
      })

      it(`readPrevSnapshot returns null when ${label}`, () => {
        localStorage.setItem(SNAPSHOT_PREV_KEY, JSON.stringify(blob))
        expect(readPrevSnapshot()).toBeNull()
      })
    }

    it('a fully well-formed snapshot still round-trips (guard does not reject valid data)', () => {
      const snap = makeSnapshot({
        activeTabId: 't1',
        activeWorkspaceId: 'ws1',
      })
      writeSnapshot(snap)
      expect(readSnapshot()).toEqual(snap)
    })
  })

  it('keeps prev snapshot independent from current snapshot', () => {
    const prev = makeSnapshot({ capturedAt: 1000 })
    const current = makeSnapshot({ capturedAt: 2000 })
    writePrevSnapshot(prev)
    writeSnapshot(current)
    expect(readPrevSnapshot()).toEqual(prev)
    expect(readSnapshot()).toEqual(current)
    expect(SNAPSHOT_KEY).not.toBe(SNAPSHOT_PREV_KEY)
  })
})

describe('setSessionMetaCwd', () => {
  function metaEntry(over: Partial<SessionMeta> & Pick<SessionMeta, 'hostId' | 'sessionCode' | 'name'>): SessionMeta {
    return { mode: 'terminal', restorable: true, ...over }
  }

  function snapWithMeta(sessionMeta: WorkspaceSnapshot['sessionMeta']): WorkspaceSnapshot {
    return makeSnapshot({ sessionMeta })
  }

  it('non-empty cwd sets cwd, restorable:true, clears captureError', () => {
    const snap = snapWithMeta({
      h1: { s1: metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/old' }) },
    })
    const next = setSessionMetaCwd(snap, 'h1', 's1', '/new/path')
    expect(next.sessionMeta.h1.s1).toMatchObject({
      cwd: '/new/path',
      restorable: true,
    })
    expect(next.sessionMeta.h1.s1.captureError).toBeUndefined()
  })

  it('trims surrounding whitespace from a non-empty cwd', () => {
    const snap = snapWithMeta({
      h1: { s1: metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'work' }) },
    })
    const next = setSessionMetaCwd(snap, 'h1', 's1', '  /trimmed  ')
    expect(next.sessionMeta.h1.s1.cwd).toBe('/trimmed')
  })

  it('non-empty cwd on a cwd-probe-failed / dead entry flips restorable true + clears captureError', () => {
    const snap = snapWithMeta({
      h1: {
        s1: metaEntry({
          hostId: 'h1',
          sessionCode: 's1',
          name: 'work',
          restorable: false,
          captureError: 'cwd-probe-failed',
        }),
      },
    })
    const next = setSessionMetaCwd(snap, 'h1', 's1', '/real/dir')
    expect(next.sessionMeta.h1.s1).toMatchObject({ cwd: '/real/dir', restorable: true })
    expect(next.sessionMeta.h1.s1.captureError).toBeUndefined()
  })

  it('empty string clears cwd, restorable:false, captureError undefined', () => {
    const snap = snapWithMeta({
      h1: { s1: metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x', restorable: true }) },
    })
    const next = setSessionMetaCwd(snap, 'h1', 's1', '')
    expect(next.sessionMeta.h1.s1.cwd).toBeUndefined()
    expect(next.sessionMeta.h1.s1.restorable).toBe(false)
    expect(next.sessionMeta.h1.s1.captureError).toBeUndefined()
  })

  it('whitespace-only cwd clears cwd, restorable:false', () => {
    const snap = snapWithMeta({
      h1: { s1: metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x', restorable: true }) },
    })
    const next = setSessionMetaCwd(snap, 'h1', 's1', '   \t  ')
    expect(next.sessionMeta.h1.s1.cwd).toBeUndefined()
    expect(next.sessionMeta.h1.s1.restorable).toBe(false)
    expect(next.sessionMeta.h1.s1.captureError).toBeUndefined()
  })

  it('does not mutate the input snapshot (new objects, original entry unchanged)', () => {
    const original = metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/old', restorable: true })
    const snap = snapWithMeta({ h1: { s1: original } })
    const next = setSessionMetaCwd(snap, 'h1', 's1', '/new')

    expect(next).not.toBe(snap)
    expect(next.sessionMeta).not.toBe(snap.sessionMeta)
    expect(next.sessionMeta.h1).not.toBe(snap.sessionMeta.h1)
    expect(next.sessionMeta.h1.s1).not.toBe(original)
    // Input entry untouched.
    expect(original.cwd).toBe('/old')
    expect(snap.sessionMeta.h1.s1.cwd).toBe('/old')
  })

  it('composite-key isolation: same code under a different host is untouched', () => {
    const snap = snapWithMeta({
      h1: { s1: metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/a' }) },
      h2: { s1: metaEntry({ hostId: 'h2', sessionCode: 's1', name: 'b', cwd: '/b' }) },
    })
    const next = setSessionMetaCwd(snap, 'h1', 's1', '/edited')
    expect(next.sessionMeta.h1.s1.cwd).toBe('/edited')
    // Different host, same code — reference and value preserved.
    expect(next.sessionMeta.h2).toBe(snap.sessionMeta.h2)
    expect(next.sessionMeta.h2.s1.cwd).toBe('/b')
  })

  it('composite-key isolation: sibling code under the same host is untouched', () => {
    const snap = snapWithMeta({
      h1: {
        s1: metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/a' }),
        s2: metaEntry({ hostId: 'h1', sessionCode: 's2', name: 'b', cwd: '/b' }),
      },
    })
    const before = snap.sessionMeta.h1.s2
    const next = setSessionMetaCwd(snap, 'h1', 's1', '/edited')
    expect(next.sessionMeta.h1.s1.cwd).toBe('/edited')
    expect(next.sessionMeta.h1.s2).toBe(before)
    expect(next.sessionMeta.h1.s2.cwd).toBe('/b')
  })

  it('unknown host → snapshot returned unchanged', () => {
    const snap = snapWithMeta({
      h1: { s1: metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/a' }) },
    })
    expect(setSessionMetaCwd(snap, 'nope', 's1', '/x')).toBe(snap)
  })

  it('unknown code under a known host → snapshot returned unchanged', () => {
    const snap = snapWithMeta({
      h1: { s1: metaEntry({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/a' }) },
    })
    expect(setSessionMetaCwd(snap, 'h1', 'nope', '/x')).toBe(snap)
  })
})

import { beforeEach, describe, expect, it } from 'vitest'
import {
  SNAPSHOT_KEY,
  SNAPSHOT_PREV_KEY,
  readSnapshot,
  writeSnapshot,
  readPrevSnapshot,
  writePrevSnapshot,
} from './storage'
import type { WorkspaceSnapshot } from './types'

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

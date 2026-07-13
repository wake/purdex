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

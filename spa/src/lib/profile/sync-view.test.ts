import { describe, it, expect } from 'vitest'
import { settingsWaitForWorkspaces, syncDotOf } from './sync-view'
import type { ProfileSyncSnapshot } from './start'
import type { ExecutorStatus } from './executor'

const status = (sections: ExecutorStatus['sections'], profile: ExecutorStatus['profile'] = 'pending'): ExecutorStatus => ({ profile, schemaLock: null, sections, locks: {} })
const snapshot = (over: Partial<ProfileSyncSnapshot> = {}): ProfileSyncSnapshot => ({
  master: { hostId: 'h1', profileId: 'p1' },
  leader: true,
  blocked: null,
  status: status({}, 'synced'),
  problems: [],
  remote: false,
  stale: false,
  ...over,
})

describe('syncDotOf — one reading of the whole master, for the switcher\'s dot and Settings › Profile alike', () => {
  it('no master → no dot', () => {
    expect(syncDotOf(snapshot({ master: null, status: null }))).toBeNull()
  })

  it.each([
    [{ blocked: 'suspended' as const }, 'syncing'],
    [{ blocked: 'profile-gone' as const }, 'problem'],
    [{ blocked: 'master-endpoint-changed' as const }, 'problem'],
    [{ status: null }, 'unknown'],
    [{ remote: true, stale: true }, 'unknown'],
    [{ status: status({}, 'locked:conflict') }, 'locked'],
    [{ status: status({}, 'locked:schema') }, 'locked'],
    [{ status: status({}, 'pending') }, 'syncing'],
    [{ status: status({}, 'synced') }, 'synced'],
    [{ status: status({}, 'idle') }, 'unknown'],
  ])('%j → %s', (over, dot) => {
    expect(syncDotOf(snapshot(over))).toBe(dot)
  })
})

describe('settingsWaitForWorkspaces — the executor\'s settings gate, as far as the published status shows it', () => {
  it.each(['locked:conflict', 'locked:reset', 'locked:invalid'] as const)('workspaces %s and settings with something to send → waiting', (lock) => {
    expect(settingsWaitForWorkspaces(status({ workspaces: lock, settings: 'pending' }))).toBe(true)
  })

  it('settings with nothing to send → not waiting, whatever workspaces is', () => {
    expect(settingsWaitForWorkspaces(status({ workspaces: 'locked:conflict', settings: 'synced' }))).toBe(false)
  })

  it('workspaces merely pending → not SAID to be waiting: that gate opens by itself in a moment', () => {
    expect(settingsWaitForWorkspaces(status({ workspaces: 'pending', settings: 'pending' }))).toBe(false)
    expect(settingsWaitForWorkspaces(status({ workspaces: 'synced', settings: 'pending' }))).toBe(false)
  })

  it('settings itself locked → that is its own story, not a wait', () => {
    expect(settingsWaitForWorkspaces(status({ workspaces: 'locked:conflict', settings: 'locked:conflict' }))).toBe(false)
  })

  it('a section that is not there, or no status at all → not waiting', () => {
    expect(settingsWaitForWorkspaces(status({ settings: 'pending' }))).toBe(false)
    expect(settingsWaitForWorkspaces(status({ workspaces: 'locked:reset' }))).toBe(false)
    expect(settingsWaitForWorkspaces(null)).toBe(false)
  })
})

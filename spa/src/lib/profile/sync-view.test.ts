import { describe, it, expect } from 'vitest'
import { describeSections, settingsWaitForWorkspaces, syncDotOf } from './sync-view'
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

describe('describeSections — the sections as a person reads them', () => {
  const master = [{ id: 'w2', name: 'Client work' }, { id: 'w1', name: 'Scratch' }]

  it('hosts, settings, workspaces — then the tabs in the MASTER world\'s workspace order, each by its workspace\'s name', () => {
    expect(describeSections(['tabs.w1', 'workspaces', 'tabs.w2', 'settings', 'hosts'], master)).toEqual([
      { key: 'hosts', kind: 'hosts' },
      { key: 'settings', kind: 'settings' },
      { key: 'workspaces', kind: 'workspaces' },
      { key: 'tabs.w2', kind: 'tabs', workspace: 'Client work' },
      { key: 'tabs.w1', kind: 'tabs', workspace: 'Scratch' },
    ])
  })

  it('tabs of a workspace this device has not seen: no name — NOT its id — and after the known ones', () => {
    expect(describeSections(['tabs.zz9', 'tabs.w1', 'tabs.aa0'], master)).toEqual([
      { key: 'tabs.w1', kind: 'tabs', workspace: 'Scratch' },
      { key: 'tabs.aa0', kind: 'tabs', workspace: null },
      { key: 'tabs.zz9', kind: 'tabs', workspace: null },
    ])
  })

  it('the master world cannot be read right now (null): tabs are there, none is named, none is called unseen', () => {
    expect(describeSections(['tabs.w1', 'hosts'], null)).toEqual([
      { key: 'hosts', kind: 'hosts' },
      { key: 'tabs.w1', kind: 'tabs', workspace: undefined },
    ])
  })

  it('a key of no known kind is kept, last, as it is', () => {
    expect(describeSections(['future.thing', 'hosts'], master)).toEqual([
      { key: 'hosts', kind: 'hosts' },
      { key: 'future.thing', kind: 'other' },
    ])
  })
})

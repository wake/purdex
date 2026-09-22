import { describe, it, expect } from 'vitest'
import { describeSections, profileIsGone, settingsWaitForWorkspaces, syncDotOf } from './sync-view'
import type { ProfileSyncSnapshot } from './start'
import type { ExecutorStatus } from './executor'

const status = (sections: ExecutorStatus['sections'], profile: ExecutorStatus['profile'] = 'pending'): ExecutorStatus => ({ profile, schemaLock: null, sections, locks: {}, profileGone: false, detail: {}, indexFailures: 0, lastSuccessAt: null })
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
  const failing = { rev: 1, failures: 2, retryAt: 5000, invalidReason: null }
  const fine = { rev: 1, failures: 0, retryAt: null, invalidReason: null }

  it.each(['locked:conflict', 'locked:reset', 'locked:invalid'] as const)('workspaces %s and settings with something to send → waiting: locked', (lock) => {
    expect(settingsWaitForWorkspaces(status({ workspaces: lock, settings: 'pending' }))).toBe('locked')
  })

  it('workspaces FAILING (its own requests, or the index read) and settings with something to send → waiting: failing', () => {
    expect(settingsWaitForWorkspaces({ ...status({ workspaces: 'pending', settings: 'pending' }), detail: { workspaces: failing } })).toBe('failing')
    expect(settingsWaitForWorkspaces({ ...status({ workspaces: 'synced', settings: 'pending' }), indexFailures: 1 })).toBe('failing')
  })

  it('locked AND failing → locked: that is the one the user can do something about', () => {
    expect(settingsWaitForWorkspaces({ ...status({ workspaces: 'locked:conflict', settings: 'pending' }), detail: { workspaces: failing }, indexFailures: 2 })).toBe('locked')
  })

  it('settings with nothing to send → not waiting, whatever workspaces is', () => {
    expect(settingsWaitForWorkspaces(status({ workspaces: 'locked:conflict', settings: 'synced' }))).toBeNull()
    expect(settingsWaitForWorkspaces({ ...status({ workspaces: 'pending', settings: 'synced' }), detail: { workspaces: failing }, indexFailures: 1 })).toBeNull()
  })

  it('workspaces merely pending, not failing → not SAID to be waiting: that gate opens by itself in a moment', () => {
    expect(settingsWaitForWorkspaces(status({ workspaces: 'pending', settings: 'pending' }))).toBeNull()
    expect(settingsWaitForWorkspaces(status({ workspaces: 'synced', settings: 'pending' }))).toBeNull()
    expect(settingsWaitForWorkspaces({ ...status({ workspaces: 'pending', settings: 'pending' }), detail: { workspaces: fine } })).toBeNull()
  })

  it('another section failing is not workspaces failing', () => {
    expect(settingsWaitForWorkspaces({ ...status({ workspaces: 'pending', settings: 'pending' }), detail: { hosts: failing, settings: failing } })).toBeNull()
  })

  it('F2: the profile gone (the index no longer lists it) → not waiting: nothing will ever open that gate', () => {
    const gone = {
      ...status({ workspaces: 'locked:conflict', settings: 'pending' }, 'locked:reset'),
      profileGone: true,
      detail: { workspaces: failing },
      indexFailures: 2,
    }
    expect(settingsWaitForWorkspaces(gone)).toBeNull()
    expect(settingsWaitForWorkspaces({ ...gone, sections: { workspaces: 'pending', settings: 'pending' } })).toBeNull()
  })

  it('settings itself locked → that is its own story, not a wait', () => {
    expect(settingsWaitForWorkspaces(status({ workspaces: 'locked:conflict', settings: 'locked:conflict' }))).toBeNull()
  })

  it('a section that is not there, or no status at all → not waiting', () => {
    expect(settingsWaitForWorkspaces(status({ settings: 'pending' }))).toBeNull()
    expect(settingsWaitForWorkspaces(status({ workspaces: 'locked:reset' }))).toBeNull()
    expect(settingsWaitForWorkspaces(null)).toBeNull()
  })
})

describe('profileIsGone — the two sources, read alike', () => {
  it('a 404 on the attachment (blocked) and the index no longer listing it (the executor) are both "gone"', () => {
    expect(profileIsGone(snapshot({ blocked: 'profile-gone' }))).toBe(true)
    expect(profileIsGone(snapshot({ status: { ...status({ hosts: 'synced' }, 'locked:reset'), profileGone: true } }))).toBe(true)
  })

  it('a reset that is not a gone profile, another block, or no status → not gone', () => {
    expect(profileIsGone(snapshot({ status: status({ hosts: 'locked:reset' }, 'locked:reset') }))).toBe(false)
    expect(profileIsGone(snapshot({ blocked: 'master-endpoint-changed' }))).toBe(false)
    expect(profileIsGone(snapshot({ status: null }))).toBe(false)
    expect(profileIsGone(snapshot({ master: null, status: null }))).toBe(false)
  })

  it('the dot reads the executor\'s gone profile as the 404 is read: a problem, not a lock', () => {
    expect(syncDotOf(snapshot({ status: { ...status({}, 'locked:reset'), profileGone: true } }))).toBe('problem')
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

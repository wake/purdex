// spa/src/lib/profile/phone-tab-group.fixture.test.ts — the phone keeps `group` / `groupRank` it did not write (task groups
// spec §4.2, TG-1b-1 task 4). iOS preserves unknown tab fields as JSON (purdex-ios `ProfileTab.adding`), so a Mac tab carrying
// both fields and a phone tab that came back carrying them must pass the Mac's guard, keep the fields on apply, and rebuild
// to the very section on the SOT. The section is the static fixture `__fixtures__/phone-tab-group.json` (hash pinned inside it,
// phtab1 copied byte for byte from the iOS-owned `phone-tab.json`, which stays untouched). It is Mac-authored: it is meant to be
// adopted as a shared case by purdex-ios's canonical-hash tests, which is how the phone side is pinned.
import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { PaneLayout, Tab } from '../../types/tab'
import groupFixtureJson from './__fixtures__/phone-tab-group.json'
import { applySectionToStores } from './apply-to-stores'
import { isWellFormedSection } from './applier'
import { buildSectionPayload } from './collector'
import { hashSection } from './hash'
import type { TabsPayload } from './types'

const fixture = groupFixtureJson as unknown as { hash: string; payload: TabsPayload }
const MAC_HOST = 'aaaaaa'
const DAEMON = 'mini-lab:278cbm'
const macTab: Tab = {
  id: 't1',
  pinned: true,
  locked: false,
  createdAt: 1789999999999,
  group: 'group\u0000aaaaaaaa',
  groupRank: 'V',
  layout: {
    type: 'leaf',
    pane: { id: 'p1', content: { kind: 'tmux-session', hostId: MAC_HOST, sessionCode: 'abc123', mode: 'terminal', cachedName: 'mac', tmuxInstance: '1:2' } },
  } as PaneLayout,
}

function loadMac(): void {
  useHostStore.setState({
    hosts: { [MAC_HOST]: { id: MAC_HOST, name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok', order: 0, daemonId: DAEMON } },
    hostOrder: [MAC_HOST], activeHostId: MAC_HOST, devHostId: null, runtime: {},
  })
  useTabStore.setState({ tabs: { t1: macTab }, tabOrder: ['t1'], activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W1', tabs: ['t1'], activeTabId: 't1' }], activeWorkspaceId: 'w1', worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
}

beforeEach(() => {
  localStorage.clear()
  loadMac()
})

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T

/** The fixture section: the Mac's tab t1 with both fields, the phone's own tab phtab1 (from the iOS fixture, no fields) and phtab2, a phone tab that came back carrying both. */
const stored = (): TabsPayload => clone(fixture.payload)

describe('phone tabs next to tabs that carry group / groupRank', () => {
  it('a. the Mac\'s own tab builds with both fields, and the whole section is well formed', () => {
    const p = stored()
    expect(p.tabs.t1).toMatchObject({ group: 'group\u0000aaaaaaaa', groupRank: 'V' })
    expect(isWellFormedSection('tabs', p)).toBe(true)
  })

  it('b. applying keeps the fields (the phone\'s tab that carries them included) and the phone\'s plain tab has none', async () => {
    const outcome = await applySectionToStores('tabs.w1', stored(), { masterHostId: MAC_HOST })
    expect(outcome.ok).toBe(true)
    const tabs = useTabStore.getState().tabs
    expect(tabs.t1).toMatchObject({ group: 'group\u0000aaaaaaaa', groupRank: 'V' })
    expect(tabs.phtab2).toMatchObject({ group: 'ungrouped', groupRank: 'a' })
    expect(Object.hasOwn(tabs.phtab1, 'group')).toBe(false)
    expect(Object.hasOwn(tabs.phtab1, 'groupRank')).toBe(false)
  })

  it('c. the Mac rebuilds the very section on the SOT, and its hash is the pinned one', async () => {
    const payload = stored()
    const outcome = await applySectionToStores('tabs.w1', clone(payload), { masterHostId: MAC_HOST })
    expect(outcome.ok).toBe(true)
    const rebuilt = buildSectionPayload('tabs.w1')!.payload
    expect(rebuilt).toEqual(payload)
    expect(await hashSection(rebuilt)).toBe(await hashSection(payload))
    expect(await hashSection(payload)).toBe(fixture.hash)
  })
})

// spa/src/lib/profile/phone-tab-group.fixture.test.ts — the phone keeps `group` / `groupRank` it did not write (task groups
// spec §4.2, TG-1b-1 task 4). iOS preserves unknown tab fields as JSON (purdex-ios `ProfileTab.adding`), so a Mac tab carrying
// both fields and a phone tab that came back carrying them must pass the Mac's guard, keep the fields on apply, and rebuild
// to the very section on the SOT. The iOS-owned fixture (`__fixtures__/phone-tab.json`, pinned by iOS) is used untouched.
import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { PaneLayout, Tab } from '../../types/tab'
import fixtureJson from './__fixtures__/phone-tab.json'
import { applySectionToStores } from './apply-to-stores'
import { isWellFormedSection } from './applier'
import { buildSectionPayload } from './collector'
import { hashSection } from './hash'
import type { TabsPayload } from './types'

const fixture = fixtureJson as unknown as { hash: string; payload: TabsPayload }
const MAC_HOST = 'aaaaaa'
const DAEMON = 'mini-lab:278cbm'
/** hashSection of the section below, pinned: a drift in what the hash covers fails here first. */
const PINNED_HASH = '1aa4cb70858a4973e02b84313aec8365caec64b5f37ccfdca82b5e321fe536fe'

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

/** The Mac's own tabs.w1 (t1 with both fields), the phone's tab appended as the fixture has it, and a second phone tab that carries both fields. */
function stored(): TabsPayload {
  const mac = clone(buildSectionPayload('tabs.w1')!.payload as TabsPayload)
  const plain = clone(fixture.payload.tabs.phtab1)
  const carrying = { ...clone(plain), id: 'phtab2', group: 'ungrouped', groupRank: 'a' }
  return { ...mac, order: [...mac.order, 'phtab1', 'phtab2'], tabs: { ...mac.tabs, phtab1: plain, phtab2: carrying } }
}

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
    expect(await hashSection(payload)).toBe(PINNED_HASH)
  })
})

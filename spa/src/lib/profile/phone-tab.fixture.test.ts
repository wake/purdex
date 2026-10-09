// A tab the phone appended to a paired profile's `tabs.<ws>` section (QR pairing spec §5.2; plan QP-3 task 5), pinned
// against the REAL Mac code: the fixture is iOS's own output (`__fixtures__/phone-tab.json`, also a case of
// canonical-hash.json). The Mac must accept it (the section must not lock), put it last, and ideally rebuild the same section.
import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { PaneLayout, Tab } from '../../types/tab'
import fixtureJson from './__fixtures__/phone-tab.json'
import { applySectionToStores } from './apply-to-stores'
import { buildSectionPayload } from './collector'
import { hashSection } from './hash'
import type { TabsPayload } from './types'

const fixture = fixtureJson as unknown as { hash: string; payload: TabsPayload }
const MAC_HOST = 'aaaaaa'
const DAEMON = 'mini-lab:278cbm'

const macTab: Tab = {
  id: 't1',
  pinned: true,
  locked: false,
  createdAt: 1789999999999,
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

/** The Mac's own tabs.w1 section, with the phone tab appended to `order` and `tabs` (as the phone does). */
function stored(): TabsPayload {
  const mac = clone(buildSectionPayload('tabs.w1')!.payload as TabsPayload)
  return { ...mac, order: [...mac.order, 'phtab1'], tabs: { ...mac.tabs, phtab1: fixture.payload.tabs.phtab1 } }
}

describe('phone-appended tab (iOS fixture)', () => {
  it('a. the fixture payload hashes to the hash iOS pinned', async () => {
    expect(await hashSection(fixture.payload)).toBe(fixture.hash)
  })

  it('b. the Mac accepts it (no lock) and puts it last, the existing tab untouched', async () => {
    const before = useTabStore.getState().tabs.t1
    const outcome = await applySectionToStores('tabs.w1', stored(), { masterHostId: MAC_HOST })
    expect(outcome.ok).toBe(true)
    expect(useWorkspaceStore.getState().workspaces[0].tabs).toEqual(['t1', 'phtab1'])
    expect(useTabStore.getState().tabs.t1).toEqual(before)
    const phone = useTabStore.getState().tabs.phtab1
    expect(phone).toMatchObject({ id: 'phtab1', pinned: false, locked: false, createdAt: 1790000000000 })
  })

  it('c. after applying, the Mac rebuilds the very section the phone wrote (no rewrite push)', async () => {
    const payload = stored()
    const outcome = await applySectionToStores('tabs.w1', clone(payload), { masterHostId: MAC_HOST })
    expect(outcome.ok).toBe(true)
    const rebuilt = buildSectionPayload('tabs.w1')!.payload
    expect(rebuilt).toEqual(payload)
    expect(await hashSection(rebuilt)).toBe(await hashSection(payload))
  })
})

// spa/src/components/settings/profile/resolve-counts.test.ts — what the Resolve confirmation counts (P3d-4 plan, R1 /
// R5): the side each choice KEEPS, read from where that side really is, and "could not be read" rather than a guess.
// The real collector builders, master world, section store and stores; only the network (`getSection`) and the
// digest are replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../../../features/workspace/store'
import { useHostStore } from '../../../stores/useHostStore'
import type { HostConfig } from '../../../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../../stores/useLocalProfilesStore'
import { useTabStore } from '../../../stores/useTabStore'
import type { Tab, Workspace } from '../../../types/tab'
import { buildSectionPayload } from '../../../lib/profile/collector'
import type { SectionLock } from '../../../lib/profile/executor'
import { __resetMasterWorldForTest } from '../../../lib/profile/master-world'
import { clearSectionStore, putStash } from '../../../lib/profile/section-store'
import { countPayload, readHostSide, readLocalSide } from './resolve-counts'

vi.mock('../../../lib/profile/hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/profile/hash')>()
  return { ...actual, hashSection: vi.fn(async (payload: unknown) => `h:${actual.structuralKey(payload)}`) }
})

vi.mock('../../../lib/profile/api', () => ({ getSection: vi.fn() }))

const api = vi.mocked(await import('../../../lib/profile/api'))
const { hashSection } = await import('../../../lib/profile/hash')

const PROFILE = 'p_0123456789ab'
const HOST = 'host-master'
const HASH = 'a'.repeat(64)

function host(id: string): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0 }
}

function tab(id: string): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'browser', url: 'https://x.test' } } } } as Tab
}

function world(prefix: string, workspaceCount: number, tabsPerWorkspace: number): ParkedWorld {
  const workspaces: Workspace[] = []
  const tabs: Record<string, Tab> = {}
  for (let w = 0; w < workspaceCount; w += 1) {
    const ids = Array.from({ length: tabsPerWorkspace }, (_, i) => `${prefix}${w}t${i}`)
    for (const id of ids) tabs[id] = tab(id)
    workspaces.push({ id: `${prefix}ws${w}`, name: `${prefix}-${w}`, tabs: ids, activeTabId: ids[0] ?? null })
  }
  return { workspaces, tabs, activeWorkspaceId: workspaces[0]?.id ?? null, activeTabId: null }
}

function masterOnScreen(m: ParkedWorld): void {
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useTabStore.setState({ tabs: m.tabs, tabOrder: Object.keys(m.tabs), activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: m.workspaces, activeWorkspaceId: m.activeWorkspaceId, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
}

/** A local profile on screen, the master parked — what a follower window shows after another window switched. */
function slaveOnScreen(m: ParkedWorld, s: ParkedWorld): void {
  useLocalProfilesStore.setState({ slaves: { s1: { id: 's1', name: 'S', createdAt: 1, world: null } }, slaveOrder: ['s1'], activeProfileId: 's1', parkedMaster: m, worldEpoch: 1 })
  useTabStore.setState({ tabs: s.tabs, tabOrder: Object.keys(s.tabs), activeTabId: null, visitHistory: [], worldId: 's1', worldEpoch: 1 })
  useWorkspaceStore.setState({ workspaces: s.workspaces, activeWorkspaceId: s.activeWorkspaceId, worldId: 's1', worldEpoch: 1 })
}

const lock = (over: Partial<SectionLock>): SectionLock => ({ status: 'locked:reset', currentHash: null, sot: { rev: 4, hash: 'b'.repeat(64) }, conflict: null, ...over })

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  vi.clearAllMocks()
  useHostStore.setState({ hosts: { [HOST]: host(HOST), h2: host('h2'), h3: host('h3') }, hostOrder: [HOST, 'h2', 'h3'] })
  masterOnScreen(world('m', 2, 3))
})

afterEach(() => {
  clearSectionStore()
})

describe('countPayload — counts, not a diff', () => {
  it('hosts → hosts, workspaces → workspaces, tabs → tabs, settings → every entry of every store', () => {
    expect(countPayload('hosts', { hosts: { a: {}, b: {} }, hostOrder: ['a', 'b'] })).toBe(2)
    expect(countPayload('workspaces', { order: ['x'], workspaces: { x: {} } })).toBe(1)
    expect(countPayload('tabs.w1', { order: ['t1', 't2', 't3'], tabs: { t1: {}, t2: {}, t3: {} } })).toBe(3)
    expect(countPayload('settings', { 'purdex-layout': { a: 1, b: 2 }, 'purdex-i18n': { c: 3 } })).toBe(3)
  })

  it('no payload (the section is absent on that side) is 0 — exact, not unknown', () => {
    expect(countPayload('hosts', null)).toBe(0)
  })

  it('something that is not that section\'s shape → null (unreadable), never a guess', () => {
    expect(countPayload('hosts', { hosts: [] })).toBeNull()
    expect(countPayload('workspaces', 'x')).toBeNull()
    expect(countPayload('tabs.w1', { order: [] })).toBeNull()
    expect(countPayload('settings', { 'purdex-layout': 3 })).toBeNull()
    expect(countPayload('bogus', {})).toBeNull()
  })
})

// tabs-local-only §3.8: an ordinal-2 payload (the SOT's, or a stash an older build took) still lists interface-only
// tabs, which this build never sends and never applies. Both sides are counted after `upcastLegacyTabs`.
describe('countPayload — tabs are counted as this build would apply them (tabs-local-only §3.8)', () => {
  const entry = (id: string, content: Record<string, unknown>) => ({ id, pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf', pane: { id: `p-${id}`, content } } })
  const legacy = { order: ['a', 's', 'n'], tabs: { a: entry('a', { kind: 'browser', url: 'u' }), s: entry('s', { kind: 'settings', scope: 'global' }), n: entry('n', { kind: 'new-tab' }) } }

  it('a legacy payload listing interface tabs counts only the ones that travel', () => {
    expect(countPayload('tabs.w1', legacy)).toBe(1)
  })

  it('the host side of a legacy row, and a legacy local stash, are both counted that way', async () => {
    api.getSection.mockResolvedValue({ kind: 'ok', value: { section: 'tabs.mws0', rev: 4, hash: 'e'.repeat(64), fingerprint: 'f', ordinal: 2, writer: 'c', updatedAt: 0, payload: legacy } })
    expect((await readHostSide(HOST, PROFILE, 'tabs.mws0', lock({}), { expectEndpoint: '10.0.0.1:7860' })).count).toEqual({ state: 'read', count: 1 })
    putStash(PROFILE, HASH, legacy)
    const side = await readLocalSide(PROFILE, 'tabs.mws0', lock({ status: 'locked:conflict', currentHash: 'c'.repeat(64), conflict: { localHash: HASH, sot: { rev: 4, hash: 'b'.repeat(64) } } }))
    expect(side.count).toEqual({ state: 'read', count: 1 })
  })
})

describe('buildSectionPayload — the collector\'s own builders over the MASTER world (R5)', () => {
  it('with the master on screen: its world', () => {
    expect(countPayload('workspaces', buildSectionPayload('workspaces')!.payload)).toBe(2)
    expect(countPayload('tabs.mws1', buildSectionPayload('tabs.mws1')!.payload)).toBe(3)
    expect(countPayload('hosts', buildSectionPayload('hosts')!.payload)).toBe(3)
  })

  it('a local profile on screen (a follower window after another switched): the PARKED master\'s, never the slave\'s', () => {
    slaveOnScreen(world('m', 2, 3), world('s', 5, 1))
    expect(countPayload('workspaces', buildSectionPayload('workspaces')!.payload)).toBe(2)
    expect(countPayload('tabs.mws0', buildSectionPayload('tabs.mws0')!.payload)).toBe(3)
    // the slave's workspace is not the master's: absent from the master world
    expect(buildSectionPayload('tabs.sws0')).toEqual({ payload: null })
  })

  it('a workspace the master does not have: absent → { payload: null } (keep-local deletes it; 0 is exact)', () => {
    expect(buildSectionPayload('tabs.nope')).toEqual({ payload: null })
  })

  it('an unsettled world (nobody can say where the master world is) → null: "could not be read"', () => {
    // the tab store says the slave, the workspace store still the master: a switch half-way through this window's rehydrates
    useTabStore.setState({ worldId: 's1', worldEpoch: 1 })
    expect(buildSectionPayload('workspaces')).toBeNull()
    expect(buildSectionPayload('settings')).toBeNull()
    // hosts are live whatever is on screen
    expect(buildSectionPayload('hosts')).not.toBeNull()
  })
})

describe('readLocalSide — what "Keep this device\'s" keeps (R1)', () => {
  it('locked:conflict → the SENT snapshot (the stash of conflict.localHash), NOT what is built now', async () => {
    putStash(PROFILE, HASH, { order: ['a'], workspaces: { a: {} } }) // 1 workspace was sent; 2 are here now
    const side = await readLocalSide(PROFILE, 'workspaces', lock({ status: 'locked:conflict', currentHash: 'c'.repeat(64), conflict: { localHash: HASH, sot: { rev: 4, hash: 'b'.repeat(64) } } }))
    expect(side).toEqual({ count: { state: 'read', count: 1 }, changedSince: false })
  })

  it('locked:conflict whose snapshot is not in the stash (conflict-not-persisted) → unreadable', async () => {
    const side = await readLocalSide(PROFILE, 'workspaces', lock({ status: 'locked:conflict', currentHash: HASH, conflict: { localHash: HASH, sot: { rev: 4, hash: null } } }))
    expect(side.count).toEqual({ state: 'unreadable' })
  })

  it('locked:conflict whose sent side was "nothing" (localHash null) → 0', async () => {
    const side = await readLocalSide(PROFILE, 'tabs.mws0', lock({ status: 'locked:conflict', currentHash: null, conflict: { localHash: null, sot: { rev: 4, hash: 'b'.repeat(64) } } }))
    expect(side.count).toEqual({ state: 'read', count: 0 })
  })

  it.each(['locked:reset', 'locked:invalid'] as const)('%s → the payload built NOW; unchanged since the lock → not said', async (status) => {
    const built = buildSectionPayload('workspaces')!.payload
    const side = await readLocalSide(PROFILE, 'workspaces', lock({ status, currentHash: await hashSection(built) }))
    expect(side).toEqual({ count: { state: 'read', count: 2 }, changedSince: false })
  })

  it('locked:reset, this device changed since the lock was taken → said', async () => {
    const side = await readLocalSide(PROFILE, 'workspaces', lock({ status: 'locked:reset', currentHash: 'd'.repeat(64) }))
    expect(side).toEqual({ count: { state: 'read', count: 2 }, changedSince: true })
  })

  it('locked:reset with an unsettled world → unreadable, and nothing is claimed about a change', async () => {
    useTabStore.setState({ worldId: 's1', worldEpoch: 1 })
    const side = await readLocalSide(PROFILE, 'workspaces', lock({ status: 'locked:reset', currentHash: 'd'.repeat(64) }))
    expect(side).toEqual({ count: { state: 'unreadable' }, changedSince: false })
  })

  it('locked:reset is never read from the stash, even when one holds currentHash', async () => {
    putStash(PROFILE, HASH, { order: [], workspaces: {} })
    const side = await readLocalSide(PROFILE, 'workspaces', lock({ status: 'locked:reset', currentHash: HASH }))
    expect(side.count).toEqual({ state: 'read', count: 2 })
  })
})

describe('readHostSide — ONE read-only getSection, made when the confirmation opens', () => {
  const AT = { expectEndpoint: '10.0.0.1:7860' }
  const section = (rev: number, payload: Record<string, unknown>) => ({ kind: 'ok' as const, value: { section: 'workspaces', rev, hash: 'e'.repeat(64), fingerprint: 'f', ordinal: 1, writer: 'c', updatedAt: 0, payload } })

  it('the host\'s copy, counted; its rev is the frozen lock\'s → not "changed again"', async () => {
    api.getSection.mockResolvedValue(section(4, { order: ['a', 'b', 'c'], workspaces: { a: {}, b: {}, c: {} } }))
    expect(await readHostSide(HOST, PROFILE, 'workspaces', lock({}), AT)).toEqual({ count: { state: 'read', count: 3 }, movedOn: false })
    expect(api.getSection).toHaveBeenCalledTimes(1)
    // pinned to the attachment's endpoint (review A3): the api checks it where it resolves the address
    expect(api.getSection).toHaveBeenCalledWith(HOST, PROFILE, 'workspaces', { signal: undefined, expectEndpoint: '10.0.0.1:7860' })
  })

  it('another rev than the frozen lock\'s → the host changed again', async () => {
    api.getSection.mockResolvedValue(section(5, { order: [], workspaces: {} }))
    expect(await readHostSide(HOST, PROFILE, 'workspaces', lock({}), AT)).toEqual({ count: { state: 'read', count: 0 }, movedOn: true })
  })

  it('the host has no such section: 0; "changed again" only if the lock thought it had one', async () => {
    api.getSection.mockResolvedValue({ kind: 'ok', value: null })
    expect(await readHostSide(HOST, PROFILE, 'workspaces', lock({ sot: { rev: 4, hash: null } }), AT)).toEqual({ count: { state: 'read', count: 0 }, movedOn: false })
    expect(await readHostSide(HOST, PROFILE, 'workspaces', lock({}), AT)).toEqual({ count: { state: 'read', count: 0 }, movedOn: true })
  })

  it('a failed read, or one that throws → unreadable; never a guess', async () => {
    api.getSection.mockResolvedValueOnce({ kind: 'failed', reason: 'timeout', status: 0, message: 'x' })
    expect(await readHostSide(HOST, PROFILE, 'workspaces', lock({}), AT)).toEqual({ count: { state: 'unreadable' }, movedOn: false })
    api.getSection.mockRejectedValueOnce(new Error('boom'))
    expect(await readHostSide(HOST, PROFILE, 'workspaces', lock({}), AT)).toEqual({ count: { state: 'unreadable' }, movedOn: false })
  })
})

describe('readHostSide through the REAL getSection: the request is pinned to the attachment\'s endpoint (review A3, ABA)', () => {
  const A = '10.0.0.1:7860'
  const at = (ip: string): void => {
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, [HOST]: { ...host(HOST), ip } } })
  }
  let urls: string[]

  beforeEach(async () => {
    const actual = await vi.importActual<typeof import('../../../lib/profile/api')>('../../../lib/profile/api')
    api.getSection.mockImplementation(actual.getSection)
    urls = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      urls.push(String(input))
      const body = { section: 'workspaces', rev: 4, hash: 'e'.repeat(64), fingerprint: 'f'.repeat(64), ordinal: 1, writer: 'c_aaaaaaaaaaaa', updatedAt: 0, payload: { order: ['a'], workspaces: { a: {} } } }
      return new Response(JSON.stringify(body), { status: 200 })
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('the host at the endpoint: read as usual, from there', async () => {
    const side = await readHostSide(HOST, PROFILE, 'workspaces', lock({}), { expectEndpoint: A })
    expect(side.count).toEqual({ state: 'read', count: 1 })
    expect(urls).toHaveLength(1)
    expect(urls[0].startsWith('http://10.0.0.1:7860/')).toBe(true)
  })

  it('A → B → A: the address is B when the request is built → NOTHING goes to B; back at A by the answer, still nothing is counted', async () => {
    at('10.9.9.9') // B — after the hook's own check saw A
    const pending = readHostSide(HOST, PROFILE, 'workspaces', lock({}), { expectEndpoint: A })
    at('10.0.0.1') // back to A before the answer
    expect(await pending).toEqual({ count: { state: 'unreadable' }, movedOn: false })
    expect(urls.filter((u) => u.includes('10.9.9.9'))).toEqual([])
    expect(urls).toEqual([])
  })

  it('the api answers `endpoint-changed` — a failure, never a throw', async () => {
    at('10.9.9.9')
    const actual = await vi.importActual<typeof import('../../../lib/profile/api')>('../../../lib/profile/api')
    const result = await actual.getSection(HOST, PROFILE, 'workspaces', { expectEndpoint: A })
    expect(result).toMatchObject({ kind: 'failed', reason: 'endpoint-changed', status: 0 })
    expect(urls).toEqual([])
  })
})

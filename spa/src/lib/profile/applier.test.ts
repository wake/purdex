import { describe, expect, it } from 'vitest'
import type { HostConfig } from '../../stores/useHostStore'
import type { PaneContent, PaneLayout, Tab, Workspace } from '../../types/tab'
import {
  applyHosts,
  applySettings,
  applyTabs,
  applyWorkspaces,
  deriveTabOrder,
  isWellFormedSection,
  restoreSizes,
  upcastLegacySettings,
} from './applier'
import { hashSection } from './hash'
import { PROJECTIONS, project } from './projections'
import {
  buildHostsSection,
  buildSettingsSection,
  buildTabsSection,
  buildWorkspacesSection,
  stripSizes,
  type SettingsBuildInput,
} from './sections'
import type {
  HostsPayload,
  HostsSlice,
  SettingsPayload,
  StrippedLayout,
  TabsPayload,
  TabsSlice,
  WorkspacesPayload,
  WorkspacesSlice,
} from './types'

// --- fixtures ----------------------------------------------------------------

const S = '__DEVICE_LOCAL__'
/** No master workspace: for the tests that are not about workspace-scoped settings. */
const NO_WS: ReadonlySet<string> = new Set()

function leaf(id: string, content: PaneContent = { kind: 'dashboard' }): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

function split(id: string, children: PaneLayout[], sizes: number[], direction: 'h' | 'v' = 'h'): PaneLayout {
  return { type: 'split', id, direction, children, sizes }
}

function tab(id: string, layout: PaneLayout = leaf(`p-${id}`), extra: Partial<Tab> = {}): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout, ...extra }
}

function ws(id: string, name: string, tabs: string[], extra: Partial<Workspace> = {}): Workspace {
  return { id, name, tabs, activeTabId: null, ...extra }
}

function host(id: string, extra: Partial<HostConfig> = {}): HostConfig {
  return { id, name: `host-${id}`, ip: '10.0.0.1', port: 7860, order: 0, ...extra }
}

function deepFreeze<T>(value: T): T {
  if (typeof value === 'object' && value !== null && !Object.isFrozen(value)) {
    Object.freeze(value)
    for (const k of Object.keys(value)) deepFreeze((value as Record<string, unknown>)[k])
  }
  return value
}

/** A deep copy that is safe to mutate into a malformed payload. */
function copy<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function tabRecord(...tabs: Tab[]): Record<string, Tab> {
  return Object.fromEntries(tabs.map((t) => [t.id, t]))
}

// --- hosts -------------------------------------------------------------------

const EMPTY_HOSTS: HostsSlice = { hosts: {}, hostOrder: [], activeHostId: null, devHostId: null }

function hostsPayload(): HostsPayload {
  return buildHostsSection({
    hosts: {
      h1: host('h1', { token: 'tok', color: '#112233', icon: 'Laptop', iconWeight: 'bold' }),
      h2: host('h2', { order: 1, token: null, colors: { console: { main: { color: '#112233', alpha: 100 } } } }),
    },
    hostOrder: ['h1', 'h2'],
  })
}

function hostsLocals(): HostsSlice[] {
  return [
    EMPTY_HOSTS,
    { hosts: { x: host('x', { icon: 'Cube' }) }, hostOrder: ['x'], activeHostId: 'x', devHostId: 'x' },
    { hosts: { h1: host('h1', { name: 'old', icon: 'Old', token: 'other' }), z: host('z') }, hostOrder: ['z', 'h1'], activeHostId: 'h1', devHostId: 'z' },
  ]
}

describe('applyHosts', () => {
  it('round trip: hash(build(apply(local, p))) === hash(p) for any local', async () => {
    const p = deepFreeze(hostsPayload())
    for (const local of hostsLocals()) {
      const { next } = applyHosts(deepFreeze(local), p)
      expect(await hashSection(buildHostsSection(next))).toBe(await hashSection(p))
    }
  })

  it('round trip from empty over a built world, including the empty world', async () => {
    for (const world of [hostsPayload(), buildHostsSection({ hosts: {}, hostOrder: [] })]) {
      const { next } = applyHosts(EMPTY_HOSTS, world)
      expect(await hashSection(buildHostsSection(next))).toBe(await hashSection(world))
    }
  })

  it('replaces hosts and order, and reports the hosts that vanished', () => {
    const local = hostsLocals()[2]
    const { next, removedHostIds } = applyHosts(local, hostsPayload())
    expect(Object.keys(next.hosts).sort()).toEqual(['h1', 'h2'])
    expect(next.hostOrder).toEqual(['h1', 'h2'])
    expect(next.hosts.h1.name).toBe('host-h1')
    expect(removedHostIds).toEqual(['z'])
  })

  it('keeps activeHostId / devHostId while their host survives', () => {
    const local: HostsSlice = { hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'], activeHostId: 'h2', devHostId: 'h1' }
    const { next } = applyHosts(local, hostsPayload())
    expect(next.activeHostId).toBe('h2')
    expect(next.devHostId).toBe('h1')
  })

  it('nulls activeHostId / devHostId when their host is gone', () => {
    const local: HostsSlice = { hosts: { x: host('x'), y: host('y') }, hostOrder: ['x', 'y'], activeHostId: 'x', devHostId: 'y' }
    const { next, removedHostIds } = applyHosts(local, hostsPayload())
    expect(next.activeHostId).toBeNull()
    expect(next.devHostId).toBeNull()
    expect(removedHostIds).toEqual(['x', 'y'])
  })

  it('does not hand out the incoming order array itself', () => {
    const p = hostsPayload()
    const { next } = applyHosts(EMPTY_HOSTS, p)
    expect(next.hostOrder).not.toBe(p.hostOrder)
    expect(next.hosts).not.toBe(p.hosts)
  })
})

// --- workspaces --------------------------------------------------------------

const EMPTY_WS: WorkspacesSlice = { workspaces: [], activeWorkspaceId: null }

function workspacesPayload(): WorkspacesPayload {
  return buildWorkspacesSection([
    ws('wsB', 'Beta', [S]),
    ws('wsA', 'Alpha', [S], { icon: 'Cube', iconWeight: 'bold', moduleConfig: { files: { root: '/a' } } }),
    ws('wsNew', 'New', [], { moduleConfig: {} }),
  ])
}

function workspacesLocals(): WorkspacesSlice[] {
  return [
    EMPTY_WS,
    { workspaces: [ws('other', 'Other', ['t9'], { icon: 'Star', activeTabId: 't9' })], activeWorkspaceId: 'other' },
    {
      workspaces: [
        ws('wsA', 'Old alpha', ['t1', 't2'], { activeTabId: 't2', icon: 'OldIcon' }),
        ws('wsB', 'Old beta', ['t3'], { activeTabId: 't3', icon: 'Stale', iconWeight: 'fill', moduleConfig: { stale: { a: 1 } } }),
        ws('gone', 'Gone', ['t4']),
      ],
      activeWorkspaceId: 'wsA',
    },
  ]
}

describe('applyWorkspaces', () => {
  // A workspace whose id cannot form `tabs.<id>` is device-local: no builder
  // sends it, so no payload can mean "delete it".
  describe('unsyncable (device-local) workspaces', () => {
    const local = (): WorkspacesSlice => ({
      workspaces: [ws('bad id!', 'Bad', ['tb'], { activeTabId: 'tb' }), ws('a', 'A', ['t1']), ws('b.c', 'Dotted', []), ws('gone', 'Gone', ['t9'])],
      activeWorkspaceId: 'bad id!',
    })
    const p = (): WorkspacesPayload => ({ order: ['n', 'a'], workspaces: { n: { name: 'New' }, a: { name: 'Alpha' } } })

    it('survive an apply, after the incoming order, in their own relative order — and are never reported removed', () => {
      const bad = local().workspaces[0]
      const frozen = deepFreeze(local())
      const out = applyWorkspaces(frozen, deepFreeze(p()))
      expect(out.next.workspaces.map((w) => w.id)).toEqual(['n', 'a', 'bad id!', 'b.c'])
      expect(out.next.workspaces[2]).toEqual(bad)
      expect(out.removedWorkspaceIds).toEqual(['gone'])
      expect(out.addedWorkspaceIds).toEqual(['n'])
      expect(out.next.activeWorkspaceId).toBe('bad id!') // focus stays on a workspace that is still here
    })

    it('round trip still holds: the builder filters them out again', async () => {
      const out = applyWorkspaces(deepFreeze(local()), deepFreeze(p()))
      expect(await hashSection(buildWorkspacesSection(out.next.workspaces))).toBe(await hashSection(p()))
    })

    it('two clients: the one holding a bad id builds a payload the other accepts; the answer does not delete it', async () => {
      const one = local()
      const sent = buildWorkspacesSection(one.workspaces)
      expect(isWellFormedSection('workspaces', sent)).toBe(true)
      const two = applyWorkspaces({ workspaces: [ws('z', 'Z', [])], activeWorkspaceId: 'z' }, sent)
      expect(two.next.workspaces.map((w) => w.id)).toEqual(['a', 'gone'])
      // client 2 renames and sends back
      const reply = buildWorkspacesSection(two.next.workspaces.map((w) => (w.id === 'a' ? { ...w, name: 'Renamed' } : w)))
      expect(isWellFormedSection('workspaces', reply)).toBe(true)
      const back = applyWorkspaces(one, reply)
      expect(back.next.workspaces.map((w) => w.id)).toEqual(['a', 'gone', 'bad id!', 'b.c'])
      expect(back.next.workspaces[0].name).toBe('Renamed')
      expect(back.removedWorkspaceIds).toEqual([])
      expect(await hashSection(buildWorkspacesSection(back.next.workspaces))).toBe(await hashSection(reply))
    })
  })

  it('round trip: hash(build(apply(local, p))) === hash(p) for any local', async () => {
    const p = deepFreeze(workspacesPayload())
    for (const local of workspacesLocals()) {
      const { next } = applyWorkspaces(deepFreeze(local), p)
      expect(await hashSection(buildWorkspacesSection(next.workspaces))).toBe(await hashSection(p))
    }
  })

  it('round trip from empty over a built world, including the empty world', async () => {
    for (const world of [workspacesPayload(), buildWorkspacesSection([])]) {
      const { next } = applyWorkspaces(EMPTY_WS, world)
      expect(await hashSection(buildWorkspacesSection(next.workspaces))).toBe(await hashSection(world))
    }
  })

  it('orders by incoming, keeps tabs and activeTabId of a known workspace, and starts a new one empty', () => {
    const { next, addedWorkspaceIds, removedWorkspaceIds } = applyWorkspaces(workspacesLocals()[2], workspacesPayload())
    expect(next.workspaces.map((w) => w.id)).toEqual(['wsB', 'wsA', 'wsNew'])
    expect(next.workspaces[0]).toMatchObject({ name: 'Beta', tabs: ['t3'], activeTabId: 't3' })
    expect(next.workspaces[1]).toMatchObject({ name: 'Alpha', tabs: ['t1', 't2'], activeTabId: 't2', icon: 'Cube', iconWeight: 'bold' })
    expect(next.workspaces[2]).toEqual({ id: 'wsNew', name: 'New', tabs: [], activeTabId: null, moduleConfig: {} })
    expect(addedWorkspaceIds).toEqual(['wsNew'])
    expect(removedWorkspaceIds).toEqual(['gone'])
  })

  it('an optional field the payload no longer carries has no key in the result', () => {
    const { next } = applyWorkspaces(workspacesLocals()[2], workspacesPayload())
    const beta = next.workspaces[0]
    expect(Object.hasOwn(beta, 'icon')).toBe(false)
    expect(Object.hasOwn(beta, 'iconWeight')).toBe(false)
    expect(Object.hasOwn(beta, 'moduleConfig')).toBe(false)
  })

  it('keeps activeWorkspaceId while it survives, else the first, else null', () => {
    const local = workspacesLocals()[2]
    expect(applyWorkspaces(local, workspacesPayload()).next.activeWorkspaceId).toBe('wsA')
    expect(applyWorkspaces({ ...local, activeWorkspaceId: 'gone' }, workspacesPayload()).next.activeWorkspaceId).toBe('wsB')
    expect(applyWorkspaces({ ...local, activeWorkspaceId: null }, workspacesPayload()).next.activeWorkspaceId).toBe('wsB')
    expect(applyWorkspaces(local, buildWorkspacesSection([])).next.activeWorkspaceId).toBeNull()
  })
})

// --- tabs --------------------------------------------------------------------

function tabsWorld(): TabsSlice {
  return {
    tabs: tabRecord(
      tab('t1', split('sp1', [leaf('p1'), leaf('p2', { kind: 'browser', url: 'https://a.test' })], [30, 70])),
      tab('t2', leaf('p3'), { pinned: true }),
      tab('t3', leaf('p4')),
      tab('t4', leaf('p5')),
    ),
    workspaces: [ws('wsA', 'Alpha', ['t1', 't2'], { activeTabId: 't2' }), ws('wsB', 'Beta', ['t3', 't4'], { activeTabId: 't3' }), ws('wsC', 'Gamma', [])],
  }
}

/** The `tabs.wsA` payload of a world where wsA holds t2 (changed), tNew (split in three) and t1. */
function tabsPayload(): TabsPayload {
  const tabs = tabRecord(
    tab('t2', split('spX', [leaf('p3'), leaf('p9')], [50, 50], 'v'), { locked: true }),
    tab('tNew', split('spN', [leaf('n1'), leaf('n2'), leaf('n3')], [20, 30, 50])),
    tab('t1', split('sp1', [leaf('p1'), leaf('p2', { kind: 'browser', url: 'https://b.test' })], [10, 90])),
  )
  return buildTabsSection(ws('wsA', 'Alpha', ['t2', 'tNew', 't1']), tabs)
}

const EMPTY_TABS_WS: TabsSlice = { tabs: {}, workspaces: [ws('wsA', 'Alpha', [])] }

function buildBack(slice: TabsSlice, id: string): TabsPayload {
  return buildTabsSection(slice.workspaces.find((w) => w.id === id) as Workspace, slice.tabs)
}

describe('applyTabs', () => {
  it('round trip: hash(build(apply(local, p))) === hash(p) for any local', async () => {
    const p = deepFreeze(tabsPayload())
    const locals: TabsSlice[] = [
      EMPTY_TABS_WS,
      tabsWorld(),
      { tabs: tabRecord(tab('u1'), tab('u2')), workspaces: [ws('wsZ', 'Zed', ['u1']), ws('wsA', 'Alpha', ['u2', 'ghost'], { activeTabId: 'u2' })] },
      // tNew currently lives in ANOTHER workspace; t2 is a standalone tab.
      { tabs: tabRecord(tab('tNew'), tab('t2'), tab('k')), workspaces: [ws('wsA', 'Alpha', []), ws('wsB', 'Beta', ['tNew', 'k'], { activeTabId: 'tNew' })] },
    ]
    for (const local of locals) {
      const { next, unrendered } = applyTabs(deepFreeze(local), 'wsA', p)
      expect(unrendered).toBe(false)
      expect(await hashSection(buildBack(next, 'wsA'))).toBe(await hashSection(p))
    }
  })

  it('round trip from empty over a built world, including the empty section', async () => {
    const world = tabsWorld()
    for (const id of ['wsA', 'wsB', 'wsC']) {
      const built = buildBack(world, id)
      const { next } = applyTabs({ tabs: {}, workspaces: [ws(id, 'x', [])] }, id, built)
      expect(await hashSection(buildBack(next, id))).toBe(await hashSection(built))
    }
  })

  it('a workspace that does not exist locally is a no-op reported as unrendered', () => {
    const local = tabsWorld()
    const result = applyTabs(local, 'nope', tabsPayload())
    expect(result.unrendered).toBe(true)
    expect(result.next).toEqual(local)
    expect(result.removedTabIds).toEqual([])
  })

  it('writes the incoming tabs and order, removes this workspace\'s leftovers, restores local ratios', () => {
    const local: TabsSlice = { ...tabsWorld(), workspaces: [ws('wsA', 'Alpha', ['t1', 't2', 'tOld']), ws('wsB', 'Beta', ['t3', 't4'])] }
    local.tabs = { ...local.tabs, tOld: tab('tOld') }
    const { next, removedTabIds } = applyTabs(local, 'wsA', tabsPayload())
    expect(next.workspaces[0].tabs).toEqual(['t2', 'tNew', 't1'])
    expect(removedTabIds).toEqual(['tOld'])
    expect(Object.keys(next.tabs).sort()).toEqual(['t1', 't2', 't3', 't4', 'tNew'])
    expect(next.tabs.t2.locked).toBe(true)
    // sp1 exists locally with the same arity → local ratios, not the sender's
    expect(next.tabs.t1.layout).toMatchObject({ type: 'split', id: 'sp1', sizes: [30, 70] })
    // spX / spN are new → even
    expect(next.tabs.t2.layout).toMatchObject({ id: 'spX', sizes: [50, 50] })
    const spN = next.tabs.tNew.layout as Extract<PaneLayout, { type: 'split' }>
    expect(spN.sizes).toHaveLength(3)
    for (const s of spN.sizes) expect(s).toBeCloseTo(100 / 3, 10)
  })

  it('leaves other workspaces and their tabs untouched, by reference', () => {
    const local = tabsWorld()
    const { next } = applyTabs(local, 'wsA', tabsPayload())
    expect(next.workspaces[1]).toBe(local.workspaces[1])
    expect(next.workspaces[2]).toBe(local.workspaces[2])
    expect(next.tabs.t3).toBe(local.tabs.t3)
    expect(next.tabs.t4).toBe(local.tabs.t4)
  })

  it('keeps activeTabId while it survives, else the first of the new order, else null', () => {
    const local = tabsWorld() // wsA active = t2, which survives
    expect(applyTabs(local, 'wsA', tabsPayload()).next.workspaces[0].activeTabId).toBe('t2')

    const gone: TabsSlice = { ...local, workspaces: [ws('wsA', 'Alpha', ['t1', 'tOld'], { activeTabId: 'tOld' }), ...local.workspaces.slice(1)] }
    expect(applyTabs(gone, 'wsA', tabsPayload()).next.workspaces[0].activeTabId).toBe('t2')

    const emptied = applyTabs(local, 'wsA', buildTabsSection(ws('wsA', 'Alpha', []), {}))
    expect(emptied.next.workspaces[0].activeTabId).toBeNull()
    expect(emptied.next.workspaces[0].tabs).toEqual([])
    expect(emptied.removedTabIds).toEqual(['t1', 't2'])
  })

  it('a tab that arrives from another workspace is taken out of it, and that focus is repaired', () => {
    const local: TabsSlice = {
      tabs: tabRecord(tab('tNew'), tab('k'), tab('t1'), tab('t2')),
      workspaces: [ws('wsA', 'Alpha', ['t1', 't2'], { activeTabId: 't1' }), ws('wsB', 'Beta', ['tNew', 'k'], { activeTabId: 'tNew' }), ws('wsC', 'Gamma', [])],
    }
    const { next, removedTabIds } = applyTabs(local, 'wsA', tabsPayload())
    expect(next.workspaces[0].tabs).toEqual(['t2', 'tNew', 't1'])
    expect(next.workspaces[1].tabs).toEqual(['k'])
    expect(next.workspaces[1].activeTabId).toBe('k')
    expect(next.workspaces[2]).toBe(local.workspaces[2])
    expect(removedTabIds).toEqual([])
    // every tab id is owned by exactly one workspace
    const owners = next.workspaces.flatMap((w) => w.tabs)
    expect(new Set(owners).size).toBe(owners.length)
  })

  it('the robbed workspace keeps a focus that was not moved, and falls to null when emptied', () => {
    const local: TabsSlice = {
      tabs: tabRecord(tab('tNew'), tab('k')),
      workspaces: [ws('wsA', 'Alpha', []), ws('wsB', 'Beta', ['tNew', 'k'], { activeTabId: 'k' }), ws('wsD', 'Delta', ['tNew'], { activeTabId: 'tNew' })],
    }
    const { next } = applyTabs(local, 'wsA', tabsPayload())
    expect(next.workspaces[1]).toMatchObject({ tabs: ['k'], activeTabId: 'k' })
    expect(next.workspaces[2]).toMatchObject({ tabs: [], activeTabId: null })
  })
})

// --- restoreSizes ------------------------------------------------------------

describe('restoreSizes', () => {
  const incoming = stripSizes(split('a', [leaf('p1'), leaf('p2')], [1, 1]))

  it('same id and arity → the local ratios, as a copy', () => {
    const local = split('a', [leaf('x'), leaf('y')], [25, 75])
    const out = restoreSizes(incoming, local) as Extract<PaneLayout, { type: 'split' }>
    expect(out.sizes).toEqual([25, 75])
    expect(out.sizes).not.toBe((local as Extract<PaneLayout, { type: 'split' }>).sizes)
    expect(out.children.map((c) => (c.type === 'leaf' ? c.pane.id : ''))).toEqual(['p1', 'p2'])
  })

  it('same id, different arity → even', () => {
    const local = split('a', [leaf('x'), leaf('y'), leaf('z')], [20, 30, 50])
    expect(restoreSizes(incoming, local)).toMatchObject({ sizes: [50, 50] })
  })

  it('new id → even; local undefined → even; a leaf stays a leaf', () => {
    expect(restoreSizes(incoming, split('other', [leaf('x'), leaf('y')], [25, 75]))).toMatchObject({ sizes: [50, 50] })
    expect(restoreSizes(incoming, undefined)).toMatchObject({ sizes: [50, 50] })
    expect(restoreSizes(incoming, leaf('x'))).toMatchObject({ sizes: [50, 50] })
    expect(restoreSizes(stripSizes(leaf('solo')), undefined)).toEqual(leaf('solo'))
  })

  it('three children → three equal thirds', () => {
    const out = restoreSizes(stripSizes(split('t', [leaf('1'), leaf('2'), leaf('3')], [1, 1, 1])), undefined) as Extract<PaneLayout, { type: 'split' }>
    expect(out.sizes).toHaveLength(3)
    for (const s of out.sizes) expect(s).toBeCloseTo(33.3333333, 5)
  })

  it('nested: each split is matched by id wherever it sits in the local tree', () => {
    const inc = stripSizes(split('outer', [leaf('p1'), split('inner', [leaf('p2'), leaf('p3')], [1, 1], 'v')], [1, 1]))
    // locally `inner` is the ROOT's first child and `outer` has a different arity
    const local = split('outer', [split('inner', [leaf('a'), leaf('b')], [10, 90], 'v'), leaf('c'), leaf('d')], [10, 20, 70])
    const out = restoreSizes(inc, local) as Extract<PaneLayout, { type: 'split' }>
    expect(out.sizes).toEqual([50, 50])
    expect(out.children[1]).toMatchObject({ id: 'inner', direction: 'v', sizes: [10, 90] })
  })

  it('a duplicated local id resolves to its first occurrence', () => {
    const local = split('root', [split('a', [leaf('1'), leaf('2')], [11, 89]), split('a', [leaf('3'), leaf('4')], [60, 40])], [50, 50])
    expect(restoreSizes(incoming, local)).toMatchObject({ sizes: [11, 89] })
  })

  it('invalid local sizes (length, non-finite, ≤ 0) → even', () => {
    const bad = (sizes: number[]) => ({ type: 'split', id: 'a', direction: 'h', children: [leaf('x'), leaf('y')], sizes }) as PaneLayout
    for (const sizes of [[100], [50, 25, 25], [NaN, 50], [Infinity, 1], [0, 100], [-10, 110]]) {
      expect(restoreSizes(incoming, bad(sizes))).toMatchObject({ sizes: [50, 50] })
    }
    const noSizes = { type: 'split', id: 'a', direction: 'h', children: [leaf('x'), leaf('y')] } as unknown as PaneLayout
    expect(restoreSizes(incoming, noSizes)).toMatchObject({ sizes: [50, 50] })
  })

  it('stripSizes(restoreSizes(x, anything)) is x, and nothing is mutated', () => {
    const inc = deepFreeze(stripSizes(split('outer', [leaf('p1'), split('inner', [leaf('p2'), leaf('p3')], [1, 1])], [1, 1])))
    const local = deepFreeze(split('inner', [leaf('a'), leaf('b')], [10, 90]))
    expect(stripSizes(restoreSizes(inc, local))).toEqual(inc)
  })
})

// --- settings ----------------------------------------------------------------

function settingsLocal(): SettingsBuildInput {
  return {
    'purdex-ui-settings': { terminalRenderer: 'webgl', keepAliveCount: 3, terminalSettingsVersion: S, setRenderer: () => undefined },
    'purdex-themes': { activeThemeId: 'dark', customThemes: { a: { name: 'A', colors: { bg: '#000' } } } },
    'purdex-newtab-layout': { presets: { p: { cols: 2 } }, activeEditingPreset: S, knownIds: [S] },
    'purdex-layout': { tabPosition: 'top', regions: S, activityBarWidth: S },
  }
}

function settingsIncoming(): SettingsPayload {
  return {
    'purdex-ui-settings': { terminalRenderer: 'dom', keepAliveCount: 3 },
    'purdex-themes': { activeThemeId: 'dark', customThemes: { a: { colors: { bg: '#000' }, name: 'A' } } },
    'purdex-newtab-layout': { presets: { p: { cols: 3 } } },
    'purdex-layout': { tabPosition: 'left' },
  }
}

function mergePatches(local: SettingsBuildInput, patches: ReturnType<typeof applySettings>['patches']): SettingsBuildInput {
  const out: Record<string, object> = { ...local }
  for (const [key, patch] of Object.entries(patches)) out[key] = { ...out[key], ...patch }
  return out as SettingsBuildInput
}

describe('applySettings', () => {
  it('patches only listed fields whose value really differs', () => {
    const { patches } = applySettings(deepFreeze(settingsLocal()), deepFreeze(settingsIncoming()), NO_WS)
    expect(patches).toEqual({
      'purdex-ui-settings': { terminalRenderer: 'dom' },
      'purdex-newtab-layout': { presets: { p: { cols: 3 } } },
      'purdex-layout': { tabPosition: 'left' },
    })
    // same value under a different key order is not a change
    expect(Object.hasOwn(patches, 'purdex-themes')).toBe(false)
  })

  it('identical settings produce no patch at all', () => {
    const local = settingsLocal()
    expect(applySettings(local, buildSettingsSection(local, NO_WS), NO_WS)).toEqual({ patches: {}, rejected: [] })
  })

  it('an unlisted field never reaches a patch, even when the payload carries it', () => {
    const incoming = {
      ...settingsIncoming(),
      'purdex-ui-settings': { terminalRenderer: 'dom', keepAliveCount: 3, terminalSettingsVersion: 'INJECTED', somethingNew: 'INJECTED' },
      'purdex-newtab-layout': { presets: { p: { cols: 3 } }, activeEditingPreset: 'INJECTED', knownIds: ['INJECTED'] },
      'purdex-layout': { tabPosition: 'left', regions: 'INJECTED', activityBarWidth: 'INJECTED' },
    } as SettingsPayload
    const { patches } = applySettings(settingsLocal(), incoming, NO_WS)
    expect(JSON.stringify(patches)).not.toContain('INJECTED')
    expect(JSON.stringify(patches)).not.toContain(S)
    expect(Object.keys(patches['purdex-ui-settings'] ?? {})).toEqual(['terminalRenderer'])
    expect(Object.keys(patches['purdex-layout'] ?? {})).toEqual(['tabPosition'])
  })

  it('ignores an unknown store, and a store the payload lacks altogether (not sent is not "cleared")', () => {
    const incoming = { 'purdex-from-the-future': { x: 1 }, 'purdex-ui-settings': { terminalRenderer: 'webgl', keepAliveCount: 9 } } as SettingsPayload
    const { patches } = applySettings(deepFreeze(settingsLocal()), deepFreeze(incoming), NO_WS)
    expect(patches).toEqual({ 'purdex-ui-settings': { keepAliveCount: 9 } })
    for (const absent of ['purdex-themes', 'purdex-newtab-layout', 'purdex-layout', 'purdex-from-the-future']) {
      expect(Object.hasOwn(patches, absent)).toBe(false)
    }
  })

  it('a listed field the payload lacks, in a store it carries, was cleared over there: patched to undefined', () => {
    const incoming: SettingsPayload = { 'purdex-ui-settings': { keepAliveCount: 3 } }
    const { patches } = applySettings(deepFreeze(settingsLocal()), deepFreeze(incoming), NO_WS)
    const patch = patches['purdex-ui-settings'] as Record<string, unknown>
    expect(Object.prototype.hasOwnProperty.call(patch, 'terminalRenderer')).toBe(true)
    expect(patch.terminalRenderer).toBeUndefined()
    // only the one listed field local really holds: no unlisted field, no listed field that is undefined on both sides
    expect(Object.keys(patch)).toEqual(['terminalRenderer'])
  })

  it('a listed field absent on both sides (or locally undefined) is not patched', () => {
    const local: SettingsBuildInput = { ...settingsLocal(), 'purdex-ui-settings': { terminalRenderer: undefined, keepAliveCount: 3, terminalSettingsVersion: S } }
    expect(applySettings(local, { 'purdex-ui-settings': { keepAliveCount: 3 } }, NO_WS).patches).toEqual({})
  })

  it('round trip through a cleared field: the merged local builds back to the incoming hash', async () => {
    const theirs: SettingsBuildInput = { ...settingsLocal(), 'purdex-ui-settings': { terminalRenderer: undefined, keepAliveCount: 3 } }
    const p = deepFreeze(buildSettingsSection(theirs, NO_WS))
    expect(Object.hasOwn(p['purdex-ui-settings'] as object, 'terminalRenderer')).toBe(false) // the builder drops undefined
    const local = settingsLocal()
    const merged = mergePatches(local, applySettings(deepFreeze(local), p, NO_WS).patches)
    expect(await hashSection(buildSettingsSection(merged, NO_WS))).toBe(await hashSection(p))
    // the device-local field next to it survived
    expect((merged['purdex-ui-settings'] as Record<string, unknown>).terminalSettingsVersion).toBe(S)
  })

  it('patches a store the local side does not have yet', () => {
    const { patches } = applySettings({}, { 'purdex-i18n': { activeLocaleId: 'zh-TW' } }, NO_WS)
    expect(patches).toEqual({ 'purdex-i18n': { activeLocaleId: 'zh-TW' } })
  })

  it('round trip: patches merged into local build back to the incoming hash', async () => {
    const world: SettingsBuildInput = {
      ...settingsLocal(),
      'purdex-ui-settings': { terminalRenderer: 'dom', keepAliveCount: 7 },
      'purdex-layout': { tabPosition: 'left', regions: 'theirs' },
    }
    const p = deepFreeze(buildSettingsSection(world, NO_WS))
    for (const local of [settingsLocal(), world, { ...settingsLocal(), 'purdex-themes': { activeThemeId: 'light', customThemes: {} } }]) {
      const merged = mergePatches(local, applySettings(deepFreeze(local), p, NO_WS).patches)
      expect(await hashSection(buildSettingsSection(merged, NO_WS))).toBe(await hashSection(p))
    }
    // from empty
    const fromEmpty = mergePatches({}, applySettings({}, p, NO_WS).patches)
    expect(await hashSection(buildSettingsSection(fromEmpty, NO_WS))).toBe(await hashSection(p))
  })

  it('property: every settings payload the guard accepts converges — hash(build(merge(local, patches))) === hash(p)', async () => {
    const built: SettingsPayload[] = [
      buildSettingsSection(settingsLocal(), NO_WS),
      buildSettingsSection({ ...settingsLocal(), 'purdex-ui-settings': { terminalRenderer: undefined, keepAliveCount: 0, linkDetectTilde: false } }, NO_WS),
      buildSettingsSection({ ...settingsLocal(), 'purdex-themes': { activeThemeId: 'light' }, 'purdex-layout': { tabPosition: 'left', regions: 'theirs' } }, NO_WS),
    ]
    const handWritten = [
      { ...settingsIncoming(), 'purdex-ui-settings': { keepAliveCount: 9 } },
      { ...settingsIncoming(), 'purdex-ui-settings': { hostBadgeSidebarBox: { w: 1 } }, 'purdex-themes': { customThemes: {} } },
    ] as SettingsPayload[]
    // Every local holds the same stores as the payloads (a store the payload LACKS is "not sent", left alone by design).
    const locals: SettingsBuildInput[] = [
      settingsLocal(),
      // values for listed fields the payloads do not carry: they must be cleared, not kept
      {
        ...settingsLocal(),
        'purdex-ui-settings': { terminalRenderer: 'webgl', keepAliveCount: 3, keepAlivePinned: true, linkDetectTilde: true, hostBadgeSidebarInset: 4, terminalSettingsVersion: S },
        'purdex-themes': { activeThemeId: 'solar', customThemes: { z: { name: 'Z' } } },
      },
      // stores present but holding no listed field at all
      { 'purdex-ui-settings': { terminalSettingsVersion: S }, 'purdex-themes': {}, 'purdex-newtab-layout': { knownIds: [S] }, 'purdex-layout': { regions: S } },
    ]
    for (const p of [...built, ...handWritten]) {
      expect(isWellFormedSection('settings', p)).toBe(true)
      const frozen = deepFreeze(copy(p))
      for (const local of locals) {
        const result = applySettings(deepFreeze(copy(local)), frozen, NO_WS)
        expect(result.rejected).toEqual([])
        const merged = mergePatches(local, result.patches)
        expect(await hashSection(buildSettingsSection(merged, NO_WS))).toBe(await hashSection(frozen))
      }
    }
  })

  it('the payload the guard now refuses is exactly one that could NOT converge', async () => {
    const bad = { 'purdex-ui-settings': { terminalSettingsVersion: 1 } } as SettingsPayload
    expect(isWellFormedSection('settings', bad)).toBe(false)
    const local = settingsLocal()
    const merged = mergePatches(local, applySettings(local, bad, NO_WS).patches)
    expect(await hashSection(buildSettingsSection(merged, NO_WS))).not.toBe(await hashSection(bad))
  })

  describe('value shapes (R1 finding: {"purdex-layout":{"tabPosition":{}}})', () => {
    it('a value of another shape than the local one is rejected, and then NOTHING is patched', () => {
      const incoming = { 'purdex-layout': { tabPosition: {} } } as SettingsPayload
      expect(isWellFormedSection('settings', incoming)).toBe(true) // the guard has no value schema; this is where it is caught
      expect(applySettings(deepFreeze(settingsLocal()), deepFreeze(incoming), NO_WS)).toEqual({ patches: {}, rejected: ['purdex-layout.tabPosition'] })
    })

    it('a legitimate change in another store of the same payload is not applied either — never a partial apply', () => {
      const incoming = { ...settingsIncoming(), 'purdex-layout': { tabPosition: {} } } as SettingsPayload
      const result = applySettings(deepFreeze(settingsLocal()), deepFreeze(incoming), NO_WS)
      expect(result.rejected).toEqual(['purdex-layout.tabPosition'])
      expect(result.patches).toEqual({})
    })

    it('rejected is sorted and duplicate-free, one entry per field, across stores', () => {
      const incoming = {
        'purdex-ui-settings': { terminalRenderer: 7, keepAliveCount: '3' },
        'purdex-themes': { activeThemeId: null, customThemes: [] },
        'purdex-layout': { tabPosition: true },
      } as SettingsPayload
      const { patches, rejected } = applySettings(settingsLocal(), incoming, NO_WS)
      expect(patches).toEqual({})
      expect(rejected).toEqual([
        'purdex-layout.tabPosition',
        'purdex-themes.activeThemeId',
        'purdex-themes.customThemes',
        'purdex-ui-settings.keepAliveCount',
        'purdex-ui-settings.terminalRenderer',
      ])
    })

    it('every pair of different shape classes is a mismatch — null and array are classes of their own', () => {
      const samples: Record<string, unknown> = { null: null, array: [1], object: { a: 1 }, string: 'x', number: 1, boolean: true }
      for (const [mine, localValue] of Object.entries(samples)) {
        for (const [theirs, incomingValue] of Object.entries(samples)) {
          const local: SettingsBuildInput = { 'purdex-layout': { tabPosition: localValue } }
          const { patches, rejected } = applySettings(local, { 'purdex-layout': { tabPosition: incomingValue } } as SettingsPayload, NO_WS)
          if (mine === theirs) expect(rejected, `${mine} → ${theirs}`).toEqual([])
          else expect({ patches, rejected }, `${mine} → ${theirs}`).toEqual({ patches: {}, rejected: ['purdex-layout.tabPosition'] })
        }
      }
    })

    it('same shape patches as before; an equal value is never looked at', () => {
      const result = applySettings(deepFreeze(settingsLocal()), deepFreeze(settingsIncoming()), NO_WS)
      expect(result.rejected).toEqual([])
      expect(Object.keys(result.patches).sort()).toEqual(['purdex-layout', 'purdex-newtab-layout', 'purdex-ui-settings'])
    })

    it('no local value to compare with (undefined, absent field, absent store) → let through', () => {
      const local: SettingsBuildInput = { 'purdex-ui-settings': { terminalRenderer: undefined }, 'purdex-layout': {} }
      const incoming = { 'purdex-ui-settings': { terminalRenderer: {}, keepAliveCount: 'x' }, 'purdex-layout': { tabPosition: [] }, 'purdex-i18n': { activeLocaleId: 5 } } as SettingsPayload
      expect(applySettings(local, incoming, NO_WS)).toEqual({
        patches: { 'purdex-ui-settings': { terminalRenderer: {}, keepAliveCount: 'x' }, 'purdex-layout': { tabPosition: [] }, 'purdex-i18n': { activeLocaleId: 5 } },
        rejected: [],
      })
    })

    it('a field cleared over there (absent → undefined) has no shape to mismatch: still patched to undefined', () => {
      const { patches, rejected } = applySettings(settingsLocal(), { 'purdex-ui-settings': { keepAliveCount: 3 } }, NO_WS)
      expect(rejected).toEqual([])
      expect(Object.hasOwn(patches['purdex-ui-settings'] as object, 'terminalRenderer')).toBe(true)
    })

    it('a local value that is not JSON data (a function) mismatches whatever arrives', () => {
      const local: SettingsBuildInput = { 'purdex-layout': { tabPosition: () => undefined } }
      expect(applySettings(local, { 'purdex-layout': { tabPosition: 'left' } }, NO_WS)).toEqual({ patches: {}, rejected: ['purdex-layout.tabPosition'] })
    })
  })

  it('device-local fields of the local stores are still there after the merge', () => {
    const merged = mergePatches(settingsLocal(), applySettings(settingsLocal(), settingsIncoming(), NO_WS).patches) as Record<string, Record<string, unknown>>
    expect(merged['purdex-ui-settings'].terminalSettingsVersion).toBe(S)
    expect(merged['purdex-newtab-layout'].activeEditingPreset).toBe(S)
    expect(merged['purdex-newtab-layout'].knownIds).toEqual([S])
    expect(merged['purdex-layout'].regions).toBe(S)
    expect(merged['purdex-layout'].activityBarWidth).toBe(S)
  })
})

// --- settings: workspace-scoped entries ---------------------------------------

const WSS = 'purdex-workspace-settings'

function scopedStore(entries: Record<string, unknown>): SettingsBuildInput {
  return { [WSS]: { workspaces: entries, get: () => undefined }, 'purdex-layout': { tabPosition: 'top' } }
}

const scopedOf = (stores: SettingsBuildInput): Record<string, unknown> => (stores[WSS] as { workspaces: Record<string, unknown> }).workspaces

describe('applySettings — workspace-scoped settings belong to the master’s workspaces', () => {
  const MASTER = new Set(['m1', 'm2', 'm3'])

  it('master ids are replaced by the payload; an entry of any other id (a slave’s, an orphan) stays, by reference', () => {
    const slave = deepFreeze({ files: { root: S } })
    const local = deepFreeze(scopedStore({ m1: { files: { root: '/old' } }, m2: { files: { root: '/gone' } }, slave }))
    const incoming = deepFreeze({ [WSS]: { workspaces: { m1: { files: { root: '/new' } }, m3: { git: { on: true } } } } })
    const { patches, rejected } = applySettings(local, incoming, MASTER)
    expect(rejected).toEqual([])
    expect(patches).toEqual({ [WSS]: { workspaces: { m1: { files: { root: '/new' } }, m3: { git: { on: true } }, slave } } })
    expect((patches[WSS]!.workspaces as Record<string, unknown>).slave).toBe(slave)
  })

  it('a payload id that is not a master workspace (an orphan pushed by an older client) is NOT written', () => {
    const local = scopedStore({ m1: { a: { v: 1 } } })
    const incoming = { [WSS]: { workspaces: { m1: { a: { v: 1 } }, orphan: { a: { v: S } } } } }
    expect(applySettings(local, incoming, MASTER)).toEqual({ patches: {}, rejected: [] })
    const changed = applySettings(local, { [WSS]: { workspaces: { m1: { a: { v: 2 } }, orphan: { a: { v: S } } } } }, MASTER)
    expect(changed.patches).toEqual({ [WSS]: { workspaces: { m1: { a: { v: 2 } } } } })
  })

  it('never writes `__proto__`, and a non-record value still goes to the shape check', () => {
    const hostile = JSON.parse(`{"${WSS}":{"workspaces":{"__proto__":{"a":{"v":1}},"m1":{"a":{"v":1}}}}}`) as SettingsPayload
    const { patches } = applySettings(scopedStore({}), hostile, new Set(['m1', '__proto__']))
    expect(Object.keys(patches[WSS]!.workspaces as object)).toEqual(['m1'])
    expect(applySettings(scopedStore({ slave: {} }), { [WSS]: { workspaces: [] } } as unknown as SettingsPayload, MASTER)).toEqual({ patches: {}, rejected: [`${WSS}.workspaces`] })
  })

  it('INVARIANT: hash(build(apply(local, p))) === hash(build-filtered(p)); a slave entry is byte-identical after the apply and never built', async () => {
    const entries: Array<Record<string, unknown>> = [
      {},
      { m1: { a: { v: 1 } } },
      { m1: { a: { v: 2 } }, m2: { b: { w: 'x' } } },
      { m2: { b: { w: 'y' } }, slave: { a: { v: S } } },
      { m1: { a: { v: 1 } }, orphan: { a: { v: S } }, slave: { c: { z: S } } },
    ]
    const masters = [new Set<string>(), new Set(['m1']), new Set(['m1', 'm2']), new Set(['m1', 'm2', 'orphan'])]
    let checked = 0
    for (const master of masters) {
      for (const localEntries of entries) {
        for (const theirEntries of entries) {
          const local = deepFreeze(scopedStore(copy(localEntries)))
          // `p` as ANY client may have written it: unfiltered (an older build), so it can carry non-master ids
          const p = deepFreeze(buildSettingsSection(scopedStore(copy(theirEntries)), new Set(Object.keys(theirEntries))))
          const merged = mergePatches(local, applySettings(local, p, master).patches)
          expect(await hashSection(buildSettingsSection(merged, master))).toBe(await hashSection(buildSettingsSection(p as SettingsBuildInput, master)))
          for (const id of Object.keys(localEntries)) {
            if (master.has(id)) continue
            expect(JSON.stringify(scopedOf(merged)[id])).toBe(JSON.stringify(localEntries[id]))
          }
          const built = JSON.stringify(buildSettingsSection(merged, master))
          if (!master.has('orphan')) expect(built).not.toContain(S)
          checked += 1
        }
      }
    }
    expect(checked).toBe(100)
  })

  it('an apply of what this client built changes nothing (no patch → no store write → no echo)', () => {
    const local = deepFreeze(scopedStore({ m1: { a: { v: 1 } }, slave: { a: { v: S } } }))
    expect(applySettings(local, buildSettingsSection(local, MASTER), MASTER)).toEqual({ patches: {}, rejected: [] })
  })

  // `SECTION_SCHEMA_ORDINAL.settings` was NOT bumped for the filter. This is why that is sound: a client
  // that does not filter and one that does, sharing a SOT, settle — the SOT is written a bounded number of times.
  it('MIXED VERSIONS: an unfiltering (older) client and a filtering one sharing a SOT converge — no ping-pong', async () => {
    const world = new Set(['m1', 'm2']) // both clients hold the same `workspaces`
    /** The older build: the whole record out, the whole record in. */
    const oldBuild = (stores: SettingsBuildInput): SettingsPayload => project(stores, PROJECTIONS.settings) as SettingsPayload
    const oldApply = (stores: SettingsBuildInput, p: SettingsPayload): SettingsBuildInput =>
      mergePatches(stores, applySettings(stores, p, new Set([...Object.keys(scopedOf(stores)), ...Object.keys(scopedOf(p as SettingsBuildInput))])).patches)
    const clients = [
      { name: 'old', stores: scopedStore({ m1: { a: { v: 1 } }, orphan: { a: { v: 9 } } }), build: oldBuild, apply: oldApply, base: '' },
      { name: 'new', stores: scopedStore({ m1: { a: { v: 1 } } }), build: (s: SettingsBuildInput) => buildSettingsSection(s, world), apply: (s: SettingsBuildInput, p: SettingsPayload) => mergePatches(s, applySettings(s, p, world).patches), base: '' },
    ]
    // the older client wrote first: the SOT holds its orphan
    let sot = clients[0].build(clients[0].stores)
    let sotHash = await hashSection(sot)
    clients[0].base = sotHash
    const writes: string[] = []
    for (let round = 0; round < 10; round += 1) {
      let moved = false
      for (const c of clients) {
        if (c.base !== sotHash) {
          c.stores = c.apply(c.stores, sot) // pull (the client is clean: it agreed on `base`)
          c.base = sotHash
          moved = true
        }
        const mine = c.build(c.stores)
        const mineHash = await hashSection(mine)
        if (mineHash !== c.base) {
          sot = mine // dirty against the base it just agreed on → pushed back
          sotHash = mineHash
          c.base = mineHash
          writes.push(c.name)
          moved = true
        }
      }
      if (!moved) break
    }
    expect(writes).toEqual(['new']) // one corrective push by the filtering client, accepted by the older one
    expect(sot[WSS]).toEqual({ workspaces: { m1: { a: { v: 1 } } } })
    expect(scopedOf(clients[0].stores)).toEqual({ m1: { a: { v: 1 } } })
    expect(scopedOf(clients[1].stores)).toEqual({ m1: { a: { v: 1 } } })
  })
})

// --- deriveTabOrder ----------------------------------------------------------

describe('deriveTabOrder', () => {
  it('workspace orders first, then standalone tabs in their previous order, then the unmentioned', () => {
    const tabs = tabRecord(tab('s2'), tab('b1'), tab('a1'), tab('s1'), tab('fresh'), tab('a2'))
    const workspaces = deepFreeze([ws('wsA', 'A', ['a1', 'ghost', 'a2', 'a1']), ws('wsB', 'B', ['b1', 'a2'])])
    const previous = deepFreeze(['s1', 'a2', 'gone', 's2', 'b1'])
    expect(deriveTabOrder(workspaces, deepFreeze(tabs), previous)).toEqual(['a1', 'a2', 'b1', 's1', 's2', 'fresh'])
  })

  it('empty world → empty order', () => {
    expect(deriveTabOrder([], {}, ['x'])).toEqual([])
  })
})

// --- isWellFormedSection -----------------------------------------------------

describe('isWellFormedSection', () => {
  it('accepts everything the builders produce', () => {
    const world = tabsWorld()
    expect(isWellFormedSection('hosts', hostsPayload())).toBe(true)
    expect(isWellFormedSection('hosts', buildHostsSection({ hosts: {}, hostOrder: [] }))).toBe(true)
    expect(isWellFormedSection('workspaces', workspacesPayload())).toBe(true)
    expect(isWellFormedSection('workspaces', buildWorkspacesSection([]))).toBe(true)
    expect(isWellFormedSection('tabs', tabsPayload())).toBe(true)
    for (const id of ['wsA', 'wsB', 'wsC']) expect(isWellFormedSection('tabs', buildBack(world, id))).toBe(true)
    expect(isWellFormedSection('settings', buildSettingsSection(settingsLocal(), NO_WS))).toBe(true)
    expect(isWellFormedSection('settings', {})).toBe(true)
  })

  it('accepts a payload that came through JSON', () => {
    expect(isWellFormedSection('tabs', copy(tabsPayload()))).toBe(true)
    expect(isWellFormedSection('hosts', copy(hostsPayload()))).toBe(true)
  })

  it('never throws on garbage, for any kind', () => {
    const cyclic: Record<string, unknown> = { order: [], tabs: {} }
    cyclic.tabs = { self: cyclic }
    const wide: Record<string, unknown> = {}
    wide.a = wide
    wide.b = wide
    const hostile = new Proxy({}, { get: () => { throw new Error('boom') }, ownKeys: () => { throw new Error('boom') } })
    const garbage: unknown[] = [null, undefined, 42, 'x', true, [], [1], () => 1, new Date(0), new Map(), cyclic, wide, hostile, { order: hostile, tabs: hostile }]
    for (const kind of ['hosts', 'settings', 'workspaces', 'tabs'] as const) {
      for (const g of garbage) expect(isWellFormedSection(kind, g)).toBe(false)
    }
    expect(isWellFormedSection('nope' as never, {})).toBe(false)
  })

  describe('tabs', () => {
    const bad = (mutate: (p: { order: unknown; tabs: Record<string, Record<string, unknown>> } & Record<string, unknown>) => void): boolean => {
      const p = copy(tabsPayload()) as never
      mutate(p)
      return isWellFormedSection('tabs', p)
    }
    const splitOf = (p: { tabs: Record<string, Record<string, unknown>> }, id: string) => p.tabs[id].layout as Record<string, unknown>

    it('order must be a duplicate-free string array equal to the record keys', () => {
      expect(bad((p) => { (p.order as string[]).push('ghost') })).toBe(false) // one too many
      expect(bad((p) => { (p.order as string[]).pop() })).toBe(false) // one too few: ⊆ is not enough
      expect(bad((p) => { (p.order as string[]).push('t1') })).toBe(false) // duplicate
      expect(bad((p) => { p.order = ['t2', 'tNew', 't2'] })).toBe(false) // duplicate hiding a missing id
      expect(bad((p) => { p.order = [1, 2, 3] })).toBe(false)
      expect(bad((p) => { p.order = 't2' })).toBe(false)
      expect(bad((p) => { delete p.order })).toBe(false)
      expect(bad((p) => { delete (p as Record<string, unknown>).tabs })).toBe(false)
      expect(bad((p) => { (p as Record<string, unknown>).tabs = [] })).toBe(false)
      expect(bad((p) => { p.extra = 1 })).toBe(false)
    })

    it('each tab: id equal to its key, booleans, finite createdAt, a layout, nothing else', () => {
      expect(bad((p) => { p.tabs.t1.id = 't2' })).toBe(false)
      expect(bad((p) => { delete p.tabs.t1.id })).toBe(false)
      expect(bad((p) => { p.tabs.t1.pinned = 'yes' })).toBe(false)
      expect(bad((p) => { delete p.tabs.t1.locked })).toBe(false)
      expect(bad((p) => { p.tabs.t1.createdAt = '1' })).toBe(false)
      expect(bad((p) => { p.tabs.t1.createdAt = Infinity })).toBe(false)
      expect(bad((p) => { delete p.tabs.t1.layout })).toBe(false)
      expect(bad((p) => { (p.tabs as Record<string, unknown>).t1 = null })).toBe(false)
      expect(bad((p) => { p.tabs.t1.activeAnything = 1 })).toBe(false) // would not survive build → could never converge
    })

    it('layout: leaves need pane.id and pane.content.kind', () => {
      const leafOf = (p: { tabs: Record<string, Record<string, unknown>> }) => (splitOf(p, 't1').children as Record<string, Record<string, unknown>>[])[0]
      expect(bad((p) => { delete leafOf(p).pane.id })).toBe(false)
      expect(bad((p) => { leafOf(p).pane.id = 7 })).toBe(false)
      expect(bad((p) => { delete (leafOf(p).pane.content as Record<string, unknown>).kind })).toBe(false)
      expect(bad((p) => { leafOf(p).pane.content = null })).toBe(false)
      expect(bad((p) => { delete (leafOf(p) as Record<string, unknown>).pane })).toBe(false)
      expect(bad((p) => { (leafOf(p) as Record<string, unknown>).type = 'twig' })).toBe(false)
    })

    it('layout: splits need id, direction, non-empty children — and no sizes', () => {
      expect(bad((p) => { delete splitOf(p, 't1').id })).toBe(false)
      expect(bad((p) => { splitOf(p, 't1').direction = 'x' })).toBe(false)
      expect(bad((p) => { splitOf(p, 't1').children = [] })).toBe(false)
      expect(bad((p) => { splitOf(p, 't1').children = 'kids' })).toBe(false)
      expect(bad((p) => { splitOf(p, 't1').sizes = [50, 50] })).toBe(false)
      // `sizes` anywhere under layout is stripped by the builder, so it can never converge
      expect(bad((p) => { ((splitOf(p, 't1').children as Record<string, Record<string, unknown>>[])[0].pane.content as Record<string, unknown>).sizes = [1] })).toBe(false)
    })

    it('layout: a tree deeper than the limit is refused, a reasonable one is not', () => {
      const nest = (depth: number): StrippedLayout => {
        let node: StrippedLayout = { type: 'leaf', pane: { id: 'deep', content: { kind: 'dashboard' } } }
        for (let i = 0; i < depth; i++) node = { type: 'split', id: `s${i}`, direction: 'h', children: [node] }
        return node
      }
      const payload = (depth: number) => ({ order: ['t'], tabs: { t: { id: 't', pinned: false, locked: false, createdAt: 1, layout: nest(depth) } } })
      expect(isWellFormedSection('tabs', payload(20))).toBe(true)
      expect(isWellFormedSection('tabs', payload(65))).toBe(false)
      expect(isWellFormedSection('tabs', payload(5000))).toBe(false)
    })

    it('refuses prototype-polluting keys at any depth', () => {
      const withProto = JSON.parse('{"order":["__proto__"],"tabs":{"__proto__":{"id":"__proto__","pinned":false,"locked":false,"createdAt":1,"layout":{"type":"leaf","pane":{"id":"p","content":{"kind":"dashboard"}}}}}}') as unknown
      expect(isWellFormedSection('tabs', withProto)).toBe(false)
      const deep = JSON.parse('{"order":["t"],"tabs":{"t":{"id":"t","pinned":false,"locked":false,"createdAt":1,"layout":{"type":"leaf","pane":{"id":"p","content":{"kind":"dashboard","__proto__":{"polluted":true}}}}}}}') as unknown
      expect(isWellFormedSection('tabs', deep)).toBe(false)
      for (const key of ['constructor', 'prototype']) {
        expect(bad((p) => { ((splitOf(p, 't1').children as Record<string, Record<string, unknown>>[])[0].pane.content as Record<string, unknown>)[key] = {} })).toBe(false)
      }
      expect(({} as Record<string, unknown>).polluted).toBeUndefined()
    })
  })

  describe('workspaces', () => {
    const bad = (mutate: (p: { order: unknown; workspaces: Record<string, Record<string, unknown>> } & Record<string, unknown>) => void): boolean => {
      const p = copy(workspacesPayload()) as never
      mutate(p)
      return isWellFormedSection('workspaces', p)
    }

    it('order must equal the record keys, without duplicates', () => {
      expect(bad((p) => { (p.order as string[]).push('ghost') })).toBe(false)
      expect(bad((p) => { (p.order as string[]).pop() })).toBe(false)
      expect(bad((p) => { (p.order as string[]).push('wsA') })).toBe(false)
      expect(bad((p) => { delete (p as Record<string, unknown>).workspaces })).toBe(false)
    })

    it('entry fields are typed, and nothing else is allowed', () => {
      expect(bad((p) => { delete p.workspaces.wsA.name })).toBe(false)
      expect(bad((p) => { p.workspaces.wsA.name = 5 })).toBe(false)
      expect(bad((p) => { p.workspaces.wsA.icon = 5 })).toBe(false)
      expect(bad((p) => { p.workspaces.wsA.iconWeight = {} })).toBe(false)
      expect(bad((p) => { p.workspaces.wsA.moduleConfig = [] })).toBe(false)
      expect(bad((p) => { p.workspaces.wsA.moduleConfig = 'x' })).toBe(false)
      expect(bad((p) => { p.workspaces.wsA.tabs = ['t1'] })).toBe(false)
      expect(bad((p) => { p.workspaces.wsA.activeTabId = 't1' })).toBe(false)
      expect(bad((p) => { (p.workspaces as Record<string, unknown>).wsA = 'x' })).toBe(false)
    })

    it('a workspace id that cannot form a `tabs.<id>` section key is refused', () => {
      expect(isWellFormedSection('workspaces', { order: ['has.dot'], workspaces: { 'has.dot': { name: 'x' } } })).toBe(false)
      expect(isWellFormedSection('workspaces', { order: [''], workspaces: { '': { name: 'x' } } })).toBe(false)
    })
  })

  describe('hosts', () => {
    const bad = (mutate: (p: { hostOrder: unknown; hosts: Record<string, Record<string, unknown>> } & Record<string, unknown>) => void): boolean => {
      const p = copy(hostsPayload()) as never
      mutate(p)
      return isWellFormedSection('hosts', p)
    }

    it('hostOrder: strings, no duplicates, every id a known host', () => {
      expect(bad((p) => { (p.hostOrder as string[]).push('ghost') })).toBe(false)
      expect(bad((p) => { (p.hostOrder as string[]).push('h1') })).toBe(false)
      expect(bad((p) => { p.hostOrder = [1] })).toBe(false)
      expect(bad((p) => { delete p.hostOrder })).toBe(false)
      expect(bad((p) => { delete (p as Record<string, unknown>).hosts })).toBe(false)
      expect(bad((p) => { p.extra = 1 })).toBe(false)
    })

    it('a host missing from hostOrder is allowed — useHostStore.reorderHosts permits it today', () => {
      expect(bad((p) => { (p.hostOrder as string[]).pop() })).toBe(true)
    })

    it('each host is typed, keyed by its id, and carries no unlisted field', () => {
      expect(bad((p) => { p.hosts.h1.id = 'h2' })).toBe(false)
      expect(bad((p) => { delete p.hosts.h1.name })).toBe(false)
      expect(bad((p) => { p.hosts.h1.ip = 1 })).toBe(false)
      expect(bad((p) => { p.hosts.h1.port = 78.5 })).toBe(false)
      expect(bad((p) => { p.hosts.h1.port = '7860' })).toBe(false)
      expect(bad((p) => { p.hosts.h1.order = null })).toBe(false)
      expect(bad((p) => { p.hosts.h1.token = 5 })).toBe(false)
      expect(bad((p) => { p.hosts.h1.color = 5 })).toBe(false)
      expect(bad((p) => { p.hosts.h1.colors = [] })).toBe(false)
      expect(bad((p) => { p.hosts.h1.runtime = {} })).toBe(false)
      expect(bad((p) => { p.hosts.h1.token = null })).toBe(true)
      expect(bad((p) => { delete p.hosts.h1.token })).toBe(true)
    })

    it('daemonId (hosts ordinal 2) is optional — an ordinal-1 payload without it is well-formed — and a non-empty string when present', () => {
      expect(Object.hasOwn(hostsPayload().hosts.h1, 'daemonId')).toBe(false) // the fixture IS an ordinal-1-shaped payload
      expect(isWellFormedSection('hosts', copy(hostsPayload()))).toBe(true)
      expect(bad((p) => { p.hosts.h1.daemonId = 'mini-lab:abc123' })).toBe(true)
      expect(bad((p) => { p.hosts.h1.daemonId = '' })).toBe(false)
      expect(bad((p) => { p.hosts.h1.daemonId = 5 })).toBe(false)
      expect(bad((p) => { p.hosts.h1.daemonId = null })).toBe(false)
      expect(bad((p) => { p.hosts.h1.daemonId = { id: 'x' } })).toBe(false)
    })
  })

  describe('settings', () => {
    it('every store value must be a plain object', () => {
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': null })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': [] })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': 'x' })).toBe(false)
      expect(isWellFormedSection('settings', JSON.parse('{"purdex-themes":{"customThemes":{"__proto__":{"x":1}}}}'))).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: NaN } })).toBe(false)
    })

    it('a known store may carry only the fields PROJECTIONS lists for it (attack finding: terminalSettingsVersion)', () => {
      // The reproduction: the guard used to accept this, applySettings then read every
      // listed field as "cleared over there" and wiped the user's settings.
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { terminalSettingsVersion: 1 } })).toBe(false)
      expect(isWellFormedSection('settings', JSON.parse('{"purdex-ui-settings":{"terminalSettingsVersion":1}}'))).toBe(false)
      // a listed field next to it does not redeem the payload
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: 3, terminalSettingsVersion: 1 } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-layout': { tabPosition: 'left', regions: {} } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-newtab-layout': { presets: {}, knownIds: [] } })).toBe(false)
      // the ordinal-3 name is not listed any more (P3e): refused HERE — apply-to-stores upcasts it first
      expect(isWellFormedSection('settings', { 'purdex-newtab-layout': { profiles: {} } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-newtab-layout': { presets: {} } })).toBe(true)
      // a field listed for ANOTHER store is not listed for this one
      expect(isWellFormedSection('settings', { 'purdex-layout': { keepAliveCount: 3 } })).toBe(false)
      // one bad store refuses the whole payload
      expect(isWellFormedSection('settings', { 'purdex-themes': { activeThemeId: 'dark' }, 'purdex-layout': { regions: {} } })).toBe(false)
      // listed fields only: fine
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: 3 }, 'purdex-layout': { tabPosition: 'left' } })).toBe(true)
    })

    it('the allowlist is PROJECTIONS.settings itself: every listed field passes on its own, for every known store', () => {
      const byStore = new Map<string, string[]>()
      for (const path of PROJECTIONS.settings) {
        const dot = path.indexOf('.')
        byStore.set(path.slice(0, dot), [...(byStore.get(path.slice(0, dot)) ?? []), path.slice(dot + 1)])
      }
      expect(byStore.size).toBe(8)
      for (const [store, fields] of byStore) {
        for (const field of fields) expect(isWellFormedSection('settings', { [store]: { [field]: 1 } })).toBe(true)
        expect(isWellFormedSection('settings', { [store]: { [`${fields[0]}X`]: 1 } })).toBe(false)
      }
    })

    it('a known store that is empty is refused — the builder omits such a store, and it would clear every listed field', () => {
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': {} })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: undefined } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-themes': { activeThemeId: 'dark' }, 'purdex-layout': {} })).toBe(false)
    })

    it('an unknown storage key is refused: nothing here could rebuild it, so the payload could never hash back', () => {
      // (A newer client with one more store has another PROJECTIONS fingerprint — the schema lock stops it first.)
      expect(isWellFormedSection('settings', { 'purdex-from-the-future': { x: 1 } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-from-the-future': {} })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: 3 }, 'purdex-from-the-future': { x: 1 } })).toBe(false)
      // a real storage key that is simply not a synced one
      expect(isWellFormedSection('settings', { 'purdex-hosts': { hosts: {} } })).toBe(false)
      // an `undefined` member is no member (the hash drops it too)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: 3 }, 'purdex-from-the-future': undefined })).toBe(true)
    })

    it('purdex-module-enabled is an unknown store: module on/off is device-local (settings ordinal 2)', () => {
      expect(isWellFormedSection('settings', { 'purdex-module-enabled': { enabled: { files: false } } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-module-enabled': { enabled: {} } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: 3 }, 'purdex-module-enabled': { enabled: { files: true } } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: 3 } })).toBe(true) // …and only that store is the reason
    })

    it('purdex-editor-settings is an unknown store: editor preferences are device-local (settings ordinal 3)', () => {
      expect(isWellFormedSection('settings', { 'purdex-editor-settings': { fontSize: 14 } })).toBe(false)
      expect(isWellFormedSection('settings', { 'purdex-ui-settings': { keepAliveCount: 3 }, 'purdex-editor-settings': { tabSize: 2 } })).toBe(false)
    })

    it('applySettings still ignores an unknown store, should one ever get past the guard', () => {
      const known = { 'purdex-ui-settings': { terminalRenderer: 'dom', keepAliveCount: 3 } }
      const incoming = { ...known, 'purdex-from-the-future': { terminalSettingsVersion: 1, keepAliveCount: 99 } } as SettingsPayload
      const local = deepFreeze(settingsLocal())
      expect(applySettings(local, deepFreeze(incoming), NO_WS)).toEqual(applySettings(local, known, NO_WS))
    })
  })
})

// --- purity ------------------------------------------------------------------

describe('no function mutates its input', () => {
  it('apply* over deep-frozen local and incoming', () => {
    expect(() => {
      applyHosts(deepFreeze(hostsLocals()[2]), deepFreeze(hostsPayload()))
      applyWorkspaces(deepFreeze(workspacesLocals()[2]), deepFreeze(workspacesPayload()))
      applyTabs(deepFreeze(tabsWorld()), 'wsA', deepFreeze(tabsPayload()))
      applyTabs(deepFreeze(tabsWorld()), 'nope', deepFreeze(tabsPayload()))
      applySettings(deepFreeze(settingsLocal()), deepFreeze(settingsIncoming()), NO_WS)
      isWellFormedSection('tabs', deepFreeze(tabsPayload()))
    }).not.toThrow()
  })
})

// --- upcastLegacySettings (P3e) ------------------------------------------------

describe('upcastLegacySettings — an ordinal-3 settings payload (newtab `profiles`) reads as ordinal 4', () => {
  const layout = { '3col': { enabled: true, columns: [['a'], [], []] }, '2col': { enabled: false, columns: [[], []] }, '1col': { enabled: true, columns: [['a']] } }

  it('renames purdex-newtab-layout.profiles to .presets, touching nothing else, never mutating', () => {
    const legacy = deepFreeze({
      'purdex-ui-settings': { keepAliveCount: 3 },
      'purdex-newtab-layout': { profiles: copy(layout) },
      'purdex-layout': { tabPosition: 'left' },
    })
    const out = upcastLegacySettings(legacy) as Record<string, unknown>
    expect(out).toEqual({
      'purdex-ui-settings': { keepAliveCount: 3 },
      'purdex-newtab-layout': { presets: layout },
      'purdex-layout': { tabPosition: 'left' },
    })
    expect(out).not.toBe(legacy)
    expect(out['purdex-ui-settings']).toBe(legacy['purdex-ui-settings']) // untouched stores are the same objects
    expect(legacy['purdex-newtab-layout']).toEqual({ profiles: layout }) // input intact
    expect(isWellFormedSection('settings', out)).toBe(true)
    expect(isWellFormedSection('settings', legacy)).toBe(false)
  })

  it('both profiles and presets present → untouched (same reference)', () => {
    const both = deepFreeze({ 'purdex-newtab-layout': { profiles: copy(layout), presets: copy(layout) } })
    expect(upcastLegacySettings(both)).toBe(both)
  })

  it('an ordinal-4 payload is returned as is (same reference)', () => {
    const current = deepFreeze({ 'purdex-newtab-layout': { presets: copy(layout) }, 'purdex-layout': { tabPosition: 'top' } })
    expect(upcastLegacySettings(current)).toBe(current)
  })

  it('no newtab store, or not a plain object, or not a payload at all → same reference', () => {
    const noStore = deepFreeze({ 'purdex-layout': { tabPosition: 'top' } })
    expect(upcastLegacySettings(noStore)).toBe(noStore)
    for (const odd of [null, undefined, 5, 'x', [], [{ 'purdex-newtab-layout': { profiles: {} } }]] as unknown[]) {
      expect(upcastLegacySettings(odd)).toBe(odd)
    }
    for (const store of [null, 7, 'profiles', [{ profiles: {} }]] as unknown[]) {
      const p = deepFreeze({ 'purdex-newtab-layout': store })
      expect(upcastLegacySettings(p)).toBe(p)
    }
  })

  it('carries the other fields of the newtab store along (the guard then judges them, as before)', () => {
    const legacy = deepFreeze({ 'purdex-newtab-layout': { profiles: copy(layout), knownIds: ['x'] } })
    expect(upcastLegacySettings(legacy)).toEqual({ 'purdex-newtab-layout': { presets: layout, knownIds: ['x'] } })
  })
})

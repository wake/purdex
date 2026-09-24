import { describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useThemeStore } from '../../stores/useThemeStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import type { FileSource } from '../../types/fs'
import type { PaneContent, PaneLayout, PaneRebuildRecord, Tab, Workspace } from '../../types/tab'
import { isWellFormedSection } from './applier'
import { hashSection, structuralKey } from './hash'
import {
  adoptStandaloneTabs,
  repairTabOwnership,
  buildHostsSection,
  buildProfileDocument,
  buildSettingsSection,
  buildTabsSection,
  buildWorkspacesSection,
  DEVICE_LOCAL_PANE_KINDS,
  isSyncableTab,
  stripSizes,
  unsyncableWorkspaceIds,
  wireResolverOf,
  type CollectInput,
  type SettingsBuildInput,
} from './sections'
import type { HostsSource, TabsSource, WorkspacesSource } from './types'
import { identityOfSync, syncIdOfSync } from './host-identity'

/** No master workspace: for the tests that are not about workspace-scoped settings. */
const NO_WS: ReadonlySet<string> = new Set()

// --- type pins ---------------------------------------------------------------
// Compile-time only (never called): the real store states must be assignable to
// the builders' inputs WITHOUT a cast, so a store refactor fails
// `tsc -p tsconfig.app.json` here rather than at runtime in P2b.
export function _typePins(): CollectInput {
  const hosts: HostsSource = useHostStore.getState()
  const workspaces: WorkspacesSource = useWorkspaceStore.getState()
  const tabs: TabsSource = useTabStore.getState()
  const settings: SettingsBuildInput = {
    'purdex-ui-settings': useUISettingsStore.getState(),
    'purdex-themes': useThemeStore.getState(),
    'purdex-i18n': useI18nStore.getState(),
    'purdex-notification-settings': useNotificationSettingsStore.getState(),
    'purdex-workspace-settings': useWorkspaceSettingsStore.getState(),
    'purdex-host-settings': useHostSettingsStore.getState(),
    'purdex-newtab-layout': useNewTabLayoutStore.getState(),
    'purdex-layout': useLayoutStore.getState(),
  }
  buildSettingsSection({ 'purdex-ui-settings': useUISettingsStore.getState() }, NO_WS)
  // @ts-expect-error — module on/off is device-local: its store is not a settings source
  buildSettingsSection({ 'purdex-module-enabled': useModuleEnabledStore.getState() }, NO_WS)
  // @ts-expect-error — editor preferences are device-local (the store's own header): not a settings source
  buildSettingsSection({ 'purdex-editor-settings': useEditorSettingsStore.getState() }, NO_WS)
  return { hosts, workspaces, tabs, settings }
}

// --- fixtures ----------------------------------------------------------------

const S = '__DEVICE_LOCAL__'
const N = 987654321 // sentinel number; appears nowhere else in the fixtures

function leaf(id: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

function split(id: string, children: PaneLayout[], sizes: number[], direction: 'h' | 'v' = 'h'): PaneLayout {
  return { type: 'split', id, direction, children, sizes }
}

function tab(id: string, layout: PaneLayout, extra: Partial<Tab> = {}): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout, ...extra }
}

function tmux(name: string, extra: Partial<Extract<PaneContent, { kind: 'tmux-session' }>> = {}): PaneContent {
  return { kind: 'tmux-session', hostId: 'h1', sessionCode: `code-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i1', ...extra }
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

function baseInput(): CollectInput {
  return {
    hosts: {
      hosts: { h1: host('h1', { token: 'tok', color: '#112233' }), h2: host('h2', { order: 1, token: null }) },
      hostOrder: ['h1', 'h2'],
    },
    workspaces: {
      workspaces: [
        ws('wsA', 'Alpha', ['t1', 't2'], { icon: 'Cube', iconWeight: 'bold', moduleConfig: { files: { root: '/a' } } }),
        ws('wsB', 'Beta', ['t3']),
        ws('wsEmpty', 'Empty', []),
      ],
    },
    tabs: {
      tabs: {
        t1: tab('t1', split('sp1', [leaf('p1', tmux('one')), leaf('p2', { kind: 'browser', url: 'https://a.test' })], [30, 70])),
        t2: tab('t2', leaf('p3', { kind: 'browser', url: 'https://b.test' }), { pinned: true }),
        t3: tab('t3', leaf('p4', tmux('three'))),
      },
      tabOrder: ['t1', 't2', 't3'],
    },
    settings: {
      'purdex-ui-settings': { terminalRenderer: 'webgl', keepAliveCount: 3 },
      'purdex-layout': { tabPosition: 'top' },
      'purdex-newtab-layout': { presets: { default: { columns: [] } } },
    },
  }
}

async function hashes(input: CollectInput): Promise<Record<string, string>> {
  const { document } = buildProfileDocument(input)
  const out: Record<string, string> = {}
  for (const key of Object.keys(document)) out[key] = await hashSection(document[key as keyof typeof document])
  return out
}

/** The section keys whose hash differs (or that exist on one side only). */
async function changed(a: CollectInput, b: CollectInput): Promise<string[]> {
  const [ha, hb] = [await hashes(a), await hashes(b)]
  return [...new Set([...Object.keys(ha), ...Object.keys(hb)])].filter((k) => ha[k] !== hb[k]).sort()
}

function hasKeyDeep(node: unknown, name: string): boolean {
  if (Array.isArray(node)) return node.some((item) => hasKeyDeep(item, name))
  if (typeof node !== 'object' || node === null) return false
  return Object.keys(node).some((k) => k === name || hasKeyDeep((node as Record<string, unknown>)[k], name))
}

// --- stripSizes --------------------------------------------------------------

describe('stripSizes', () => {
  it('removes sizes from every split, at any depth, and keeps the structure', () => {
    const layout = split('s1', [leaf('p1', { kind: 'dashboard' }), split('s2', [leaf('p2', { kind: 'hosts' }), leaf('p3', { kind: 'history' })], [20, 80], 'v')], [40, 60])
    expect(stripSizes(layout)).toEqual({
      type: 'split', id: 's1', direction: 'h',
      children: [
        { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } },
        { type: 'split', id: 's2', direction: 'v', children: [
          { type: 'leaf', pane: { id: 'p2', content: { kind: 'hosts' } } },
          { type: 'leaf', pane: { id: 'p3', content: { kind: 'history' } } },
        ] },
      ],
    })
  })

  it('does not mutate its input and returns a new tree', () => {
    const layout = deepFreeze(split('s1', [leaf('p1', { kind: 'dashboard' }), leaf('p2', { kind: 'hosts' })], [40, 60]))
    const out = stripSizes(layout)
    expect(out).not.toBe(layout)
    expect(layout).toHaveProperty('sizes', [40, 60])
  })
})

// --- buildWorkspacesSection --------------------------------------------------

describe('buildWorkspacesSection', () => {
  it('converts the array to order + record, without id, tabs or activeTabId', () => {
    const out = buildWorkspacesSection([
      ws('b', 'Beta', ['t1'], { activeTabId: 't1', icon: 'Cube', iconWeight: 'fill', moduleConfig: { m: { k: 1 } } }),
      ws('a', 'Alpha', []),
    ])
    expect(out).toEqual({
      order: ['b', 'a'],
      workspaces: { b: { name: 'Beta', icon: 'Cube', iconWeight: 'fill', moduleConfig: { m: { k: 1 } } }, a: { name: 'Alpha' } },
    })
  })

  // A workspace whose id cannot name a `tabs.<id>` section (importWorkspace
  // accepts any id) can never sync its tabs — and the guard on
  // the receiving side refuses a `workspaces` payload that lists it. It is
  // device-local as a whole: the builder leaves it out.
  it('leaves out a workspace whose id cannot form a tabs.<id> key — from order AND record', () => {
    const long = 'x'.repeat(65)
    const out = buildWorkspacesSection([ws('a', 'Alpha', []), ws('bad id!', 'Bad', ['t1']), ws('', 'Empty', []), ws(long, 'Long', []), ws('ok_Id-9', 'Ok', [])])
    expect(out).toEqual({ order: ['a', 'ok_Id-9'], workspaces: { a: { name: 'Alpha' }, 'ok_Id-9': { name: 'Ok' } } })
    expect(isWellFormedSection('workspaces', out)).toBe(true)
    expect(isWellFormedSection('workspaces', buildWorkspacesSection([ws('bad id!', 'Bad', [])]))).toBe(true)
  })

  it('unsyncableWorkspaceIds names exactly those workspaces, each once, in list order', () => {
    expect(unsyncableWorkspaceIds([ws('a', 'A', []), ws('bad id!', 'B', []), ws('b.c', 'C', []), ws('bad id!', 'dup', [])])).toEqual(['bad id!', 'b.c'])
    expect(unsyncableWorkspaceIds([ws('a', 'A', [])])).toEqual([])
  })

  it('an empty list still yields a well-formed payload', () => {
    expect(buildWorkspacesSection([])).toEqual({ order: [], workspaces: {} })
  })

  it('a duplicated workspace id is kept once, first occurrence', () => {
    const out = buildWorkspacesSection([ws('a', 'First', []), ws('a', 'Second', [])])
    expect(out).toEqual({ order: ['a'], workspaces: { a: { name: 'First' } } })
  })
})

// --- isSyncableTab -----------------------------------------------------------

/**
 * Every pane kind, decided. A `Record` over the union is exhaustive at compile time: a new kind in
 * `PaneContent` fails `tsc` here until someone chooses whether it travels (tabs-local-only spec §3.1).
 */
const KIND_DECISION: Record<PaneContent['kind'], 'device-local' | 'syncs'> = {
  'new-tab': 'device-local',
  settings: 'device-local',
  dashboard: 'device-local',
  hosts: 'device-local',
  history: 'device-local',
  'memory-monitor': 'device-local',
  'editor-buffers': 'device-local',
  'tmux-session': 'syncs',
  browser: 'syncs',
  editor: 'syncs',
  'image-preview': 'syncs',
  'pdf-preview': 'syncs',
  execution: 'syncs',
}

const FS: FileSource = { type: 'daemon', hostId: 'h1' }
function contentOf(kind: PaneContent['kind']): PaneContent {
  switch (kind) {
    case 'tmux-session': return tmux('x')
    case 'settings': return { kind, scope: 'global' }
    case 'browser': return { kind, url: 'https://a.test' }
    case 'editor': case 'image-preview': case 'pdf-preview': return { kind, source: FS, filePath: '/f' }
    case 'execution': return { kind, executionId: 'e1' }
    default: return { kind } as PaneContent
  }
}

describe('isSyncableTab', () => {
  it('the device-local set is exactly the kinds decided device-local (exhaustiveness)', () => {
    for (const [kind, decision] of Object.entries(KIND_DECISION)) {
      expect({ kind, local: DEVICE_LOCAL_PANE_KINDS.has(kind as PaneContent['kind']) }).toEqual({ kind, local: decision === 'device-local' })
    }
    expect(DEVICE_LOCAL_PANE_KINDS.size).toBe(7)
  })

  it('a tab whose single pane is of a kind: syncable iff the kind syncs', () => {
    for (const [kind, decision] of Object.entries(KIND_DECISION)) {
      const t = tab('t', leaf('p', contentOf(kind as PaneContent['kind'])))
      expect({ kind, syncable: isSyncableTab(t) }).toEqual({ kind, syncable: decision === 'syncs' })
    }
  })

  it('a split syncs when one leaf syncs; not when every leaf is device-local — at any depth', () => {
    expect(isSyncableTab(tab('t', split('s', [leaf('a', { kind: 'new-tab' }), leaf('b', tmux('x'))], [50, 50])))).toBe(true)
    expect(isSyncableTab(tab('t', split('s', [leaf('a', { kind: 'settings', scope: 'global' }), leaf('b', { kind: 'hosts' }), leaf('c', { kind: 'new-tab' })], [30, 30, 40])))).toBe(false)
    const deepLocal = split('s', [leaf('a', { kind: 'new-tab' }), split('s2', [leaf('b', { kind: 'history' }), leaf('c', { kind: 'dashboard' })], [50, 50], 'v')], [50, 50])
    expect(isSyncableTab(tab('t', deepLocal))).toBe(false)
    const deepSync = split('s', [leaf('a', { kind: 'new-tab' }), split('s2', [leaf('b', { kind: 'history' }), leaf('c', { kind: 'browser', url: 'u' })], [50, 50], 'v')], [50, 50])
    expect(isSyncableTab(tab('t', deepSync))).toBe(true)
  })

  it('reads a wire tab entry (stripped layout) the same way', () => {
    const entry = buildTabsSection(ws('w', 'W', ['t1']), { t1: tab('t1', split('s', [leaf('a', { kind: 'new-tab' }), leaf('b', tmux('x'))], [50, 50])) }).tabs.t1
    expect(isSyncableTab(entry)).toBe(true)
    expect(isSyncableTab({ layout: { type: 'split', id: 's', direction: 'h', children: [leaf('a', { kind: 'settings', scope: 'global' })] } })).toBe(false)
  })

  // T1 (R2 finding A): device-local ⇔ a COMPLETE layout (the guard's shape) whose every leaf is device-local.
  // A malformed tab is not device-local: it goes to the build exactly as on main, never kept as a ghost.
  describe('a malformed layout is NOT device-local (it syncs, as on main)', () => {
    const settingsLeaf = leaf('a', { kind: 'settings', scope: 'global' })
    const cases: Array<[string, unknown]> = [
      ['no layout', undefined],
      ['an unknown node type', { type: 'grid', pane: { id: 'p', content: { kind: 'settings' } } }],
      ['an empty split', { type: 'split', id: 's', direction: 'h', children: [], sizes: [] }],
      ['a split with a device-local leaf and an empty split', { type: 'split', id: 's', direction: 'h', children: [settingsLeaf, { type: 'split', id: 's2', direction: 'v', children: [], sizes: [] }], sizes: [50, 50] }],
      ['a leaf without a pane', { type: 'leaf' }],
      ['a leaf without content', { type: 'leaf', pane: { id: 'p' } }],
      ['a leaf whose pane has no id', { type: 'leaf', pane: { content: { kind: 'settings', scope: 'global' } } }],
      ['a split with a bad direction', { type: 'split', id: 's', direction: 'x', children: [settingsLeaf], sizes: [100] }],
    ]
    for (const [name, layout] of cases) {
      it(name, () => {
        expect(isSyncableTab({ layout } as unknown as Tab)).toBe(true)
      })
    }

    it('a local split carrying `sizes` is complete (the builder strips them): all device-local → device-local', () => {
      expect(isSyncableTab(tab('t', split('s', [settingsLeaf, leaf('b', { kind: 'hosts' })], [50, 50])))).toBe(false)
    })
  })
})

// --- buildTabsSection --------------------------------------------------------

describe('buildTabsSection', () => {
  it('order follows ws.tabs; ids with no Tab are dropped, duplicates kept once; record = order', () => {
    const tabs = { t1: tab('t1', leaf('p1', { kind: 'browser', url: 'u1' })), t2: tab('t2', leaf('p2', { kind: 'browser', url: 'u2' })), other: tab('other', leaf('p9', { kind: 'browser', url: 'u9' })) }
    const out = buildTabsSection(ws('w', 'W', ['t2', 'ghost', 't1', 't2']), tabs)
    expect(out.order).toEqual(['t2', 't1'])
    expect(Object.keys(out.tabs).sort()).toEqual(['t1', 't2'])
    expect(new Set(out.order).size).toBe(out.order.length)
  })

  it('leaves out interface-only tabs: [tmux, settings, new-tab, browser] → [tmux, browser], record exactly those', () => {
    const tabs = {
      a: tab('a', leaf('p1', tmux('one'))),
      s: tab('s', leaf('p2', { kind: 'settings', scope: 'global' })),
      n: tab('n', leaf('p3', { kind: 'new-tab' })),
      b: tab('b', leaf('p4', { kind: 'browser', url: 'https://a.test' })),
    }
    const out = buildTabsSection(ws('w', 'W', ['a', 's', 'n', 'b']), tabs)
    expect(out.order).toEqual(['a', 'b'])
    expect(Object.keys(out.tabs).sort()).toEqual(['a', 'b'])
    expect(isWellFormedSection('tabs', out)).toBe(true)
  })

  it('a workspace of device-local tabs only builds the empty payload', () => {
    const tabs = { s: tab('s', leaf('p1', { kind: 'settings', scope: 'global' })), h: tab('h', leaf('p2', { kind: 'hosts' })) }
    expect(buildTabsSection(ws('w', 'W', ['s', 'h']), tabs)).toEqual({ order: [], tabs: {} })
  })

  it('a split new-tab + tmux is sent whole, its new-tab leaf included', () => {
    const layout = split('sp', [leaf('p1', { kind: 'new-tab' }), leaf('p2', tmux('x'))], [50, 50])
    const out = buildTabsSection(ws('w', 'W', ['t']), { t: tab('t', layout) })
    expect(out.order).toEqual(['t'])
    expect(out.tabs.t.layout).toEqual(stripSizes(layout))
  })

  it('a workspace with no tabs yields {order: [], tabs: {}}', () => {
    expect(buildTabsSection(ws('w', 'W', []), {})).toEqual({ order: [], tabs: {} })
  })

  it('carries the tab fields and the split structure, and no sizes at any depth', () => {
    const layout = split('s1', [leaf('p1', { kind: 'browser', url: 'u1' }), split('s2', [leaf('p2', { kind: 'hosts' }), leaf('p3', { kind: 'history' })], [20, 80], 'v')], [40, 60])
    const out = buildTabsSection(ws('w', 'W', ['t1']), { t1: tab('t1', layout, { pinned: true, locked: true, createdAt: 42 }) })
    expect(out.tabs.t1).toMatchObject({ id: 't1', pinned: true, locked: true, createdAt: 42 })
    expect(out.tabs.t1.layout).toEqual(stripSizes(layout))
    expect(hasKeyDeep(out, 'sizes')).toBe(false)
  })

  it('preserves a PaneRebuildRecord verbatim, field by field (decision 13)', () => {
    const rebuild: PaneRebuildRecord = {
      sessionName: 'work-2', tmuxInstance: 'inst-9', cwd: '/srv/app', cwdSource: 'agent-session-start',
      agent: { type: 'cc', sessionId: 'sid-1', tmuxPaneId: '%4', updatedAt: 1700000000 },
      resumeCommandOverride: 'claude --resume sid-1', unverified: true, capturedAt: 1700000001,
    }
    const out = buildTabsSection(ws('w', 'W', ['t1']), { t1: tab('t1', leaf('p1', tmux('work', { rebuild, terminated: 'tmux-restarted' }))) })
    const entry = out.tabs.t1.layout
    if (entry.type !== 'leaf' || entry.pane.content.kind !== 'tmux-session') throw new Error('expected a tmux leaf')
    expect(entry.pane.content.rebuild).toEqual(rebuild)
    expect(entry.pane.content.rebuild).not.toBe(rebuild)
    expect(entry.pane.content.terminated).toBe('tmux-restarted')
  })
})

// --- buildHostsSection -------------------------------------------------------

describe('buildHostsSection', () => {
  it('carries exactly the projected host fields and the order', () => {
    const full = host('h1', { token: 'tok', order: 2, color: '#112233', colors: { console: { main: { color: '#112233', alpha: 100 }, light: { alpha: 22 } } }, icon: 'Laptop', iconWeight: 'duotone' })
    const out = buildHostsSection({ hosts: { h1: full }, hostOrder: ['h1'] })
    expect(out).toEqual({ hosts: { h1: full }, hostOrder: ['h1'] })
    expect(out.hosts.h1).not.toBe(full)
  })

  it('keeps token: null (cleared, re-auth required) distinct from an absent token', () => {
    const out = buildHostsSection({ hosts: { a: host('a', { token: null }), b: host('b') }, hostOrder: ['a', 'b'] })
    expect(out.hosts.a.token).toBeNull()
    expect(Object.hasOwn(out.hosts.a, 'token')).toBe(true)
    expect(Object.hasOwn(out.hosts.b, 'token')).toBe(false)
  })

  it('drops everything else the store state carries', () => {
    const state = { hosts: { h1: { ...host('h1'), extra: S } }, hostOrder: ['h1'], activeHostId: S, devHostId: S, runtime: { h1: { status: S } } }
    expect(structuralKey(buildHostsSection(state as unknown as HostsSource))).not.toContain(S)
  })

  it('drops a hostOrder id that has no host (reorderHosts does not check ids), so the receiver accepts the section', () => {
    const source = deepFreeze({ hosts: { a: host('a'), b: host('b') }, hostOrder: ['a', 'ghost', 'b'] })
    const out = buildHostsSection(source)
    expect(out.hostOrder).toEqual(['a', 'b'])
    expect(Object.keys(out.hosts).sort()).toEqual(['a', 'b'])
    expect(isWellFormedSection('hosts', out)).toBe(true)
    expect(source.hostOrder).toEqual(['a', 'ghost', 'b']) // input untouched
  })

  it('keeps a repeated hostOrder id once, at its first occurrence', () => {
    const out = buildHostsSection(deepFreeze({ hosts: { a: host('a'), b: host('b') }, hostOrder: ['b', 'a', 'b', 'a'] }))
    expect(out.hostOrder).toEqual(['b', 'a'])
    expect(isWellFormedSection('hosts', out)).toBe(true)
  })

  it('does not add a host that hostOrder never mentions (the store state is reflected, not repaired)', () => {
    const out = buildHostsSection({ hosts: { a: host('a'), b: host('b') }, hostOrder: ['b'] })
    expect(out.hostOrder).toEqual(['b'])
    expect(Object.keys(out.hosts).sort()).toEqual(['a', 'b'])
    expect(isWellFormedSection('hosts', out)).toBe(true)
  })
})

// --- buildSettingsSection ----------------------------------------------------

describe('buildSettingsSection', () => {
  it('outputs {storageKey: {listed fields}} and nothing unlisted — functions included', () => {
    const out = buildSettingsSection({
      'purdex-ui-settings': { terminalRenderer: 'dom', terminalSettingsVersion: N, setTerminalRenderer: () => undefined },
      'purdex-layout': { tabPosition: 'left', activityBarWidth: N, regions: { a: S } },
    }, NO_WS)
    expect(out).toEqual({ 'purdex-ui-settings': { terminalRenderer: 'dom' }, 'purdex-layout': { tabPosition: 'left' } })
  })

  it('skips an absent store, and a store none of whose listed fields is present (no empty object)', () => {
    const out = buildSettingsSection({ 'purdex-themes': { activeThemeId: 'dark' }, 'purdex-layout': { regions: {} } }, NO_WS)
    expect(out).toEqual({ 'purdex-themes': { activeThemeId: 'dark' } })
    expect(buildSettingsSection({}, NO_WS)).toEqual({})
  })

  it('ignores a storage key that is not one of the eight', () => {
    const out = buildSettingsSection({ 'purdex-tabs': { tabs: S } } as unknown as SettingsBuildInput, NO_WS)
    expect(out).toEqual({})
  })

  it('module on/off is device-local: useModuleEnabledStore contributes nothing, `enabled` included', () => {
    const out = buildSettingsSection({
      'purdex-ui-settings': { keepAliveCount: 3 },
      'purdex-module-enabled': { enabled: { files: false }, baseline: { files: true } },
    } as unknown as SettingsBuildInput, NO_WS)
    expect(out).toEqual({ 'purdex-ui-settings': { keepAliveCount: 3 } })
  })

  it('editor preferences are device-local: useEditorSettingsStore contributes nothing', () => {
    const out = buildSettingsSection({
      'purdex-ui-settings': { keepAliveCount: 3 },
      'purdex-editor-settings': { fontSize: 11, tabSize: 2, wordWrap: 'on' },
    } as unknown as SettingsBuildInput, NO_WS)
    expect(out).toEqual({ 'purdex-ui-settings': { keepAliveCount: 3 } })
  })

  describe('workspace-scoped settings: the master’s workspaces only', () => {
    const scoped = (): SettingsBuildInput => ({
      'purdex-workspace-settings': { workspaces: { m1: { files: { root: '/m1' } }, m2: { files: { root: '/m2' } }, slave: { files: { root: S } }, orphan: { files: { root: S } } } },
      'purdex-host-settings': { hosts: { slave: { x: 1 }, h1: { x: 2 } } },
      'purdex-newtab-layout': { presets: { slave: { cols: 2 } } },
    })

    it('carries an entry iff its key is a master workspace id', () => {
      const out = buildSettingsSection(scoped(), new Set(['m1', 'm2', 'not-in-the-store']))
      expect(out['purdex-workspace-settings']).toEqual({ workspaces: { m1: { files: { root: '/m1' } }, m2: { files: { root: '/m2' } } } })
      expect(JSON.stringify(out)).not.toContain(S)
    })

    it('filters NOTHING else: a record of another store that happens to use the same key is untouched', () => {
      const out = buildSettingsSection(scoped(), new Set(['m1']))
      expect(out['purdex-host-settings']).toEqual({ hosts: { slave: { x: 1 }, h1: { x: 2 } } })
      expect(out['purdex-newtab-layout']).toEqual({ presets: { slave: { cols: 2 } } })
    })

    it('with no master entry left the field is `{}`, not absent — what a store with no entry builds', () => {
      expect(buildSettingsSection(scoped(), new Set())['purdex-workspace-settings']).toEqual({ workspaces: {} })
      expect(buildSettingsSection({ 'purdex-workspace-settings': { workspaces: {} } }, new Set(['m1']))).toEqual({ 'purdex-workspace-settings': { workspaces: {} } })
    })

    it('does not mutate its input', () => {
      const input = scoped()
      const before = structuralKey(input)
      buildSettingsSection(input, new Set(['m1']))
      expect(structuralKey(input)).toBe(before)
    })
  })
})

// --- buildProfileDocument ----------------------------------------------------

describe('buildProfileDocument', () => {
  it('has hosts, settings, workspaces and one tabs.<id> per workspace — empty ones included', () => {
    const { document } = buildProfileDocument(baseInput())
    expect(Object.keys(document).sort()).toEqual(['hosts', 'settings', 'tabs.wsA', 'tabs.wsB', 'tabs.wsEmpty', 'workspaces'])
    expect(document['tabs.wsEmpty']).toEqual({ order: [], tabs: {} })
    expect(document['tabs.wsA']).toMatchObject({ order: ['t1', 't2'] })
  })

  // (Was: "…and is reported, in tabOrder order" — `standaloneTabIds` is gone in P3c-2; adoption is the app's.)
  it('a tab in no workspace enters no section', () => {
    const input = baseInput()
    input.tabs = {
      tabs: { ...input.tabs.tabs, loose2: tab('loose2', leaf('px', { kind: 'hosts' })), loose1: tab('loose1', leaf('py', { kind: 'history' })) },
      tabOrder: ['loose1', 't1', 'loose2', 't2', 't3'],
    }
    const { document } = buildProfileDocument(input)
    expect(Object.keys(document).sort()).toEqual(['hosts', 'settings', 'tabs.wsA', 'tabs.wsB', 'tabs.wsEmpty', 'workspaces'])
    expect(structuralKey(document)).not.toContain('loose')
  })

  it('workspace-scoped settings follow the `workspaces` section: a workspace that is not in it (unknown, or unsyncable id) has no entry', () => {
    const input = baseInput()
    input.workspaces = { workspaces: [...input.workspaces.workspaces, ws('bad id!', 'Bad', [])] }
    input.settings = { ...input.settings, 'purdex-workspace-settings': { workspaces: { wsA: { files: { root: '/a' } }, 'bad id!': { files: { root: S } }, gone: { files: { root: S } } } } }
    const { document } = buildProfileDocument(input)
    expect((document.settings as Record<string, unknown>)['purdex-workspace-settings']).toEqual({ workspaces: { wsA: { files: { root: '/a' } } } })
  })

  it('a workspace id the daemon would reject is device-local: no entry, no tabs section, and its tabs are not up for adoption', () => {
    const input = baseInput()
    input.workspaces = { workspaces: [...input.workspaces.workspaces, ws('bad id!', 'Bad', ['tBad'])] }
    input.tabs = { tabs: { ...input.tabs.tabs, tBad: tab('tBad', leaf('pBad', { kind: 'hosts' })) }, tabOrder: [...input.tabs.tabOrder, 'tBad'] }
    const { document } = buildProfileDocument(input)
    expect(Object.keys(document).sort()).toEqual(['hosts', 'settings', 'tabs.wsA', 'tabs.wsB', 'tabs.wsEmpty', 'workspaces'])
    expect(structuralKey(document)).not.toContain('bad id!')
    expect(structuralKey(document)).not.toContain('tBad')
    // Owned, if by a workspace that does not travel: nothing for `adoptStandaloneTabs` to move.
    expect(adoptStandaloneTabs({ workspaces: input.workspaces.workspaces, ...input.tabs }, { unsortedName: 'Unsorted', newWorkspaceId: 'ws-new' }).adopted).toEqual([])
  })

  it('every tabs section is well-formed: order has no duplicates and equals the record keys', () => {
    const input = baseInput()
    input.workspaces = { workspaces: [ws('wsA', 'Alpha', ['t1', 'ghost', 't2', 't1']), ws('wsB', 'Beta', ['t3'])] }
    const { document } = buildProfileDocument(input)
    for (const key of ['tabs.wsA', 'tabs.wsB'] as const) {
      const payload = document[key] as { order: string[]; tabs: Record<string, unknown> }
      expect(new Set(payload.order).size).toBe(payload.order.length)
      expect([...payload.order].sort()).toEqual(Object.keys(payload.tabs).sort())
    }
  })

  it('NO device-local field leaks: a sentinel in every one of them appears nowhere in the document', () => {
    const input = {
      hosts: {
        hosts: { h1: { ...host('h1', { token: 'tok' }), lastSeen: S } },
        hostOrder: ['h1'],
        activeHostId: S,
        devHostId: S,
        runtime: { h1: { status: S, latency: N } },
      },
      workspaces: {
        workspaces: [ws('wsA', 'Alpha', ['t1'], { activeTabId: S }), ws('wsB', 'Beta', ['t2'], { activeTabId: S })],
        activeWorkspaceId: S,
      },
      tabs: {
        tabs: {
          t1: { ...tab('t1', split('sp1', [leaf('p1', tmux('one')), split('sp2', [leaf('p2', { kind: 'hosts' }), leaf('p3', { kind: 'history' })], [N, N])], [N, N])), scrollTop: S },
          t2: tab('t2', leaf('p4', { kind: 'browser', url: 'u4' })),
          // standalone — its content must not travel either
          [S]: tab(S, leaf('p5', { kind: 'browser', url: S })),
        },
        tabOrder: [S, 't1', 't2'],
        activeTabId: S,
        visitHistory: [S, S],
      },
      settings: {
        'purdex-ui-settings': { terminalRenderer: 'webgl', terminalSettingsVersion: N },
        'purdex-newtab-layout': { presets: {}, activeEditingPreset: S, knownIds: [S] },
        'purdex-layout': { tabPosition: 'top', regions: { primary: S }, activityBarWidth: N, activityBarWideSize: N, workspaceExpanded: { wsA: S } },
        'purdex-device-state': { baseline: S },
        // the WHOLE store is device-local — `enabled` as much as the in-memory `baseline`
        'purdex-module-enabled': { enabled: { files: S, [S]: true }, baseline: { files: S } },
        // the WHOLE store is device-local — every one of its nine fields
        'purdex-editor-settings': {
          tabSize: N, insertSpaces: S, wordWrap: S, lineNumbers: S, minimap: S, fontSize: N, popupOnMissingFile: S,
          autoSearchLayer1: S, contentWidth: S,
        },
      },
      baseline: S,
      activeWorkspaceId: S,
    } as unknown as CollectInput

    const { document } = buildProfileDocument(input)
    const key = structuralKey(document)
    expect(key).not.toContain(S)
    expect(key).not.toContain(String(N))
    expect(hasKeyDeep(document, 'sizes')).toBe(false)
    expect(hasKeyDeep(document, 'purdex-module-enabled')).toBe(false)
    expect(hasKeyDeep(document, 'enabled')).toBe(false)
    expect(hasKeyDeep(document, 'purdex-editor-settings')).toBe(false)
    expect(hasKeyDeep(document, 'fontSize')).toBe(false)
    // …and the fixture is not vacuous: the synced neighbours of those fields did travel.
    expect(key).toContain('"terminalRenderer":"webgl"')
    expect(key).toContain('"tabPosition":"top"')
    expect(key).toContain('"sp2"')
  })

  it('never mutates its input', () => {
    const input = deepFreeze(baseInput())
    expect(() => buildProfileDocument(input)).not.toThrow()
    expect(input).toEqual(baseInput())
  })
})

// --- which section a change lands in -----------------------------------------

describe('section hashes: a change lands in exactly the sections it belongs to', () => {
  it('resizing a split changes nothing', async () => {
    const b = baseInput()
    b.tabs.tabs.t1 = tab('t1', split('sp1', [leaf('p1', tmux('one')), leaf('p2', { kind: 'browser', url: 'https://a.test' })], [55, 45]))
    expect(await changed(baseInput(), b)).toEqual([])
  })

  it('splitting a tab changes only that workspace\'s tabs section', async () => {
    const b = baseInput()
    b.tabs.tabs.t3 = tab('t3', split('spNew', [leaf('p4', tmux('three')), leaf('p5', { kind: 'new-tab' })], [50, 50], 'v'))
    expect(await changed(baseInput(), b)).toEqual(['tabs.wsB'])
  })

  it('reordering workspaces changes only workspaces', async () => {
    const b = baseInput()
    b.workspaces = { workspaces: [...b.workspaces.workspaces].reverse() }
    expect(await changed(baseInput(), b)).toEqual(['workspaces'])
  })

  it('moving a tab from A to B changes exactly tabs.A and tabs.B', async () => {
    const b = baseInput()
    const [a, bb, empty] = b.workspaces.workspaces
    b.workspaces = { workspaces: [{ ...a, tabs: ['t1'] }, { ...bb, tabs: ['t3', 't2'] }, empty] }
    expect(await changed(baseInput(), b)).toEqual(['tabs.wsA', 'tabs.wsB'])
  })

  it('a host colour change changes only hosts', async () => {
    const b = baseInput()
    b.hosts.hosts.h1 = { ...b.hosts.hosts.h1, color: '#abcdef' }
    expect(await changed(baseInput(), b)).toEqual(['hosts'])
  })

  // host ownership §4.4: until H3 the `hosts` section still carries HostConfig name / colours / icon, and the look
  // store travels in `settings` — the two never feed each other.
  describe('host looks (host ownership §4.4)', () => {
    const LOOKS = { 'purdex-host-looks': { looks: { h1: { name: 'look-one', colors: { console: { main: { color: '#445566', alpha: 100 } } } } } } }
    const withLooks = (input: CollectInput, looks: object = LOOKS): CollectInput => ({ ...input, settings: { ...input.settings, ...looks } })

    it('a HostConfig rename + recolour changes only hosts — settings, looks included, does not move', async () => {
      const b = withLooks(baseInput())
      b.hosts.hosts.h1 = { ...b.hosts.hosts.h1, name: 'renamed', color: '#abcdef', icon: 'Laptop' }
      expect(await changed(withLooks(baseInput()), b)).toEqual(['hosts'])
    })

    it('a look change changes only settings — hosts does not move', async () => {
      const b = withLooks(baseInput(), { 'purdex-host-looks': { looks: { h1: { name: 'other' }, d1_far: { icon: 'Laptop' } } } })
      expect(await changed(withLooks(baseInput()), b)).toEqual(['settings'])
    })

    it('buildHostsSection is identical whatever the look store holds, and building it notifies no look-store subscriber', () => {
      const src = baseInput().hosts
      useHostLookStore.setState({ looks: {} })
      const empty = JSON.stringify(buildHostsSection(src))
      useHostLookStore.setState({ looks: { h1: { name: 'look-one', icon: 'Laptop' }, d1_far: { name: 'far' } } })
      const spy = vi.fn()
      const unsub = useHostLookStore.subscribe(spy)
      const full = JSON.stringify(buildHostsSection(src))
      buildProfileDocument(baseInput())
      unsub()
      expect(full).toBe(empty)
      expect(full).not.toContain('look-one')
      expect(spy).not.toHaveBeenCalled()
      useHostLookStore.setState({ looks: {} })
    })

    it('buildSettingsSection does not change when a HostConfig name / colour changes (the identity it builds through is the same)', () => {
      const a = baseInput()
      const b = baseInput()
      b.hosts.hosts.h1 = { ...b.hosts.hosts.h1, name: 'renamed', color: '#abcdef', colors: { console: { main: { color: '#abcdef', alpha: 100 } } } }
      const settings = withLooks(a).settings
      expect(JSON.stringify(buildSettingsSection(settings, NO_WS, identityOfSync(b.hosts.hosts)))).toBe(
        JSON.stringify(buildSettingsSection(settings, NO_WS, identityOfSync(a.hosts.hosts))),
      )
    })
  })

  it('a UI setting change changes only settings', async () => {
    const b = baseInput()
    b.settings = { ...b.settings, 'purdex-ui-settings': { terminalRenderer: 'dom', keepAliveCount: 3 } }
    expect(await changed(baseInput(), b)).toEqual(['settings'])
  })

  it('switching the active tab / workspace / host changes nothing', async () => {
    const a = baseInput()
    const b = baseInput()
    b.workspaces = {
      workspaces: b.workspaces.workspaces.map((w) => ({ ...w, activeTabId: w.tabs[w.tabs.length - 1] ?? null })),
      activeWorkspaceId: 'wsB',
    } as WorkspacesSource
    b.tabs = { ...b.tabs, activeTabId: 't3', tabOrder: ['t3', 't2', 't1'] } as TabsSource
    b.hosts = { ...b.hosts, activeHostId: 'h2', devHostId: 'h2' } as HostsSource
    expect(await changed(a, b)).toEqual([])
  })
})

// --- adoptStandaloneTabs -----------------------------------------------------

describe('adoptStandaloneTabs', () => {
  const opts = { unsortedName: 'Unsorted', newWorkspaceId: 'ws-new' }
  const tabsOf = (...ids: string[]): Record<string, Tab> => Object.fromEntries(ids.map((id) => [id, tab(id, leaf(`p-${id}`, { kind: 'dashboard' }))]))

  it('nothing to adopt → same workspaces, nothing created', () => {
    const workspaces = [ws('a', 'A', ['t1'])]
    const out = adoptStandaloneTabs({ workspaces, tabs: tabsOf('t1'), tabOrder: ['t1'] }, opts)
    expect(out).toEqual({ workspaces, adopted: [], createdWorkspaceId: null })
  })

  it('appends to an existing workspace named like unsortedName — the first one, if several', () => {
    const workspaces = [ws('a', 'A', ['t1']), ws('u1', 'Unsorted', ['t2'], { activeTabId: 't2' }), ws('u2', 'Unsorted', [])]
    const out = adoptStandaloneTabs({ workspaces, tabs: tabsOf('t1', 't2', 'x', 'y'), tabOrder: ['y', 't1', 'x', 't2'] }, opts)
    expect(out.createdWorkspaceId).toBeNull()
    expect(out.adopted).toEqual(['y', 'x'])
    expect(out.workspaces).toEqual([workspaces[0], { ...workspaces[1], tabs: ['t2', 'y', 'x'] }, workspaces[2]])
  })

  it('creates the workspace, with the given id, only when there is something to adopt', () => {
    const workspaces = [ws('a', 'A', ['t1'])]
    const out = adoptStandaloneTabs({ workspaces, tabs: tabsOf('t1', 'x', 'y'), tabOrder: ['x', 't1', 'y'] }, opts)
    expect(out.createdWorkspaceId).toBe('ws-new')
    expect(out.adopted).toEqual(['x', 'y'])
    expect(out.workspaces).toEqual([workspaces[0], { id: 'ws-new', name: 'Unsorted', tabs: ['x', 'y'], activeTabId: null, moduleConfig: {} }])
  })

  it('ignores tabOrder ids with no Tab, and adopts a tab missing from tabOrder last', () => {
    const out = adoptStandaloneTabs({ workspaces: [], tabs: tabsOf('hidden', 'x'), tabOrder: ['ghost', 'x', 'x'] }, opts)
    expect(out.adopted).toEqual(['x', 'hidden'])
  })

  it('after adoption nothing is left to adopt, and the document carries the tab', () => {
    const input = baseInput()
    input.tabs = { tabs: { ...input.tabs.tabs, loose: tab('loose', leaf('px', { kind: 'browser', url: 'ux' })) }, tabOrder: ['loose', 't1', 't2', 't3'] }
    const adopted = adoptStandaloneTabs({ workspaces: input.workspaces.workspaces, ...input.tabs }, opts)
    const { document } = buildProfileDocument({ ...input, workspaces: { workspaces: adopted.workspaces } })
    expect(adoptStandaloneTabs({ workspaces: adopted.workspaces, ...input.tabs }, opts).adopted).toEqual([])
    expect(document['tabs.ws-new']).toMatchObject({ order: ['loose'] })
  })

  it('never mutates its input', () => {
    const world = deepFreeze({ workspaces: [ws('u', 'Unsorted', ['t1'])], tabs: tabsOf('t1', 'x'), tabOrder: ['t1', 'x'] })
    const out = adoptStandaloneTabs(world, opts)
    expect(out.workspaces[0].tabs).toEqual(['t1', 'x'])
    expect(world.workspaces[0].tabs).toEqual(['t1'])
  })
})

// --- repairTabOwnership: the ONE rule of "every tab in exactly one workspace" (adopt-standalone.ts + switch-active.ts)

describe('repairTabOwnership', () => {
  const opts = { unsortedName: 'Unsorted', newWorkspaceId: 'unsorted' }
  const tabsOf = (...ids: string[]): Record<string, Tab> => Object.fromEntries(ids.map((id) => [id, tab(id, leaf(`p-${id}`, { kind: 'dashboard' }))]))
  const base = { tabs: tabsOf('t1', 't2', 'o'), tabOrder: ['t1', 't2', 'o'] }

  it('a clean world: nothing changed, the pointer as it was', () => {
    const out = repairTabOwnership({ workspaces: [ws('a', 'A', ['t1', 't2', 'o'])], ...base, activeTabId: 't1', activeWorkspaceId: 'a' }, opts)
    expect(out).toMatchObject({ membershipChanged: false, adopted: [], dropped: 0, activeWorkspaceId: 'a' })
  })

  it('zero owners → Unsorted; several → the first keeps it and the loser\'s active tab is cleared; and it is idempotent', () => {
    const world = { workspaces: [ws('a', 'A', ['t1', 't2']), { ...ws('b', 'B', ['t2']), activeTabId: 't2' }], ...base, activeTabId: 't1', activeWorkspaceId: 'a' }
    const out = repairTabOwnership(deepFreeze(world), opts)
    expect(out.workspaces.map((w) => [w.id, w.tabs, w.activeTabId])).toEqual([['a', ['t1', 't2'], null], ['b', [], null], ['unsorted', ['o'], null]])
    expect(out).toMatchObject({ adopted: ['o'], dropped: 1, membershipChanged: true, activeWorkspaceId: 'a' })
    const again = repairTabOwnership({ ...world, workspaces: out.workspaces, activeWorkspaceId: out.activeWorkspaceId }, opts)
    expect(again).toMatchObject({ membershipChanged: false, activeWorkspaceId: 'a' })
    expect(again.workspaces).toEqual(out.workspaces)
  })

  it('`only`: those tab ids are repaired and every other broken one is left exactly as it is; absent = all', () => {
    const world = { workspaces: [ws('a', 'A', ['t1', 't2']), ws('b', 'B', ['t1', 't2'])], tabs: tabsOf('t1', 't2', 'o', 'p'), tabOrder: ['t1', 't2', 'o', 'p'], activeTabId: null, activeWorkspaceId: 'a' }
    const some = repairTabOwnership(deepFreeze(world), { ...opts, only: new Set(['t2', 'p']) })
    expect(some.workspaces.map((w) => [w.id, w.tabs])).toEqual([['a', ['t1', 't2']], ['b', ['t1']], ['unsorted', ['p']]])
    expect(some).toMatchObject({ adopted: ['p'], dropped: 1, membershipChanged: true })
    const none = repairTabOwnership(world, { ...opts, only: new Set() })
    expect(none.membershipChanged).toBe(false)
    expect(none.workspaces).toEqual(world.workspaces)
    const all = repairTabOwnership(world, opts)
    expect(all.workspaces.map((w) => [w.id, w.tabs])).toEqual([['a', ['t1', 't2']], ['b', []], ['unsorted', ['o', 'p']]])
  })

  it.each([
    ['the tab on screen is the one adopted → the pointer follows it', { activeTabId: 'o', activeWorkspaceId: 'a' }, [ws('a', 'A', ['t1', 't2'])], 'unsorted'],
    ['another tab is adopted → the pointer stays', { activeTabId: 't1', activeWorkspaceId: 'a' }, [ws('a', 'A', ['t1', 't2'])], 'a'],
    ['the pointed-at workspace loses the tab on screen → the pointer follows it', { activeTabId: 't2', activeWorkspaceId: 'b' }, [ws('a', 'A', ['t1', 't2']), ws('b', 'B', ['t2', 'o'])], 'a'],
    ['a THIRD workspace is pointed at → the user chose that; it stays', { activeTabId: 't2', activeWorkspaceId: 'c' }, [ws('a', 'A', ['t1', 't2']), ws('b', 'B', ['t2']), ws('c', 'C', ['o'])], 'c'],
    ['no pointer → the owner of the tab on screen', { activeTabId: 't2', activeWorkspaceId: null }, [ws('a', 'A', ['t1']), ws('b', 'B', ['t2', 'o'])], 'b'],
    ['no pointer, no tab on screen → the first workspace', { activeTabId: null, activeWorkspaceId: null }, [ws('a', 'A', ['t1']), ws('b', 'B', ['t2', 'o'])], 'a'],
  ])('%s', (_name, focus, workspaces, expected) => {
    expect(repairTabOwnership({ workspaces, ...base, ...focus }, opts).activeWorkspaceId).toBe(expected)
  })
})

// --- host-sync-identity PR 2: the builders speak WIRE ids --------------------

describe('host-sync-identity: local → wire at build', () => {
  const DAEMON = 'mini-lab:278cbm'
  const WIRE = syncIdOfSync(DAEMON)
  const hostCfg = (id: string, extra: Partial<HostConfig> = {}): HostConfig => ({ id, name: `N-${id}`, ip: '10.0.0.1', port: 7860, order: 0, ...extra })
  const hostsSrc = (): HostsSource => ({
    hosts: {
      bbbbbb: hostCfg('bbbbbb', { daemonId: DAEMON, syncAliases: ['aaaaaa'] }),
      legacy: hostCfg('legacy', { syncAliases: ['zzzzzz'] }), // no claim: travels under its local id, never with aliases
    },
    hostOrder: ['bbbbbb', 'legacy'],
  })

  it('hosts: a claimed host is keyed, `.id`-ed and ordered by its sync id and carries its syncAliases as `aliases`; a host with no claim keeps its local id', () => {
    const p = buildHostsSection(hostsSrc())
    expect(Object.keys(p.hosts)).toEqual([WIRE, 'legacy'])
    expect(p.hostOrder).toEqual([WIRE, 'legacy'])
    // aliases: the legacy keys it was matched from, AND its own local id (its wire key in the ordinal-2 era)
    expect(p.hosts[WIRE]).toEqual({ id: WIRE, name: 'N-bbbbbb', ip: '10.0.0.1', port: 7860, order: 0, daemonId: DAEMON, aliases: ['aaaaaa', 'bbbbbb'] }) // sorted
    expect(p.hosts.legacy).toEqual({ id: 'legacy', name: 'N-legacy', ip: '10.0.0.1', port: 7860, order: 0 })
    expect(JSON.stringify(p)).not.toContain('syncAliases')
    expect(isWellFormedSection('hosts', p)).toBe(true)
  })

  it('hosts: a canonical row\'s aliases are the SORTED unique union of the remembered ones and its own id, the first 16 (A1: one list every client computes)', () => {
    const at = (own: string, syncAliases?: string[]) =>
      (buildHostsSection({ hosts: { [own]: hostCfg(own, { daemonId: DAEMON, syncAliases }) }, hostOrder: [own] }).hosts[WIRE] as { aliases?: string[] }).aliases
    expect(at('bbbbbb')).toEqual(['bbbbbb'])
    expect(at('bbbbbb', ['zzzzzz', 'aaaaaa'])).toEqual(['aaaaaa', 'bbbbbb', 'zzzzzz'])
    const sixteen = Array.from({ length: 16 }, (_, i) => `x${i.toString(36).padStart(2, '0')}`)
    // full, own id sorts last: it is simply not listed — the build equals the row it came from
    expect(at('zzzzzz', sixteen)).toEqual(sixteen)
    // full, own id sorts first: it goes in, the LARGEST goes out
    expect(at('aaaaaa', sixteen)).toEqual(['aaaaaa', ...sixteen.slice(0, 15)])
    const s = { hosts: { aaaaaa: hostCfg('aaaaaa', { daemonId: DAEMON, syncAliases: sixteen }) }, hostOrder: ['aaaaaa'] }
    expect(isWellFormedSection('hosts', buildHostsSection(s))).toBe(true)
  })

  it('hosts: a stray local `aliases` field never travels (only syncAliases, and only on a canonical row)', () => {
    const src = hostsSrc()
    ;(src.hosts.legacy as unknown as Record<string, unknown>).aliases = ['x']
    ;(src.hosts.bbbbbb as unknown as Record<string, unknown>).aliases = ['y']
    const p = buildHostsSection({ ...src, hosts: { ...src.hosts, bbbbbb: { ...src.hosts.bbbbbb, syncAliases: undefined } } })
    expect(Object.hasOwn(p.hosts.legacy, 'aliases')).toBe(false)
    expect((p.hosts[WIRE] as { aliases?: string[] }).aliases).toEqual(['bbbbbb']) // its own id only — never the stray field's
  })

  it('hosts / tabs / settings refuse to build under an identity conflict (two hosts, one daemon)', () => {
    const src: HostsSource = { hosts: { a: hostCfg('a', { daemonId: DAEMON }), b: hostCfg('b', { daemonId: DAEMON }) }, hostOrder: ['a', 'b'] }
    const identity = identityOfSync(src.hosts)
    expect(identity.conflict).toEqual(['a', 'b'])
    expect(() => buildHostsSection(src)).toThrow(/host identity conflict/)
    expect(() => buildTabsSection({ id: 'w', name: 'W', tabs: [], activeTabId: null }, {}, identity)).toThrow(/host identity conflict/)
    expect(() => buildSettingsSection({}, NO_WS, identity)).toThrow(/host identity conflict/)
    expect(wireResolverOf(src)).toBeNull()
  })

  it('tabs: every host-bearing pane field is translated through the identity; an id it does not know passes through', () => {
    const identity = identityOfSync(hostsSrc().hosts)
    const layout = split('s', [
      leaf('p1', tmux('one', { hostId: 'bbbbbb' })),
      leaf('p2', { kind: 'editor', source: { type: 'daemon', hostId: 'bbbbbb' }, filePath: '/f' } as PaneContent),
      leaf('p3', { kind: 'execution', host: 'bbbbbb' } as unknown as PaneContent),
      leaf('p4', tmux('gone', { hostId: 'gone00' })),
      leaf('p5', tmux('legacy', { hostId: 'legacy' })),
    ], [20, 20, 20, 20, 20])
    const p = buildTabsSection({ id: 'w', name: 'W', tabs: ['t1'], activeTabId: 't1' }, { t1: tab('t1', layout) }, identity)
    const kids = (p.tabs.t1.layout as { children: Array<{ pane: { content: Record<string, unknown> } }> }).children.map((c) => c.pane.content)
    expect(kids[0].hostId).toBe(WIRE)
    expect((kids[1].source as { hostId: string }).hostId).toBe(WIRE)
    expect(kids[2].host).toBe(WIRE)
    expect(kids[3].hostId).toBe('gone00')
    expect(kids[4].hostId).toBe('legacy')
    // no identity: nothing is translated (the pure builder's old behaviour — production passes one)
    const plain = buildTabsSection({ id: 'w', name: 'W', tabs: ['t1'], activeTabId: 't1' }, { t1: tab('t1', layout) })
    expect(JSON.stringify(plain)).not.toContain(WIRE)
  })

  // host ownership H2c-1 (spec §4.1): look keys are wire ids IN the store — the build maps nothing, WITH an identity.
  it('settings: host-look keys pass through verbatim — a local id the identity maps (bbbbbb → its sync id) is NOT translated', () => {
    const identity = identityOfSync(hostsSrc().hosts)
    expect(identity.toWire.get('bbbbbb')).toBe(WIRE) // the identity would map it
    const looks = { [WIRE]: { name: 'a' }, bbbbbb: { name: 'x' }, d1_unknown: { name: 'far' } }
    const input: SettingsBuildInput = { 'purdex-host-looks': { looks }, 'purdex-host-settings': { hosts: { bbbbbb: { a: 1 } } } }
    const p = buildSettingsSection(input, NO_WS, identity)
    expect(p['purdex-host-looks']).toEqual({ looks: { [WIRE]: { name: 'a' }, bbbbbb: { name: 'x' }, d1_unknown: { name: 'far' } } })
    expect(Object.keys((p['purdex-host-looks'] as { looks: object }).looks)).toEqual([WIRE, 'bbbbbb', 'd1_unknown'])
    expect(p['purdex-host-settings']).toEqual({ hosts: { [WIRE]: { a: 1 } } }) // …while host-settings keys ARE translated
    expect(input['purdex-host-looks']).toEqual({ looks }) // the store state is never touched
  })

  it('settings: an EMPTY look store still builds `{ looks: {} }` — always present (plan §0.6)', () => {
    const p = buildSettingsSection({ 'purdex-host-looks': { looks: {} } }, NO_WS, identityOfSync(hostsSrc().hosts))
    expect(p).toEqual({ 'purdex-host-looks': { looks: {} } })
  })

  it('settings: host-settings keys and `sessions:` / `headless:` preset columns are translated; other columns and fields are not', () => {
    const identity = identityOfSync(hostsSrc().hosts)
    const presets = {
      '3col': { enabled: true, columns: [['sessions:bbbbbb', 'x'], ['headless:bbbbbb'], ['sessions:gone00']] },
      '2col': { enabled: true, columns: [['sessions:legacy'], []] },
      '1col': { enabled: false, columns: [['files']] },
    }
    const input: SettingsBuildInput = {
      'purdex-host-settings': { hosts: { bbbbbb: { a: 1 }, legacy: { b: 2 } } },
      'purdex-newtab-layout': { presets },
    }
    const p = buildSettingsSection(input, NO_WS, identity)
    expect(p['purdex-host-settings']).toEqual({ hosts: { [WIRE]: { a: 1 }, legacy: { b: 2 } } })
    expect(p['purdex-newtab-layout']).toEqual({
      presets: {
        '3col': { enabled: true, columns: [[`sessions:${WIRE}`, 'x'], [`headless:${WIRE}`], ['sessions:gone00']] },
        '2col': { enabled: true, columns: [['sessions:legacy'], []] },
        '1col': { enabled: false, columns: [['files']] },
      },
    })
    expect(input['purdex-newtab-layout']).toEqual({ presets }) // the store state is never touched
  })

  it('buildProfileDocument translates hosts, tabs and settings through ONE identity of its hosts', () => {
    const doc = buildProfileDocument({
      hosts: hostsSrc(),
      workspaces: { workspaces: [{ id: 'w1', name: 'W', tabs: ['t1'], activeTabId: 't1' }] },
      tabs: { tabs: { t1: tab('t1', leaf('p', tmux('one', { hostId: 'bbbbbb' }))) }, tabOrder: ['t1'] },
      settings: { 'purdex-host-settings': { hosts: { bbbbbb: {} } } },
    }).document
    expect(JSON.stringify(doc)).not.toMatch(/"(hostId|id)":"bbbbbb"|"bbbbbb":/)
    expect(JSON.stringify(doc['tabs.w1'])).toContain(WIRE)
  })

  it('wireResolverOf: a sync id → its local host; an alias the canonical row carries → that host; anything else unchanged', () => {
    const resolve = wireResolverOf(hostsSrc())!
    expect(resolve(WIRE)).toBe('bbbbbb')
    expect(resolve('aaaaaa')).toBe('bbbbbb')
    expect(resolve('legacy')).toBe('legacy')
    expect(resolve('zzzzzz')).toBe('zzzzzz') // a no-claim row never carries aliases
    expect(resolve(syncIdOfSync('other:daemon'))).toBe(syncIdOfSync('other:daemon'))
  })
})

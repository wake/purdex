import { describe, expect, it } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
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
import type { PaneContent, PaneLayout, PaneRebuildRecord, Tab, Workspace } from '../../types/tab'
import { isWellFormedSection } from './applier'
import { hashSection, structuralKey } from './hash'
import {
  adoptStandaloneTabs,
  buildHostsSection,
  buildProfileDocument,
  buildSettingsSection,
  buildTabsSection,
  buildWorkspacesSection,
  stripSizes,
  type CollectInput,
  type SettingsBuildInput,
} from './sections'
import type { HostsSource, TabsSource, WorkspacesSource } from './types'

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
  buildSettingsSection({ 'purdex-ui-settings': useUISettingsStore.getState() })
  // @ts-expect-error — module on/off is device-local: its store is not a settings source
  buildSettingsSection({ 'purdex-module-enabled': useModuleEnabledStore.getState() })
  // @ts-expect-error — editor preferences are device-local (the store's own header): not a settings source
  buildSettingsSection({ 'purdex-editor-settings': useEditorSettingsStore.getState() })
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
        t2: tab('t2', leaf('p3', { kind: 'dashboard' }), { pinned: true }),
        t3: tab('t3', leaf('p4', tmux('three'))),
      },
      tabOrder: ['t1', 't2', 't3'],
    },
    settings: {
      'purdex-ui-settings': { terminalRenderer: 'webgl', keepAliveCount: 3 },
      'purdex-layout': { tabPosition: 'top' },
      'purdex-newtab-layout': { profiles: { default: { columns: [] } } },
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

  it('an empty list still yields a well-formed payload', () => {
    expect(buildWorkspacesSection([])).toEqual({ order: [], workspaces: {} })
  })

  it('a duplicated workspace id is kept once, first occurrence', () => {
    const out = buildWorkspacesSection([ws('a', 'First', []), ws('a', 'Second', [])])
    expect(out).toEqual({ order: ['a'], workspaces: { a: { name: 'First' } } })
  })
})

// --- buildTabsSection --------------------------------------------------------

describe('buildTabsSection', () => {
  it('order follows ws.tabs; ids with no Tab are dropped, duplicates kept once; record = order', () => {
    const tabs = { t1: tab('t1', leaf('p1', { kind: 'dashboard' })), t2: tab('t2', leaf('p2', { kind: 'hosts' })), other: tab('other', leaf('p9', { kind: 'history' })) }
    const out = buildTabsSection(ws('w', 'W', ['t2', 'ghost', 't1', 't2']), tabs)
    expect(out.order).toEqual(['t2', 't1'])
    expect(Object.keys(out.tabs).sort()).toEqual(['t1', 't2'])
    expect(new Set(out.order).size).toBe(out.order.length)
  })

  it('a workspace with no tabs yields {order: [], tabs: {}}', () => {
    expect(buildTabsSection(ws('w', 'W', []), {})).toEqual({ order: [], tabs: {} })
  })

  it('carries the tab fields and the split structure, and no sizes at any depth', () => {
    const layout = split('s1', [leaf('p1', { kind: 'dashboard' }), split('s2', [leaf('p2', { kind: 'hosts' }), leaf('p3', { kind: 'history' })], [20, 80], 'v')], [40, 60])
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
    })
    expect(out).toEqual({ 'purdex-ui-settings': { terminalRenderer: 'dom' }, 'purdex-layout': { tabPosition: 'left' } })
  })

  it('skips an absent store, and a store none of whose listed fields is present (no empty object)', () => {
    const out = buildSettingsSection({ 'purdex-themes': { activeThemeId: 'dark' }, 'purdex-layout': { regions: {} } })
    expect(out).toEqual({ 'purdex-themes': { activeThemeId: 'dark' } })
    expect(buildSettingsSection({})).toEqual({})
  })

  it('ignores a storage key that is not one of the eight', () => {
    const out = buildSettingsSection({ 'purdex-tabs': { tabs: S } } as unknown as SettingsBuildInput)
    expect(out).toEqual({})
  })

  it('module on/off is device-local: useModuleEnabledStore contributes nothing, `enabled` included', () => {
    const out = buildSettingsSection({
      'purdex-ui-settings': { keepAliveCount: 3 },
      'purdex-module-enabled': { enabled: { files: false }, baseline: { files: true } },
    } as unknown as SettingsBuildInput)
    expect(out).toEqual({ 'purdex-ui-settings': { keepAliveCount: 3 } })
  })

  it('editor preferences are device-local: useEditorSettingsStore contributes nothing', () => {
    const out = buildSettingsSection({
      'purdex-ui-settings': { keepAliveCount: 3 },
      'purdex-editor-settings': { fontSize: 11, tabSize: 2, wordWrap: 'on' },
    } as unknown as SettingsBuildInput)
    expect(out).toEqual({ 'purdex-ui-settings': { keepAliveCount: 3 } })
  })
})

// --- buildProfileDocument ----------------------------------------------------

describe('buildProfileDocument', () => {
  it('has hosts, settings, workspaces and one tabs.<id> per workspace — empty ones included', () => {
    const { document, standaloneTabIds } = buildProfileDocument(baseInput())
    expect(Object.keys(document).sort()).toEqual(['hosts', 'settings', 'tabs.wsA', 'tabs.wsB', 'tabs.wsEmpty', 'workspaces'])
    expect(document['tabs.wsEmpty']).toEqual({ order: [], tabs: {} })
    expect(document['tabs.wsA']).toMatchObject({ order: ['t1', 't2'] })
    expect(standaloneTabIds).toEqual([])
  })

  it('a tab in no workspace enters no section and is reported, in tabOrder order', () => {
    const input = baseInput()
    input.tabs = {
      tabs: { ...input.tabs.tabs, loose2: tab('loose2', leaf('px', { kind: 'hosts' })), loose1: tab('loose1', leaf('py', { kind: 'history' })) },
      tabOrder: ['loose1', 't1', 'loose2', 't2', 't3'],
    }
    const { document, standaloneTabIds } = buildProfileDocument(input)
    expect(standaloneTabIds).toEqual(['loose1', 'loose2'])
    expect(structuralKey(document)).not.toContain('loose')
  })

  it('a standalone tab missing from tabOrder is still reported, after the ordered ones', () => {
    const input = baseInput()
    input.tabs = {
      tabs: { ...input.tabs.tabs, hidden: tab('hidden', leaf('px', { kind: 'hosts' })), loose: tab('loose', leaf('py', { kind: 'history' })) },
      tabOrder: ['t1', 't2', 't3', 'loose'],
    }
    expect(buildProfileDocument(input).standaloneTabIds).toEqual(['loose', 'hidden'])
  })

  it('throws on a workspace id the daemon would reject as a section key', () => {
    const input = baseInput()
    input.workspaces = { workspaces: [ws('bad id!', 'Bad', [])] }
    expect(() => buildProfileDocument(input)).toThrow(/cannot form a section key/)
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
          t2: tab('t2', leaf('p4', { kind: 'dashboard' })),
          // standalone — its content must not travel either
          [S]: tab(S, leaf('p5', { kind: 'browser', url: S })),
        },
        tabOrder: [S, 't1', 't2'],
        activeTabId: S,
        visitHistory: [S, S],
      },
      settings: {
        'purdex-ui-settings': { terminalRenderer: 'webgl', terminalSettingsVersion: N },
        'purdex-newtab-layout': { profiles: {}, activeEditingProfile: S, knownIds: [S] },
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

    const { document, standaloneTabIds } = buildProfileDocument(input)
    const key = structuralKey(document)
    expect(key).not.toContain(S)
    expect(key).not.toContain(String(N))
    expect(hasKeyDeep(document, 'sizes')).toBe(false)
    expect(hasKeyDeep(document, 'purdex-module-enabled')).toBe(false)
    expect(hasKeyDeep(document, 'enabled')).toBe(false)
    expect(hasKeyDeep(document, 'purdex-editor-settings')).toBe(false)
    expect(hasKeyDeep(document, 'fontSize')).toBe(false)
    expect(standaloneTabIds).toEqual([S])
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

  it('after adoption the document has no standalone tab left', () => {
    const input = baseInput()
    input.tabs = { tabs: { ...input.tabs.tabs, loose: tab('loose', leaf('px', { kind: 'hosts' })) }, tabOrder: ['loose', 't1', 't2', 't3'] }
    const adopted = adoptStandaloneTabs({ workspaces: input.workspaces.workspaces, ...input.tabs }, opts)
    const { document, standaloneTabIds } = buildProfileDocument({ ...input, workspaces: { workspaces: adopted.workspaces } })
    expect(standaloneTabIds).toEqual([])
    expect(document['tabs.ws-new']).toMatchObject({ order: ['loose'] })
  })

  it('never mutates its input', () => {
    const world = deepFreeze({ workspaces: [ws('u', 'Unsorted', ['t1'])], tabs: tabsOf('t1', 'x'), tabOrder: ['t1', 'x'] })
    const out = adoptStandaloneTabs(world, opts)
    expect(out.workspaces[0].tabs).toEqual(['t1', 'x'])
    expect(world.workspaces[0].tabs).toEqual(['t1'])
  })
})

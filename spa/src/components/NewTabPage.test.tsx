import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { NewTabPage } from './NewTabPage'
import {
  registerNewTabProvider,
  registerNewTabProviderSource,
  clearNewTabRegistry,
  getNewTabProviders,
  type NewTabProviderProps,
} from '../lib/new-tab-registry'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { useI18nStore } from '../stores/useI18nStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useSessionStore } from '../stores/useSessionStore'
import type { Session } from '../lib/host-api'
import { createTab, type PaneContent, type Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import * as paneMove from '../lib/pane-move'
import { createHeadlessProviderSource } from '../lib/headless-new-tab-providers'
import { useHostStore } from '../stores/useHostStore'
import { useNexHostStore, type NexHostEntry } from '../stores/useNexHostStore'

// Keep MOVABLE_KINDS (imported by NewTabPage) real; only spy the mover.
vi.mock('../lib/pane-move', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/pane-move')>()
  return { ...actual, moveTabContentIntoPane: vi.fn(() => true) }
})

// Commit 2 — A2-4 / A2-5: NewTabPage module-aware filter (spec §4.9.3).
//
// Registering providers directly (not via registerBuiltinModules) so each
// test case has a predictable set.  `useNewTabLayoutStore` is preloaded with
// both columns containing all known provider ids so they actually render;
// `persist.hasHydrated()` is forced true to bypass the loading placeholder.

const FakeEditorCard: React.FC<NewTabProviderProps> = () => <div data-testid="card-editor">editor</div>
const FakeEditorBuffersCard: React.FC<NewTabProviderProps> = () => <div data-testid="card-editor-buffers">buffers</div>
const FakeSessionsCard: React.FC<NewTabProviderProps> = () => <div data-testid="card-sessions">sessions</div>

function primeLayout(ids: string[]) {
  // Force 1col active and place every provider into the single column so the
  // rendering path doesn't depend on `ensureDefaults` timing.
  useNewTabLayoutStore.setState({
    profiles: {
      '3col': { enabled: false, columns: [[], [], []] },
      '2col': { enabled: false, columns: [[], []] },
      '1col': { enabled: true, columns: [[...ids]] },
    },
    knownIds: ids,
    activeEditingProfile: '1col',
  })
}

function forceHydrated() {
  // NewTabPage gates its content behind `persist.hasHydrated()`. In test
  // environments rehydration may not have run yet — trigger it directly so
  // the grid mounts synchronously.
  const api = useNewTabLayoutStore.persist as unknown as {
    hasHydrated: () => boolean
    rehydrate?: () => Promise<void>
  }
  if (!api.hasHydrated()) {
    api.rehydrate?.()
  }
}

beforeEach(() => {
  clearNewTabRegistry()
  clearModuleRegistry()
  // Declare the editor module so `isEnabled('editor')` can distinguish
  // "disableable + user off" from "not found → default true".
  registerModule({ id: 'editor', name: 'Editor', disableable: true })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  useI18nStore.setState({ t: (k: string) => k })
  forceHydrated()
})

afterEach(() => {
  clearNewTabRegistry()
  clearModuleRegistry()
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
})

describe('NewTabPage — module-aware provider filter', () => {
  it('A2-4: hides providers whose moduleId is disabled (editor + editor-buffers)', () => {
    registerNewTabProvider({
      id: 'editor',
      label: 'editor.provider_label',
      icon: 'File',
      order: 5,
      component: FakeEditorCard,
      moduleId: 'editor',
    })
    registerNewTabProvider({
      id: 'editor-buffers',
      label: 'newTab.editor.buffers.label',
      icon: 'Stack',
      order: 6,
      component: FakeEditorBuffersCard,
      moduleId: 'editor',
    })
    registerNewTabProvider({
      id: 'sessions',
      label: 'session.provider_label',
      icon: 'List',
      order: 0,
      component: FakeSessionsCard,
      // no moduleId — legacy provider
    })
    primeLayout(['sessions', 'editor', 'editor-buffers'])

    // Disable the editor module.
    useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })

    render(<NewTabPage onSelect={() => {}} />)

    expect(screen.queryByTestId('card-editor')).toBeNull()
    expect(screen.queryByTestId('card-editor-buffers')).toBeNull()
    // Sessions still visible — it has no moduleId.
    expect(screen.getByTestId('card-sessions')).toBeTruthy()
  })

  it('A2-5: providers without moduleId remain visible even when any module is disabled', () => {
    registerNewTabProvider({
      id: 'sessions',
      label: 'session.provider_label',
      icon: 'List',
      order: 0,
      component: FakeSessionsCard,
      // no moduleId
    })
    primeLayout(['sessions'])

    // Disable editor module — must not affect a no-moduleId provider.
    useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })

    render(<NewTabPage onSelect={() => {}} />)

    expect(screen.getByTestId('card-sessions')).toBeTruthy()
  })

  it('reload-required: post-mount setEnabled(editor, false) does not hide editor cards until re-render via bootstrap', async () => {
    const { act } = await import('react')
    registerNewTabProvider({
      id: 'editor',
      label: 'editor.provider_label',
      icon: 'File',
      order: 5,
      component: FakeEditorCard,
      moduleId: 'editor',
    })
    registerNewTabProvider({
      id: 'sessions',
      label: 'session.provider_label',
      icon: 'List',
      order: 0,
      component: FakeSessionsCard,
    })
    primeLayout(['sessions', 'editor'])

    render(<NewTabPage onSelect={() => {}} />)
    expect(screen.getByTestId('card-editor')).toBeTruthy()

    await act(async () => {
      useModuleEnabledStore.getState().setEnabled('editor', false)
    })

    // Editor card is still rendered — providers are snapshotted at render
    // creation time and only refresh when registerBuiltinModules() runs again
    // (matches PaneLayoutRenderer + file-opener registry contract).
    expect(screen.getByTestId('card-editor')).toBeTruthy()
  })

  it('A2-7: renders the empty state when every column pins only filtered-out providers (v1.4 F8)', () => {
    // A module-aware provider AND a legacy provider exist; the
    // pinned profile, however, only references the module-aware
    // cards. When the Editor module is disabled every column entry
    // resolves to null → old code rendered an empty grid silently.
    // v1.4 falls back to the shared empty state instead. The legacy
    // `sessions` provider stays registered (so `providers.length`
    // is non-zero) to exercise the NEW `hasAnyVisibleEntry` gate
    // rather than the pre-existing zero-providers shortcut.
    registerNewTabProvider({
      id: 'editor',
      label: 'editor.provider_label',
      icon: 'File',
      order: 5,
      component: FakeEditorCard,
      moduleId: 'editor',
    })
    registerNewTabProvider({
      id: 'editor-buffers',
      label: 'newTab.editor.buffers.label',
      icon: 'Stack',
      order: 6,
      component: FakeEditorBuffersCard,
      moduleId: 'editor',
    })
    registerNewTabProvider({
      id: 'sessions',
      label: 'session.provider_label',
      icon: 'List',
      order: 0,
      component: FakeSessionsCard,
      // no moduleId — always visible
    })
    // Profile pins ONLY the editor-module providers; `sessions` is
    // visible but not pinned, so every column entry filters out.
    primeLayout(['editor', 'editor-buffers'])
    useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })

    render(<NewTabPage onSelect={() => {}} />)

    // Empty-state fallback fires via the new `hasAnyVisibleEntry`
    // gate; no cards render.
    expect(screen.getByTestId('newtab-empty-state')).toBeTruthy()
    expect(screen.queryByTestId('card-editor')).toBeNull()
    expect(screen.queryByTestId('card-editor-buffers')).toBeNull()
    expect(screen.queryByTestId('card-sessions')).toBeNull()
  })

  it('grid root fills its mount with h-full (not flex-1) so the scrollable column is bounded', () => {
    // The new-tab pane mounts under TabContent's position:absolute wrapper (a
    // plain block, not a flex container), so a `flex-1` root collapses to
    // content height and the inner `overflow-y-auto` column never gets a
    // bounded height to scroll within. The root must claim height via `h-full`
    // (resolves against the block parent's definite inset:0 height), matching
    // EditorPane. Guards against regressing the start-screen scroll.
    registerNewTabProvider({
      id: 'sessions',
      label: 'session.provider_label',
      icon: 'List',
      order: 0,
      component: FakeSessionsCard,
    })
    primeLayout(['sessions'])

    const { container } = render(<NewTabPage onSelect={() => {}} />)
    const root = container.firstChild as HTMLElement
    expect(root.className).toContain('h-full')
    expect(root.className).not.toContain('flex-1')
    // The scroll column is still present underneath.
    expect((root.firstChild as HTMLElement).className).toContain('overflow-y-auto')
  })
})

// PR-B Task B2 — "Bring in an open tab" cross-workspace section.
//
// NewTabPage, when mounted inside a real `new-tab` pane, receives that pane's
// tab + pane ids. It then surfaces every OTHER open tab (across all
// workspaces) whose content can be relocated (single-pane + MOVABLE_KINDS +
// not locked), so the user can pull it into this split pane. Clicking a row
// calls `moveTabContentIntoPane(sourceTabId, currentTabId, currentPaneId)`.

const BRING_IN_TITLE = 'page.newtab.bringInTab' // i18n key (t = identity in tests)

function seedProvidersForGrid() {
  // The section only renders on the happy grid path, so register at least one
  // provider + prime a single-column layout like the tests above.
  registerNewTabProvider({
    id: 'sessions',
    label: 'session.provider_label',
    icon: 'List',
    order: 0,
    component: FakeSessionsCard,
  })
  primeLayout(['sessions'])
}

function primaryPaneId(tab: Tab): string {
  return getPrimaryPane(tab.layout).id
}

/** Create a tab in a fresh workspace named `wsName`; returns tab + ws id. */
function seedWorkspaceTab(wsName: string, content: PaneContent): { tab: Tab; wsId: string } {
  const ws = useWorkspaceStore.getState().addWorkspace(wsName)
  const tab = createTab(content)
  useTabStore.getState().addTab(tab)
  useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
  return { tab, wsId: ws.id }
}

/**
 * Seed the "current" host tab as a real SPLIT — a `new-tab` pane split once so
 * the owning tab has >1 pane. This is the only state in which the Bring-in
 * section is meant to appear (a full-page new tab is single-pane and must NOT
 * show it). Returns the tab + the surviving new-tab pane id (`splitPaneBlank`
 * preserves the original pane id) to pass as `currentPaneId`.
 */
function seedSplitCurrentTab(wsName: string): { tab: Tab; paneId: string } {
  const { tab } = seedWorkspaceTab(wsName, { kind: 'new-tab' })
  const paneId = primaryPaneId(tab)
  useTabStore.getState().splitPaneBlank(tab.id, paneId, 'h')
  return { tab, paneId }
}

const editorContent = (filePath: string): PaneContent => ({
  kind: 'editor',
  source: { type: 'daemon', hostId: 'h1' },
  filePath,
})

describe('NewTabPage — bring in an open tab (PR-B B2)', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.getState().reset()
    useSessionStore.setState({ sessions: {} })
    vi.mocked(paneMove.moveTabContentIntoPane).mockClear()
    seedProvidersForGrid()
  })

  afterEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.getState().reset()
    useSessionStore.setState({ sessions: {} })
  })

  it('labels tmux sessions by their own host when two hosts share a session code', () => {
    // Two different hosts each have a live session with the SAME code but a
    // DIFFERENT name. A flat code→session map (across all hosts) collapses them
    // and mislabels one tab with the other host's name; the lookup must be
    // scoped by each candidate's hostId.
    const mkSession = (code: string, name: string): Session => ({
      code,
      name,
      cwd: '/',
      mode: 'terminal',
    })
    useSessionStore.setState({
      sessions: {
        hostA: [mkSession('dup', 'server-alpha')],
        hostB: [mkSession('dup', 'server-beta')],
      },
    })

    const { tab: current, paneId } = seedSplitCurrentTab('WS')
    seedWorkspaceTab('WS', {
      kind: 'tmux-session',
      hostId: 'hostA',
      sessionCode: 'dup',
      mode: 'terminal',
      cachedName: 'cached-A',
      tmuxInstance: 'default',
    })
    seedWorkspaceTab('WS', {
      kind: 'tmux-session',
      hostId: 'hostB',
      sessionCode: 'dup',
      mode: 'terminal',
      cachedName: 'cached-B',
      tmuxInstance: 'default',
    })

    render(
      <NewTabPage
        onSelect={() => {}}
        currentTabId={current.id}
        currentPaneId={paneId}
      />,
    )

    // Each candidate resolves against its OWN host's session list.
    expect(screen.getByText('server-alpha')).toBeTruthy()
    expect(screen.getByText('server-beta')).toBeTruthy()
  })

  it('lists movable tabs across workspaces with their tab name + workspace name', () => {
    const { tab: current, paneId } = seedSplitCurrentTab('Alpha')
    seedWorkspaceTab('Alpha', editorContent('/proj/readme.md'))
    seedWorkspaceTab('Beta', {
      kind: 'tmux-session',
      hostId: 'h1',
      sessionCode: 's1',
      mode: 'terminal',
      cachedName: 'build-server',
      tmuxInstance: 'default',
    })

    render(
      <NewTabPage
        onSelect={() => {}}
        currentTabId={current.id}
        currentPaneId={paneId}
      />,
    )

    expect(screen.getByText(BRING_IN_TITLE)).toBeTruthy()
    // Editor tab: basename label + its workspace name.
    expect(screen.getByText('readme.md')).toBeTruthy()
    expect(screen.getAllByText('Alpha').length).toBeGreaterThan(0)
    // tmux tab: cachedName label + its workspace name.
    expect(screen.getByText('build-server')).toBeTruthy()
    expect(screen.getByText('Beta')).toBeTruthy()
  })

  it('excludes browser tabs, multi-pane tabs, locked tabs, and the current tab', () => {
    const { tab: current, paneId } = seedSplitCurrentTab('Alpha')

    // browser — not a MOVABLE_KIND.
    seedWorkspaceTab('Alpha', { kind: 'browser', url: 'https://example.com' })
    // multi-pane editor tab.
    const { tab: multi } = seedWorkspaceTab('Alpha', editorContent('/multi.ts'))
    useTabStore.getState().splitPaneBlank(multi.id, primaryPaneId(multi), 'h')
    // locked editor tab.
    const { tab: locked } = seedWorkspaceTab('Alpha', editorContent('/locked.ts'))
    useTabStore.getState().toggleLock(locked.id)
    // a self-referential movable tab (same id as current) is impossible, but
    // seed another movable so the section renders and we can assert exclusions.
    seedWorkspaceTab('Beta', editorContent('/keep.ts'))

    render(
      <NewTabPage
        onSelect={() => {}}
        currentTabId={current.id}
        currentPaneId={paneId}
      />,
    )

    expect(screen.getByText('keep.ts')).toBeTruthy()
    expect(screen.queryByText('example.com')).toBeNull()
    expect(screen.queryByText('multi.ts')).toBeNull()
    expect(screen.queryByText('locked.ts')).toBeNull()
  })

  it('does not render the section on a full-page new tab (single pane, not a split)', () => {
    // The core gate: a standalone new tab is a single-pane tab, NOT a pane-split
    // target. Even with plenty of movable tabs available, the section must stay
    // hidden — it only belongs when this new-tab pane is one cell of a split.
    const { tab: current } = seedWorkspaceTab('Alpha', { kind: 'new-tab' })
    seedWorkspaceTab('Alpha', editorContent('/movable-a.ts'))
    seedWorkspaceTab('Beta', editorContent('/movable-b.ts'))

    render(
      <NewTabPage
        onSelect={() => {}}
        currentTabId={current.id}
        currentPaneId={primaryPaneId(current)}
      />,
    )

    expect(screen.queryByText(BRING_IN_TITLE)).toBeNull()
    expect(screen.queryByText('movable-a.ts')).toBeNull()
    expect(screen.queryByText('movable-b.ts')).toBeNull()
  })

  it('does not list the current tab among the candidates', () => {
    const { tab: current, paneId } = seedSplitCurrentTab('Alpha')
    seedWorkspaceTab('Alpha', editorContent('/other.ts'))

    render(
      <NewTabPage
        onSelect={() => {}}
        currentTabId={current.id}
        currentPaneId={paneId}
      />,
    )

    expect(screen.getByText('other.ts')).toBeTruthy()
    // The current (split) tab is never listed as a candidate for itself.
    expect(screen.queryByText(BRING_IN_TITLE)).toBeTruthy()
  })

  it('clicking a row calls moveTabContentIntoPane(sourceId, currentTabId, currentPaneId)', () => {
    const { tab: current, paneId: currentPane } = seedSplitCurrentTab('Alpha')
    const { tab: source } = seedWorkspaceTab('Beta', editorContent('/pull-me.ts'))

    render(
      <NewTabPage
        onSelect={() => {}}
        currentTabId={current.id}
        currentPaneId={currentPane}
      />,
    )

    fireEvent.click(screen.getByText('pull-me.ts'))

    expect(paneMove.moveTabContentIntoPane).toHaveBeenCalledTimes(1)
    expect(paneMove.moveTabContentIntoPane).toHaveBeenCalledWith(source.id, current.id, currentPane)
  })

  it('does not render the section when currentTabId / currentPaneId are absent', () => {
    seedWorkspaceTab('Alpha', editorContent('/x.ts'))

    render(<NewTabPage onSelect={() => {}} />)

    expect(screen.queryByText(BRING_IN_TITLE)).toBeNull()
  })

  it('does not render the section when there are no movable tabs to bring in', () => {
    const { tab: current, paneId } = seedSplitCurrentTab('Alpha')
    // Only non-movable / current tabs exist.
    seedWorkspaceTab('Alpha', { kind: 'browser', url: 'https://only.example.com' })

    render(
      <NewTabPage
        onSelect={() => {}}
        currentTabId={current.id}
        currentPaneId={paneId}
      />,
    )

    expect(screen.queryByText(BRING_IN_TITLE)).toBeNull()
  })

  it('caps the section at half the pane height and scrolls the list internally', () => {
    // Regression: with many open tabs the section grew unbounded and pushed the
    // provider grid (sessions / files / …) out of view in a split pane.
    const { tab: current, paneId } = seedSplitCurrentTab('Alpha')
    for (let i = 0; i < 20; i++) seedWorkspaceTab('Alpha', editorContent(`/proj/f${i}.md`))

    render(
      <NewTabPage
        onSelect={() => {}}
        currentTabId={current.id}
        currentPaneId={paneId}
      />,
    )

    const section = screen.getByTestId('newtab-bring-in')
    expect(section.className).toContain('max-h-[50%]')
    expect(section.className).toContain('min-h-0')
    const list = screen.getByTestId('newtab-bring-in-list')
    expect(list.className).toContain('overflow-y-auto')
    expect(list.className).toContain('min-h-0')
  })
})

describe('NewTabPage — module visibility follows the mount-time snapshot', () => {
  function editorSource() {
    let ids: string[] = []
    const listeners = new Set<() => void>()
    registerNewTabProviderSource({
      id: 'dyn-editor',
      getProviders: () => ids.map((id) => ({ id, label: `label-${id}`, icon: 'File', order: 0, component: FakeEditorCard, moduleId: 'editor' })),
      subscribe: (l) => { listeners.add(l); return () => { listeners.delete(l) } },
      ownsId: (id) => id.startsWith('ed:'),
    })
    return (next: string[]) => act(() => { ids = next; listeners.forEach((l) => l()) })
  }

  it('shows a late provider whose module was enabled at mount, even if disabled since', () => {
    registerNewTabProvider({ id: 'sessions', label: 'sessions', icon: 'List', order: 0, component: FakeSessionsCard })
    const emit = editorSource()
    primeLayout(['sessions', 'ed:a'])
    render(<NewTabPage onSelect={() => {}} />)
    act(() => { useModuleEnabledStore.setState({ enabled: { editor: false } }) })
    emit(['ed:a'])
    expect(screen.getByTestId('card-editor')).toBeTruthy()
  })

  it('hides a late provider whose module was disabled at mount, even if enabled since', () => {
    registerNewTabProvider({ id: 'sessions', label: 'sessions', icon: 'List', order: 0, component: FakeSessionsCard })
    useModuleEnabledStore.setState({ enabled: { editor: false } })
    const emit = editorSource()
    primeLayout(['sessions', 'ed:a'])
    render(<NewTabPage onSelect={() => {}} />)
    act(() => { useModuleEnabledStore.setState({ enabled: {} }) })
    emit(['ed:a'])
    expect(screen.queryByTestId('card-editor')).toBeNull()
  })
})

describe('NewTabPage — dynamic providers', () => {
  it('interpolates labelParams into the section heading', () => {
    useI18nStore.setState({ t: (k: string, p?: Record<string, string | number>) => (p ? `${k}:${p.host}` : k) })
    registerNewTabProvider({ id: 'sessions:h1', label: 'session.provider_label_host', labelParams: { host: 'mlab' }, icon: 'List', order: 0, component: FakeSessionsCard })
    primeLayout(['sessions:h1'])
    render(<NewTabPage onSelect={() => {}} />)
    expect(screen.getByText('session.provider_label_host:mlab')).toBeTruthy()
  })

  it('picks up a provider added by a source while mounted', () => {
    let ids = ['a']
    const listeners = new Set<() => void>()
    registerNewTabProviderSource({
      id: 'dyn',
      getProviders: () => ids.map((id) => ({ id: `dyn:${id}`, label: `dyn-${id}`, icon: 'List', order: 0, component: FakeSessionsCard })),
      subscribe: (l) => { listeners.add(l); return () => { listeners.delete(l) } },
      ownsId: (id) => id.startsWith('dyn:'),
    })
    primeLayout(['dyn:a', 'dyn:b'])
    render(<NewTabPage onSelect={() => {}} />)
    expect(screen.queryByText('dyn-b')).toBeNull()
    act(() => { ids = ['a', 'b']; listeners.forEach((l) => l()) })
    expect(screen.getByText('dyn-b')).toBeTruthy()
  })
})

// P-C.1 task 5 — the Headless source adds one `headless:<hostId>` block per
// host and nothing else: the sessions / editor / browser entries keep their
// ids and relative order (spec §4.2 ordering guard).
describe('NewTabPage — Headless section per host (P-C.1)', () => {
  const FakeEditor: React.FC<NewTabProviderProps> = () => <div data-testid="card-editor">editor</div>
  const FakeBrowser: React.FC<NewTabProviderProps> = () => <div data-testid="card-browser">browser</div>
  const FakeSessions = (hostId: string): React.FC<NewTabProviderProps> => () => (
    <div data-testid={`card-sessions-${hostId}`}>sessions</div>
  )
  const disabledEntry = (): NexHostEntry => ({
    info: { configured: false, mounted: false, ready: false, init_error: '', effective: null } as never,
    capabilities: null,
    phase: 'disabled',
    error: null,
    fetchedAt: 1,
    generation: 1,
    fingerprint: '',
  })

  beforeEach(() => {
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'mlab', ip: '1', port: 7860, order: 0 },
        h2: { id: 'h2', name: 'air', ip: '2', port: 7860, order: 1 },
      },
      hostOrder: ['h1', 'h2'],
      activeHostId: 'h1',
    })
    // `disabled` renders the one-line hint and performs no fetch; `ensure` is
    // stubbed so mounting never reaches the network either.
    useNexHostStore.setState({
      byHost: { h1: disabledEntry(), h2: disabledEntry() },
      ensure: vi.fn(async () => {}),
    })
    useI18nStore.setState({ t: (k: string, p?: Record<string, string | number>) => (p?.host ? `${k}:${p.host}` : k) })
  })

  afterEach(() => {
    useNexHostStore.setState({ byHost: {} })
  })

  function registerLikeBootstrap() {
    // Same shape and order as register-modules/index.tsx: browser (static,
    // order -10), sessions source (order 0), headless source (order 5),
    // editor (static, order 5).
    registerNewTabProvider({ id: 'browser', label: 'browser.provider_label', icon: 'Globe', order: -10, component: FakeBrowser })
    registerNewTabProviderSource({
      id: 'sessions',
      getProviders: () => useHostStore.getState().hostOrder.map((h) => ({
        id: `sessions:${h}`, label: 'session.provider_label_host', labelParams: { host: h }, icon: 'List', order: 0, component: FakeSessions(h),
      })),
      subscribe: () => () => {},
      ownsId: (id) => id.startsWith('sessions:'),
    })
    registerNewTabProviderSource(createHeadlessProviderSource())
    registerNewTabProvider({ id: 'editor', label: 'editor.provider_label', icon: 'File', order: 5, component: FakeEditor, moduleId: 'editor' })
  }

  it('adds only headless:<hostId> ids, after every pre-existing provider of lower or equal order', () => {
    registerLikeBootstrap()
    expect(getNewTabProviders().map((p) => p.id)).toEqual([
      'browser', 'sessions:h1', 'sessions:h2', 'editor', 'headless:h1', 'headless:h2',
    ])
  })

  it('renders one Headless section per host while the sessions / editor / browser sections keep their order', () => {
    registerLikeBootstrap()
    const ids = ['browser', 'sessions:h1', 'sessions:h2', 'editor', 'headless:h1', 'headless:h2']
    primeLayout(ids)

    const { container } = render(<NewTabPage onSelect={() => {}} />)

    const headings = [...container.querySelectorAll('section > h3')].map((h) => h.textContent)
    expect(headings).toEqual([
      'browser.provider_label',
      'session.provider_label_host:h1',
      'session.provider_label_host:h2',
      'editor.provider_label',
      'newtab.headless.title:mlab',
      'newtab.headless.title:air',
    ])
    expect(screen.getByTestId('card-browser')).toBeTruthy()
    expect(screen.getByTestId('card-sessions-h1')).toBeTruthy()
    expect(screen.getByTestId('card-sessions-h2')).toBeTruthy()
    expect(screen.getByTestId('card-editor')).toBeTruthy()
    // Two Headless blocks, each on its `disabled` line; ensure() ran per host, no fetch.
    expect(screen.getAllByTestId('headless-disabled')).toHaveLength(2)
    const ensure = vi.mocked(useNexHostStore.getState().ensure)
    expect(ensure.mock.calls.map((c) => c[0]).sort()).toEqual(['h1', 'h2'])
  })
})

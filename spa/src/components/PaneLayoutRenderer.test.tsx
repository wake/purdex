import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, createEvent, type RenderResult } from '@testing-library/react'
import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { countLeaves } from '../lib/pane-tree'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useAgentStore } from '../stores/useAgentStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useUndoToast } from '../stores/useUndoToast'
import { compositeKey } from '../lib/composite-key'
import { handToNex } from '../lib/nex/handoff'
import type { PaneLayout, Tab, TmuxSessionContent } from '../types/tab'

vi.mock('../lib/nex/handoff', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/handoff')>()),
  handToNex: vi.fn(),
}))
const mockedHandToNex = vi.mocked(handToNex)

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
})

describe('PaneLayoutRenderer', () => {
  it('renders the correct component for a registered kind', () => {
    registerModule({
      id: 'dashboard',
      name: 'Dashboard',
      panes: [{
        kind: 'dashboard',
        component: ({ pane }) => <div data-testid="dashboard">Dashboard:{pane.id}</div>,
      }],
    })
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p1', content: { kind: 'dashboard' } },
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByTestId('dashboard')).toBeTruthy()
    expect(screen.getByTestId('dashboard').textContent).toBe('Dashboard:p1')
  })

  it('shows fallback for an unregistered kind', () => {
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p1', content: { kind: 'settings', scope: 'global' } },
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={false} />)
    expect(screen.getByText(/No renderer for/)).toBeTruthy()
    expect(screen.getByText(/settings/)).toBeTruthy()
  })

  it('passes isActive prop to the rendered component', () => {
    registerModule({
      id: 'history',
      name: 'History',
      panes: [{
        kind: 'history',
        component: ({ isActive }) => (
          <div data-testid="history">{isActive ? 'active' : 'inactive'}</div>
        ),
      }],
    })
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p2', content: { kind: 'history' } },
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={false} />)
    expect(screen.getByTestId('history').textContent).toBe('inactive')
  })

  it('shows fallback for empty split children', () => {
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'h',
      children: [],
      sizes: [],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByText(/Empty split layout/)).toBeTruthy()
  })

  it('renders all children of a split layout', () => {
    registerModule({
      id: 'dashboard-multi',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'h',
      children: [
        { type: 'leaf', pane: { id: 'left', content: { kind: 'dashboard' } } },
        { type: 'leaf', pane: { id: 'right', content: { kind: 'dashboard' } } },
      ],
      sizes: [50, 50],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByTestId('dash-left')).toBeTruthy()
    expect(screen.getByTestId('dash-right')).toBeTruthy()
  })

  it('split container claims height via h-full (not flex-1) so panes stay bounded under a block parent', () => {
    // A top-level split mounts under TabContent's `.absolute inset-0` BLOCK
    // wrapper, where flex-1 is inert — the container would collapse to content
    // height and an overflow-y-auto region inside any pane could never scroll
    // (verified by layout measurement in split-pane-scroll headless repro).
    // h-full resolves against the absolute wrapper's definite height. Guard the
    // class so a refactor can't silently regress split-pane scrolling.
    registerModule({
      id: 'dashboard-split-root',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div>{pane.id}</div> }],
    })
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'v',
      children: [
        { type: 'leaf', pane: { id: 'top', content: { kind: 'dashboard' } } },
        { type: 'leaf', pane: { id: 'bot', content: { kind: 'dashboard' } } },
      ],
      sizes: [50, 50],
    }
    const { container } = render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    const root = container.firstChild as HTMLElement
    expect(root.className).toContain('h-full')
    expect(root.className).toContain('w-full')
    expect(root.className).not.toContain('flex-1')
  })

  it('renders nested splits', () => {
    registerModule({
      id: 'dashboard-nested',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'v',
      children: [
        { type: 'leaf', pane: { id: 'top', content: { kind: 'dashboard' } } },
        { type: 'split', id: 's2', direction: 'h',
          children: [
            { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
            { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
          ],
          sizes: [50, 50] },
      ],
      sizes: [50, 50],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByTestId('dash-top')).toBeTruthy()
    expect(screen.getByTestId('dash-bl')).toBeTruthy()
    expect(screen.getByTestId('dash-br')).toBeTruthy()
  })

  describe('disabled module fallback', () => {
    it('renders the actual component when the disableable module is enabled', () => {
      registerModule({
        id: 'feat-mod',
        name: 'Feat',
        disableable: true,
        panes: [{ kind: 'feat-pane', component: ({ pane }) => <div data-testid="real">real:{pane.id}</div> }],
      })
      const layout: PaneLayout = {
        type: 'leaf',
        pane: { id: 'p1', content: { kind: 'feat-pane' } as never },
      }
      render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
      expect(screen.getByTestId('real')).toBeTruthy()
    })

    it('renders DisabledModulePlaceholder when the module is disabled', () => {
      registerModule({
        id: 'feat-mod',
        name: 'Feat',
        disableable: true,
        panes: [{ kind: 'feat-pane', component: ({ pane }) => <div data-testid="real">real:{pane.id}</div> }],
      })
      useModuleEnabledStore.getState().setEnabled('feat-mod', false)
      const layout: PaneLayout = {
        type: 'leaf',
        pane: { id: 'p1', content: { kind: 'feat-pane' } as never },
      }
      render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
      expect(screen.queryByTestId('real')).toBeNull()
      expect(screen.getByRole('button', { name: /feat-mod/i })).toBeInTheDocument()
    })

    it('does not flip when the parent re-renders the leaf after a toggle (snapshot at mount)', async () => {
      const { act } = await import('react')
      registerModule({
        id: 'feat-mod',
        name: 'Feat',
        disableable: true,
        panes: [{ kind: 'feat-pane', component: ({ pane }) => <div data-testid="real">real:{pane.id}</div> }],
      })
      const layout: PaneLayout = {
        type: 'leaf',
        pane: { id: 'p1', content: { kind: 'feat-pane' } as never },
      }

      const rendered: RenderResult = render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
      expect(screen.getByTestId('real')).toBeInTheDocument()

      // User toggles editor off in the Switchboard.
      await act(async () => {
        useModuleEnabledStore.getState().setEnabled('feat-mod', false)
      })

      // Parent re-renders the leaf for an unrelated reason (active-tab focus,
      // showHeader flip, layout-tree swap). The leaf must keep rendering the
      // real component because the enable map was snapshotted at mount.
      rendered.rerender(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={false} />)
      expect(screen.getByTestId('real')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /feat-mod/i })).toBeNull()

      rendered.rerender(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} showHeader />)
      expect(screen.getByTestId('real')).toBeInTheDocument()
    })

    it('does not flip post-mount when the store toggles enabled→disabled (reload required by SPEC contract)', async () => {
      const { act } = await import('react')
      registerModule({
        id: 'feat-mod',
        name: 'Feat',
        disableable: true,
        panes: [{ kind: 'feat-pane', component: ({ pane }) => <div data-testid="real">real:{pane.id}</div> }],
      })
      const layout: PaneLayout = {
        type: 'leaf',
        pane: { id: 'p1', content: { kind: 'feat-pane' } as never },
      }
      render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
      expect(screen.getByTestId('real')).toBeInTheDocument()

      await act(async () => {
        useModuleEnabledStore.getState().setEnabled('feat-mod', false)
      })

      // The pane must keep rendering the real component until a manual reload
      // re-runs registerBuiltinModules(). This matches DisabledModulePlaceholder's
      // "Reload the page after enabling" / 「啟用後請手動重載頁面」 hint and
      // the file-opener / new-tab registries that only reconcile at bootstrap.
      expect(screen.getByTestId('real')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /feat-mod/i })).toBeNull()
    })

    it('renders the module-supplied custom disabledComponent when present', () => {
      const Custom = ({ moduleId, paneKind }: { moduleId: string; paneKind: string }) => (
        <div data-testid="custom-disabled">custom:{moduleId}:{paneKind}</div>
      )
      registerModule({
        id: 'custom-mod',
        name: 'Custom',
        disableable: true,
        panes: [{ kind: 'custom-pane', component: () => <div data-testid="real" /> }],
        disabledComponent: Custom,
      })
      useModuleEnabledStore.getState().setEnabled('custom-mod', false)
      const layout: PaneLayout = {
        type: 'leaf',
        pane: { id: 'p1', content: { kind: 'custom-pane' } as never },
      }
      render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
      expect(screen.getByTestId('custom-disabled').textContent).toBe('custom:custom-mod:custom-pane')
    })
  })

  // Compat regression: a grid-4-shaped layout (v-split of two h-splits, 4 leaves)
  // was previously special-cased. After removing that hardcoded path it must
  // still render correctly via the generic recursive split renderer — persisted
  // layouts keep the tree shape, not a pattern enum.
  const grid4Shape: PaneLayout = {
    type: 'split', id: 'outer', direction: 'v',
    children: [
      {
        type: 'split', id: 'top-row', direction: 'h',
        children: [
          { type: 'leaf', pane: { id: 'tl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'tr', content: { kind: 'dashboard' } } },
        ],
        sizes: [50, 50],
      },
      {
        type: 'split', id: 'bot-row', direction: 'h',
        children: [
          { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
        ],
        sizes: [50, 50],
      },
    ],
    sizes: [50, 50],
  }

  it('renders a grid-4-shaped layout via the generic recursive renderer (compat regression)', () => {
    registerModule({
      id: 'dashboard-grid4',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
    render(<PaneLayoutRenderer layout={grid4Shape} tabId="t1" isActive={true} />)
    // All 4 pane leaf areas must be rendered
    expect(screen.getByTestId('dash-tl').textContent).toBe('tl')
    expect(screen.getByTestId('dash-tr').textContent).toBe('tr')
    expect(screen.getByTestId('dash-bl').textContent).toBe('bl')
    expect(screen.getByTestId('dash-br').textContent).toBe('br')
  })

  it('renders a grid-4-shaped layout after splitting a cell — no grid special-case (F2 regression)', () => {
    registerModule({
      id: 'dashboard-grid4-split',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
    const tab: Tab = { id: 't1', pinned: false, locked: false, createdAt: 0, layout: grid4Shape }
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1', visitHistory: [] })
    // Split one grid cell → the outer v-split still holds two h-splits, but the
    // top-left "cell" is now itself a nested split. The old isGrid4 special-case
    // only checked the root/children, so this would have mis-triggered the
    // hardcoded linked-resize grid path. With the special-case removed, the tree
    // must render purely via generic recursion (5 leaves, no crash).
    useTabStore.getState().splitPaneBlank('t1', 'tl', 'v')
    const layout = useTabStore.getState().tabs['t1'].layout
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    // The original tl content survives the split and all sibling cells still render.
    expect(screen.getByTestId('dash-tl')).toBeTruthy()
    expect(screen.getByTestId('dash-tr')).toBeTruthy()
    expect(screen.getByTestId('dash-bl')).toBeTruthy()
    expect(screen.getByTestId('dash-br')).toBeTruthy()
    // The split introduced one extra leaf (a blank new-tab pane) → 5 leaves total.
    expect(countLeaves(layout)).toBe(5)
  })
})

describe('PaneLayoutRenderer context menu', () => {
  function registerDash() {
    registerModule({
      id: 'dashboard',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
  }
  function registerEditor() {
    registerModule({
      id: 'editor',
      name: 'Editor',
      panes: [{ kind: 'editor', component: ({ pane }) => <div data-testid={`ed-${pane.id}`}>{pane.id}</div> }],
    })
  }
  function seedTab(layout: PaneLayout): Tab {
    const tab: Tab = { id: 't1', pinned: false, locked: false, createdAt: 0, layout }
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1', visitHistory: [] })
    return tab
  }
  const singleLeaf: PaneLayout = { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } }
  const splitLeaves: PaneLayout = {
    type: 'split', id: 's1', direction: 'h',
    children: [
      { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } },
      { type: 'leaf', pane: { id: 'p2', content: { kind: 'dashboard' } } },
    ],
    sizes: [50, 50],
  }

  it('opens the pane menu and prevents default on a non-editor right-click', () => {
    registerDash()
    seedTab(singleLeaf)
    render(<PaneLayoutRenderer layout={singleLeaf} tabId="t1" isActive={true} />)
    const wrapper = screen.getByTestId('dash-p1').parentElement!
    const ev = createEvent.contextMenu(wrapper, { clientX: 50, clientY: 60 })
    fireEvent(wrapper, ev)
    expect(ev.defaultPrevented).toBe(true)
    expect(screen.getByText('Split Horizontal')).toBeInTheDocument()
  })

  it('does NOT intercept a right-click on an editor leaf (keeps Monaco native menu)', () => {
    registerEditor()
    const layout: PaneLayout = { type: 'leaf', pane: { id: 'p1', content: { kind: 'editor', source: { type: 'inapp' }, filePath: '/a.md' } } }
    seedTab(layout)
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    const wrapper = screen.getByTestId('ed-p1').parentElement!
    const ev = createEvent.contextMenu(wrapper)
    fireEvent(wrapper, ev)
    expect(ev.defaultPrevented).toBe(false)
    expect(screen.queryByText('Split Horizontal')).not.toBeInTheDocument()
  })

  it('lets Shift+right-click through as an escape hatch (native menu)', () => {
    registerDash()
    seedTab(singleLeaf)
    render(<PaneLayoutRenderer layout={singleLeaf} tabId="t1" isActive={true} />)
    const wrapper = screen.getByTestId('dash-p1').parentElement!
    const ev = createEvent.contextMenu(wrapper, { shiftKey: true })
    fireEvent(wrapper, ev)
    expect(ev.defaultPrevented).toBe(false)
    expect(screen.queryByText('Split Horizontal')).not.toBeInTheDocument()
  })

  it('stops propagation so a parent context-menu handler is not invoked', () => {
    registerDash()
    seedTab(singleLeaf)
    const parentSpy = vi.fn()
    render(
      <div onContextMenu={parentSpy}>
        <PaneLayoutRenderer layout={singleLeaf} tabId="t1" isActive={true} />
      </div>,
    )
    const wrapper = screen.getByTestId('dash-p1').parentElement!
    fireEvent.contextMenu(wrapper)
    expect(parentSpy).not.toHaveBeenCalled()
  })

  it('single-pane menu hides Close/Detach (canDetach false)', () => {
    registerDash()
    seedTab(singleLeaf)
    render(<PaneLayoutRenderer layout={singleLeaf} tabId="t1" isActive={true} />)
    fireEvent.contextMenu(screen.getByTestId('dash-p1').parentElement!)
    expect(screen.getByText('Split Horizontal')).toBeInTheDocument()
    expect(screen.queryByText('Close pane')).not.toBeInTheDocument()
    expect(screen.queryByText('Detach to tab')).not.toBeInTheDocument()
  })

  it('split-pane menu shows Close/Detach (canDetach true)', () => {
    registerDash()
    seedTab(splitLeaves)
    render(<PaneLayoutRenderer layout={splitLeaves} tabId="t1" isActive={true} />)
    fireEvent.contextMenu(screen.getByTestId('dash-p1').parentElement!)
    expect(screen.getByText('Close pane')).toBeInTheDocument()
    expect(screen.getByText('Detach to tab')).toBeInTheDocument()
  })

  it('wires split-h to splitPaneBlank(tabId, paneId, "h")', () => {
    registerDash()
    seedTab(singleLeaf)
    const spy = vi.spyOn(useTabStore.getState(), 'splitPaneBlank').mockImplementation(() => {})
    render(<PaneLayoutRenderer layout={singleLeaf} tabId="t1" isActive={true} />)
    fireEvent.contextMenu(screen.getByTestId('dash-p1').parentElement!)
    fireEvent.click(screen.getByText('Split Horizontal'))
    expect(spy).toHaveBeenCalledWith('t1', 'p1', 'h')
  })

  it('wires split-v to splitPaneBlank(tabId, paneId, "v")', () => {
    registerDash()
    seedTab(singleLeaf)
    const spy = vi.spyOn(useTabStore.getState(), 'splitPaneBlank').mockImplementation(() => {})
    render(<PaneLayoutRenderer layout={singleLeaf} tabId="t1" isActive={true} />)
    fireEvent.contextMenu(screen.getByTestId('dash-p1').parentElement!)
    fireEvent.click(screen.getByText('Split Vertical'))
    expect(spy).toHaveBeenCalledWith('t1', 'p1', 'v')
  })

  it('wires close to closePane(tabId, paneId)', () => {
    registerDash()
    seedTab(splitLeaves)
    const spy = vi.spyOn(useTabStore.getState(), 'closePane').mockImplementation(() => {})
    render(<PaneLayoutRenderer layout={splitLeaves} tabId="t1" isActive={true} />)
    fireEvent.contextMenu(screen.getByTestId('dash-p1').parentElement!)
    fireEvent.click(screen.getByText('Close pane'))
    expect(spy).toHaveBeenCalledWith('t1', 'p1')
  })

  it('guards close when the tab collapsed to a single leaf while the menu was open (no closeTab escalation)', async () => {
    const { act } = await import('react')
    registerDash()
    seedTab(splitLeaves)
    const closeTabSpy = vi.spyOn(useTabStore.getState(), 'closeTab').mockImplementation(() => {})
    render(<PaneLayoutRenderer layout={splitLeaves} tabId="t1" isActive={true} />)
    // Menu opens on p1 while the tab is a 2-pane split (canDetach true, Close shown).
    fireEvent.contextMenu(screen.getByTestId('dash-p1').parentElement!)
    expect(screen.getByText('Close pane')).toBeInTheDocument()
    // Another path collapses the tab back to a single leaf (closes p2). The menu
    // stays open because this instance renders from the captured `layout` prop.
    await act(async () => {
      useTabStore.getState().closePane('t1', 'p2')
    })
    // Clicking Close now must be guarded: closePane on a single leaf would escalate
    // to closeTab and destroy the whole tab. The action-time guard blocks it.
    fireEvent.click(screen.getByText('Close pane'))
    expect(closeTabSpy).not.toHaveBeenCalled()
    expect(useTabStore.getState().tabs['t1']).toBeDefined()
  })

  it('detach reuses the PaneHeader onDetach flow: detachPane + insertTab + setActiveTab', () => {
    registerDash()
    seedTab(splitLeaves)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', name: 'W', tabs: ['t1'], activeTabId: 't1' }],
      activeWorkspaceId: 'w1',
    })
    const detachSpy = vi.spyOn(useTabStore.getState(), 'detachPane').mockReturnValue('new-tab-id')
    const insertSpy = vi.spyOn(useWorkspaceStore.getState(), 'insertTab').mockImplementation(() => {})
    const setActiveSpy = vi.spyOn(useTabStore.getState(), 'setActiveTab').mockImplementation(() => {})

    render(<PaneLayoutRenderer layout={splitLeaves} tabId="t1" isActive={true} />)
    fireEvent.contextMenu(screen.getByTestId('dash-p1').parentElement!)
    fireEvent.click(screen.getByText('Detach to tab'))

    expect(detachSpy).toHaveBeenCalledWith('t1', 'p1', 't1')
    expect(insertSpy).toHaveBeenCalledWith('new-tab-id', 'w1', 't1')
    expect(setActiveSpy).toHaveBeenCalledWith('new-tab-id')
  })
})

describe('PaneLayoutRenderer — Hand to nex (P-C.3b)', () => {
  const H = 'h1'
  const CODE = 'zk16vd'
  const tmux = (id: string, over: Partial<TmuxSessionContent> = {}): PaneLayout => ({
    type: 'leaf',
    pane: { id, content: { kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: 'purdex', tmuxInstance: 'inst-1', ...over } },
  })
  const dash = (id: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'dashboard' } } })
  function registerKinds() {
    registerModule({
      id: 'tmux',
      name: 'Tmux',
      panes: [{ kind: 'tmux-session', component: ({ pane }) => <div data-testid={`tmux-${pane.id}`}>{pane.id}</div> }],
    })
    registerModule({
      id: 'dashboard',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
  }
  function seedTab(layout: PaneLayout) {
    const tab: Tab = { id: 't1', pinned: false, locked: false, createdAt: 0, layout }
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1', visitHistory: [] })
  }
  function seedReady(ready = true) {
    useNexHostStore.setState({
      byHost: {
        [H]: {
          info: null,
          capabilities: { delegate: { resume_session_id: true }, sandbox_profiles: ready ? ['default', 'handoff'] : ['default'] } as never,
          phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f',
        },
      },
    })
  }
  const liveCc = () => useAgentStore.setState({ agentTypes: { [compositeKey(H, CODE)]: 'cc' } })
  const rightClick = (testId: string) => fireEvent.contextMenu(screen.getByTestId(testId).parentElement!)
  let ensure: ReturnType<typeof vi.fn>

  beforeEach(() => {
    registerKinds()
    mockedHandToNex.mockReset()
    ensure = vi.fn().mockResolvedValue(undefined)
    useNexHostStore.setState({ byHost: {}, ensure } as never)
    useAgentStore.setState({ agentTypes: {} })
    useUndoToast.setState({ toast: null })
  })

  it('shows "Hand to nex" when the live agent is cc and the host is ready', () => {
    seedTab(tmux('p1'))
    seedReady()
    liveCc()
    render(<PaneLayoutRenderer layout={tmux('p1')} tabId="t1" isActive={true} />)
    rightClick('tmux-p1')
    expect(screen.getByText('Hand to nex')).toBeInTheDocument()
  })

  it('hides it when the live agent is codex', () => {
    seedTab(tmux('p1'))
    seedReady()
    useAgentStore.setState({ agentTypes: { [compositeKey(H, CODE)]: 'codex' } })
    render(<PaneLayoutRenderer layout={tmux('p1')} tabId="t1" isActive={true} />)
    rightClick('tmux-p1')
    expect(screen.getByText('Split Horizontal')).toBeInTheDocument()
    expect(screen.queryByText('Hand to nex')).not.toBeInTheDocument()
  })

  it('hides it when the host is not handoff-ready', () => {
    seedTab(tmux('p1'))
    seedReady(false)
    liveCc()
    render(<PaneLayoutRenderer layout={tmux('p1')} tabId="t1" isActive={true} />)
    rightClick('tmux-p1')
    expect(screen.queryByText('Hand to nex')).not.toBeInTheDocument()
  })

  it('a non-session pane never shows it', () => {
    seedTab(dash('p1'))
    seedReady()
    render(<PaneLayoutRenderer layout={dash('p1')} tabId="t1" isActive={true} />)
    rightClick('dash-p1')
    expect(screen.queryByText('Hand to nex')).not.toBeInTheDocument()
  })

  it('reacts to the agent store: the item appears once the daemon reports cc', async () => {
    const { act } = await import('react')
    seedTab(tmux('p1'))
    seedReady()
    render(<PaneLayoutRenderer layout={tmux('p1')} tabId="t1" isActive={true} />)
    rightClick('tmux-p1')
    expect(screen.queryByText('Hand to nex')).not.toBeInTheDocument()
    await act(async () => { liveCc() })
    expect(screen.getByText('Hand to nex')).toBeInTheDocument()
  })

  it('calls ensure(hostId) for a tmux-session leaf, not for other kinds', () => {
    seedTab(tmux('p1'))
    render(<PaneLayoutRenderer layout={tmux('p1')} tabId="t1" isActive={true} />)
    expect(ensure).toHaveBeenCalledWith(H)
    ensure.mockClear()
    cleanup()
    render(<PaneLayoutRenderer layout={dash('p1')} tabId="t1" isActive={true} />)
    expect(ensure).not.toHaveBeenCalled()
  })

  it('offers it on the SECONDARY leaf of a split and the dialog hands off that pane', async () => {
    const split: PaneLayout = {
      type: 'split', id: 's1', direction: 'h',
      children: [
        dash('p1'),
        tmux('p2', { rebuild: { sessionName: 'purdex', tmuxInstance: 'inst-1', agent: { type: 'cc', updatedAt: 1 }, capturedAt: 1 } }),
      ],
      sizes: [50, 50],
    }
    seedTab(split)
    seedReady()
    mockedHandToNex.mockResolvedValueOnce({ result: { execution_id: 'exc_1', state: 'running', session_id: 's', cwd: '/' }, swapped: true })
    const { act } = await import('react')
    render(<PaneLayoutRenderer layout={split} tabId="t1" isActive={true} />)

    rightClick('dash-p1')
    expect(screen.queryByText('Hand to nex')).not.toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })

    rightClick('tmux-p2')
    fireEvent.click(screen.getByText('Hand to nex'))
    expect(screen.getByTestId('handoff-dialog')).toBeInTheDocument()

    await act(async () => { fireEvent.click(screen.getByTestId('handoff-confirm')) })
    expect(mockedHandToNex).toHaveBeenCalledTimes(1)
    expect(mockedHandToNex).toHaveBeenCalledWith({
      hostId: H, sessionCode: CODE, tmuxInstance: 'inst-1', cachedName: 'purdex', tabId: 't1', paneId: 'p2', keepSession: true,
    })
    expect(screen.queryByTestId('handoff-dialog')).not.toBeInTheDocument()
    expect(useUndoToast.getState().toast?.message).toBe('Handed to nex.')
  })

  it('Cancel closes the dialog without a request (rebuild record as the only cc source)', () => {
    // P-D.3b: the session row's relay id used to be the cc source here; the
    // daemon no longer sends it, so the rebuild record stands in.
    const recorded = tmux('p1', { rebuild: { sessionName: 'purdex', tmuxInstance: 'inst-1', agent: { type: 'cc', updatedAt: 1 }, capturedAt: 1 } })
    seedTab(recorded)
    seedReady()
    render(<PaneLayoutRenderer layout={recorded} tabId="t1" isActive={true} />)
    rightClick('tmux-p1')
    fireEvent.click(screen.getByText('Hand to nex'))
    expect(screen.getByTestId('handoff-dialog')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('handoff-cancel'))
    expect(screen.queryByTestId('handoff-dialog')).not.toBeInTheDocument()
    expect(mockedHandToNex).not.toHaveBeenCalled()
  })

  it('does not open the dialog when the pane left the live layout while the menu was open', async () => {
    const split: PaneLayout = {
      type: 'split', id: 's1', direction: 'h',
      children: [tmux('p1'), dash('p2')],
      sizes: [50, 50],
    }
    seedTab(split)
    seedReady()
    liveCc()
    const { act } = await import('react')
    render(<PaneLayoutRenderer layout={split} tabId="t1" isActive={true} />)
    rightClick('tmux-p1')
    expect(screen.getByText('Hand to nex')).toBeInTheDocument()
    // Collapse the tab to the other leaf behind the open menu (setState rather
    // than closePane: an earlier test in this file leaves closePane spied).
    await act(async () => { useTabStore.getState().setTabLayout('t1', dash('p2')) })
    expect(useTabStore.getState().tabs['t1'].layout).toEqual(dash('p2'))
    fireEvent.click(screen.getByText('Hand to nex'))
    expect(screen.queryByTestId('handoff-dialog')).not.toBeInTheDocument()
  })
})

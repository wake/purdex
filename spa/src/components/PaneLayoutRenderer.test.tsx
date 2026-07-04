import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, createEvent, type RenderResult } from '@testing-library/react'
import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { isGrid4 } from './pane-layout-grid'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import type { PaneLayout, Tab } from '../types/tab'

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
})

describe('isGrid4', () => {
  it('returns true for a valid 2x2 grid layout', () => {
    const layout: PaneLayout = {
      type: 'split', id: 'outer', direction: 'v',
      children: [
        { type: 'split', id: 'top', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'tl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'tr', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
        { type: 'split', id: 'bot', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
      ],
      sizes: [50, 50],
    }
    expect(isGrid4(layout)).toBe(true)
  })

  it('returns false for a leaf layout', () => {
    const layout: PaneLayout = { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } }
    expect(isGrid4(layout)).toBe(false)
  })

  it('returns false for a horizontal split (not vertical outer)', () => {
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'h',
      children: [
        { type: 'split', id: 'a', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'p2', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
        { type: 'split', id: 'b', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'p3', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'p4', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
      ],
      sizes: [50, 50],
    }
    expect(isGrid4(layout)).toBe(false)
  })

  it('returns false when children are not splits', () => {
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'v',
      children: [
        { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } },
        { type: 'leaf', pane: { id: 'p2', content: { kind: 'dashboard' } } },
      ],
      sizes: [50, 50],
    }
    expect(isGrid4(layout)).toBe(false)
  })

  it('returns false when outer split has 3 children', () => {
    const makeSplit = (id: string) => ({
      type: 'split' as const, id, direction: 'h' as const,
      children: [
        { type: 'leaf' as const, pane: { id: `${id}-l`, content: { kind: 'dashboard' as const } } },
        { type: 'leaf' as const, pane: { id: `${id}-r`, content: { kind: 'dashboard' as const } } },
      ],
      sizes: [50, 50],
    })
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'v',
      children: [makeSplit('a'), makeSplit('b'), makeSplit('c')],
      sizes: [33, 33, 34],
    }
    expect(isGrid4(layout)).toBe(false)
  })

  it('validates grid-4 structure completely', () => {
    const layout: PaneLayout = {
      type: 'split', id: 'outer', direction: 'v',
      children: [
        { type: 'split', id: 'top', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'tl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'tr', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
        { type: 'split', id: 'bot', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
      ],
      sizes: [50, 50],
    }
    expect(isGrid4(layout)).toBe(true)
    // After isSplit guard, .children is accessible
    if (layout.type === 'split' && isGrid4(layout)) {
      expect(layout.children.length).toBe(2)
      expect(layout.direction).toBe('v')
      expect(layout.id).toBe('outer')
    }
  })
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

  it('renders grid-4 layout with 4 pane areas', () => {
    registerModule({
      id: 'dashboard-grid4',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
    // Grid-4: vertical split of two horizontal splits → 2x2 grid
    const layout: PaneLayout = {
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
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    // All 4 pane leaf areas must be rendered
    expect(screen.getByTestId('dash-tl')).toBeTruthy()
    expect(screen.getByTestId('dash-tr')).toBeTruthy()
    expect(screen.getByTestId('dash-bl')).toBeTruthy()
    expect(screen.getByTestId('dash-br')).toBeTruthy()
    // Verify text content of each pane
    expect(screen.getByTestId('dash-tl').textContent).toBe('tl')
    expect(screen.getByTestId('dash-tr').textContent).toBe('tr')
    expect(screen.getByTestId('dash-bl').textContent).toBe('bl')
    expect(screen.getByTestId('dash-br').textContent).toBe('br')
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

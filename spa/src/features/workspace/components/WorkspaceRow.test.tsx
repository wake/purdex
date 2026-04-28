import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import { WorkspaceRow } from './WorkspaceRow'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useTabStore } from '../../../stores/useTabStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'
import type { Workspace, Tab } from '../../../types/tab'

const mkWs = (id: string, name: string, tabs: string[] = []): Workspace => ({
  id,
  name,
  tabs,
  activeTabId: null,
})

const mkTab = (id: string, hostname: string): Tab =>
  ({
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `${id}-pane`,
        content: { kind: 'browser', url: `https://${hostname}.example.com` },
      },
    },
  }) as Tab

beforeEach(() => {
  cleanup()
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

function renderRow(ws: Workspace, overrides: Partial<React.ComponentProps<typeof WorkspaceRow>> = {}) {
  return render(
    <DndContext>
      <SortableContext items={[ws.id]}>
        <WorkspaceRow
          workspace={ws}
          isActive={false}
          tabsById={{}}
          activeTabId={null}
          onSelectWorkspace={() => {}}
          onContextMenuWorkspace={() => {}}
          onSelectTab={() => {}}
          onCloseTab={() => {}}
          onMiddleClickTab={() => {}}
          onContextMenuTab={() => {}}
          onAddTabToWorkspace={() => {}}
          {...overrides}
        />
      </SortableContext>
    </DndContext>,
  )
}

describe('WorkspaceRow', () => {
  it('renders workspace name', () => {
    renderRow(mkWs('ws-1', 'Purdex'))
    expect(screen.getByText('Purdex')).toBeInTheDocument()
  })

  it('header click selects workspace', () => {
    const onSelect = vi.fn()
    renderRow(mkWs('ws-1', 'Purdex'), { onSelectWorkspace: onSelect })
    fireEvent.click(screen.getByText('Purdex'))
    expect(onSelect).toHaveBeenCalledWith('ws-1')
  })

  it('tabs hidden when workspaceExpanded[id] is false/undefined', () => {
    renderRow(mkWs('ws-1', 'W', ['t1']), { tabsById: { t1: mkTab('t1', 'alpha') } })
    expect(screen.queryByText('alpha.example.com')).not.toBeInTheDocument()
  })

  it('tabs shown when workspaceExpanded[id]=true', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide', workspaceExpanded: { 'ws-1': true } })
    renderRow(mkWs('ws-1', 'W', ['t1']), { tabsById: { t1: mkTab('t1', 'alpha') } })
    // Label appears twice per row: visible title span + HoverTooltip.
    expect(screen.getAllByText('alpha.example.com').length).toBeGreaterThan(0)
  })

  it('clicking title on ACTIVE ws toggles expand (does not re-select)', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
    const onSelect = vi.fn()
    renderRow(mkWs('ws-1', 'Purdex'), { isActive: true, onSelectWorkspace: onSelect })
    fireEvent.click(screen.getByText('Purdex'))
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(true)
    expect(onSelect).not.toHaveBeenCalled()

    fireEvent.click(screen.getByText('Purdex'))
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(false)
    expect(onSelect).not.toHaveBeenCalled()
  })

  it("active-click toggle is inert when tabPosition='top' (no inline tabs); still selects", () => {
    useLayoutStore.setState({ tabPosition: 'top' })
    const onSelect = vi.fn()
    renderRow(mkWs('ws-1', 'Purdex'), { isActive: true, onSelectWorkspace: onSelect })
    fireEvent.click(screen.getByText('Purdex'))
    expect(onSelect).toHaveBeenCalledWith('ws-1')
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBeFalsy()
  })

  it('clicking title on INACTIVE ws selects (does not toggle)', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
    const onSelect = vi.fn()
    renderRow(mkWs('ws-1', 'Purdex'), { isActive: false, onSelectWorkspace: onSelect })
    fireEvent.click(screen.getByText('Purdex'))
    expect(onSelect).toHaveBeenCalledWith('ws-1')
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBeFalsy()
  })

  it('chevron toggles expand state', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
    renderRow(mkWs('ws-1', 'W', ['t1']), { tabsById: { t1: mkTab('t1', 'alpha') } })
    const chevron = screen.getByRole('button', { name: /expand|collapse/i })
    fireEvent.click(chevron)
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(true)
  })

  it('+ button visible when expanded, calls onAddTabToWorkspace', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide', workspaceExpanded: { 'ws-1': true } })
    const onAdd = vi.fn()
    renderRow(mkWs('ws-1', 'W', []), { onAddTabToWorkspace: onAdd })
    const addBtn = screen.getByRole('button', { name: /new tab in W/i })
    fireEvent.click(addBtn)
    expect(onAdd).toHaveBeenCalledWith('ws-1')
  })

  describe('droppable header (Phase 3 PR D)', () => {
    it('exposes header with data-testid=ws-header-<id> for drop target lookup', () => {
      renderRow(mkWs('ws-1', 'Alpha'))
      expect(screen.getByTestId('ws-header-ws-1')).toBeInTheDocument()
    })
  })

  describe('drag-steals-click guard', () => {
    it('name button stops pointer-down propagation so dnd-kit drag does not start on click', () => {
      renderRow(mkWs('ws-1', 'Alpha'))
      const nameBtn = screen.getByText('Alpha').closest('button')!
      const evt = new Event('pointerdown', { bubbles: true, cancelable: true })
      const stopPropagationSpy = vi.spyOn(evt, 'stopPropagation')
      nameBtn.dispatchEvent(evt)
      expect(stopPropagationSpy).toHaveBeenCalled()
    })

    it('chevron does not block pointer-down (keeps row drag reachable)', () => {
      useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
      renderRow(mkWs('ws-1', 'Alpha'))
      const chevron = screen.getByRole('button', { name: /expand|collapse/i })
      const evt = new Event('pointerdown', { bubbles: true, cancelable: true })
      const stopPropagationSpy = vi.spyOn(evt, 'stopPropagation')
      chevron.dispatchEvent(evt)
      expect(stopPropagationSpy).not.toHaveBeenCalled()
    })
  })
})

describe('WorkspaceRow chevron visibility', () => {
  it("hides chevron when tabPosition='top'", () => {
    useLayoutStore.setState({ tabPosition: 'top' })
    renderRow(mkWs('w1', 'Alpha'))
    expect(screen.queryByLabelText(/expand alpha/i)).not.toBeInTheDocument()
  })

  it("shows chevron when tabPosition='left'", () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
    renderRow(mkWs('w1', 'Alpha'))
    expect(screen.getByLabelText(/expand alpha/i)).toBeInTheDocument()
  })

  it("shows chevron when tabPosition='both'", () => {
    useLayoutStore.setState({ tabPosition: 'both', activityBarWidth: 'wide' })
    renderRow(mkWs('w1', 'Alpha'))
    expect(screen.getByLabelText(/expand alpha/i)).toBeInTheDocument()
  })
})

describe('WorkspaceRow — header "+ New tab"', () => {
  const baseProps = {
    workspace: { id: 'w1', name: 'Alpha', tabs: [], activeTabId: null } as never,
    isActive: false,
    tabsById: {},
    activeTabId: null,
    onSelectWorkspace: () => {},
    onSelectTab: () => {},
    onCloseTab: () => {},
    onMiddleClickTab: () => {},
    onContextMenuTab: () => {},
  }

  beforeEach(() => {
    useLayoutStore.setState({
      ...useLayoutStore.getInitialState(),
      tabPosition: 'left',
      activityBarWidth: 'wide',
    })
  })

  it('header shows a hover-revealed "+ New tab" button when tabs are visible', () => {
    render(<WorkspaceRow {...baseProps} onAddTabToWorkspace={() => {}} />)
    expect(screen.getByLabelText(/new tab in alpha/i)).toBeInTheDocument()
  })

  it('calls onAddTabToWorkspace when header plus is clicked', () => {
    const onAdd = vi.fn()
    render(<WorkspaceRow {...baseProps} onAddTabToWorkspace={onAdd} />)
    fireEvent.click(screen.getByLabelText(/new tab in alpha/i))
    expect(onAdd).toHaveBeenCalledWith('w1')
  })

  it('only one "+ New tab" affordance exists (bottom button removed)', () => {
    useLayoutStore.setState({
      ...useLayoutStore.getInitialState(),
      tabPosition: 'left',
      activityBarWidth: 'wide',
      workspaceExpanded: { w1: true },
    })
    render(<WorkspaceRow {...baseProps} onAddTabToWorkspace={() => {}} />)
    expect(screen.getAllByLabelText(/new tab in alpha/i)).toHaveLength(1)
  })

  it("does NOT render header '+' when tabPosition='top'", () => {
    useLayoutStore.setState({ tabPosition: 'top' })
    render(<WorkspaceRow {...baseProps} onAddTabToWorkspace={() => {}} />)
    expect(screen.queryByLabelText(/new tab in alpha/i)).not.toBeInTheDocument()
  })
})

// ──────────────────────────────────────────────────────────────────────
// Phase 1b' — Plus hover popover + touch fallback (codex round-1 B8 / C17)
// ──────────────────────────────────────────────────────────────────────

function setupHoverPopoverFixtures(opts: { withBindings: boolean }) {
  useQuickCommandStore.setState({
    global: opts.withBindings ? [{ id: 'cmd-x', name: 'X', command: 'x' }] : [],
    byHost: {},
    bindings: opts.withBindings ? { 'cmd-x': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] } : {},
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: ['h1'],
    runtime: { h1: { status: 'connected' } },
    activeHostId: 'h1',
  } as Partial<ReturnType<typeof useHostStore.getState>> as never)
  // workspace has a tmux-session tab so inferWorkspaceHostId returns 'h1' (no picker prompt)
  useTabStore.setState({
    tabs: {
      t1: {
        id: 't1', pinned: false, locked: false, createdAt: 0,
        layout: {
          type: 'leaf',
          pane: {
            id: 'p1',
            content: {
              kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal',
              cachedName: 'x', tmuxInstance: '',
            },
          },
        },
      },
    },
    tabOrder: ['t1'],
    activeTabId: 't1',
  } as Partial<ReturnType<typeof useTabStore.getState>> as never)
  useLayoutStore.setState({
    ...useLayoutStore.getInitialState(),
    tabPosition: 'left',
    activityBarWidth: 'wide',
  })
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
}

// Props 依 WorkspaceRow.tsx Props 介面 (L11-24) 對齊
const hoverBaseProps: React.ComponentProps<typeof WorkspaceRow> = {
  workspace: { id: 'w1', name: 'WS', tabs: ['t1'], activeTabId: 't1' },
  isActive: false,
  tabsById: {
    t1: {
      id: 't1', pinned: false, locked: false, createdAt: 0,
      layout: {
        type: 'leaf' as const,
        pane: {
          id: 'p1',
          content: {
            kind: 'tmux-session' as const, hostId: 'h1', sessionCode: 'sess', mode: 'terminal' as const,
            cachedName: 'x', tmuxInstance: '',
          },
        },
      },
    } as Tab,
  },
  activeTabId: 't1',
  onSelectWorkspace: vi.fn(),
  onContextMenuWorkspace: vi.fn(),
  onSelectTab: vi.fn(),
  onCloseTab: vi.fn(),
  onMiddleClickTab: vi.fn(),
  onContextMenuTab: vi.fn(),
  onRenameTab: vi.fn(),
  onAddTabToWorkspace: vi.fn(),
}

describe("WorkspaceRow — Plus hover popover (Phase 1b')", () => {
  beforeEach(() => {
    cleanup()
    setupHoverPopoverFixtures({ withBindings: true })
  })

  it('opens popover on Plus hover, closes on mouseleave', () => {
    render(<WorkspaceRow {...hoverBaseProps} />)
    const plusBtn = screen.getByLabelText(/new tab in/i)
    const hub = plusBtn.parentElement!

    // 預設未 hover → chip 不在 DOM
    expect(screen.queryByLabelText(/^X/)).toBeNull()

    // hover Plus → popover 顯示
    fireEvent.mouseEnter(hub)
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()

    // mouseleave wrapper → popover 收回
    fireEvent.mouseLeave(hub)
    expect(screen.queryByLabelText(/^X/)).toBeNull()
  })

  it('does NOT open popover when no WORKSPACE_ACTIONS bindings exist', () => {
    setupHoverPopoverFixtures({ withBindings: false })
    render(<WorkspaceRow {...hoverBaseProps} />)
    const plusBtn = screen.getByLabelText(/new tab in/i)
    fireEvent.mouseEnter(plusBtn.parentElement!)
    // popover wrapper not rendered → no toolbar / no chip
    expect(screen.queryByLabelText(/^X/)).toBeNull()
  })

  // codex round-1 P2 (F3 — picker hover-dismissal). Without this guard the
  // hub's mouseleave handler unconditionally collapsed the popover, taking the
  // HostPickerPopover (rendered inside) down with it the moment the user
  // moved the pointer toward a host option.
  it('keeps popover up while picker is open; collapses after picker resolves outside hub', () => {
    // Override fixture so workspace has no tmux-session tabs → hostId resolves
    // to null → clicking a chip opens HostPickerPopover.
    useTabStore.setState({
      tabs: {},
      tabOrder: [],
      activeTabId: null,
    } as Partial<ReturnType<typeof useTabStore.getState>> as never)
    const props: React.ComponentProps<typeof WorkspaceRow> = {
      ...hoverBaseProps,
      workspace: { ...hoverBaseProps.workspace, tabs: [] },
      tabsById: {},
    }
    render(<WorkspaceRow {...props} />)
    const plusBtn = screen.getByLabelText(/new tab in/i)
    const hub = plusBtn.parentElement!

    fireEvent.mouseEnter(hub)
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()

    fireEvent.click(screen.getByLabelText(/^X/))
    expect(screen.getByRole('listbox')).toBeInTheDocument()

    // mouseleave the hub while picker is up — popover MUST stay so the user
    // can drift onto a host option without losing it.
    fireEvent.mouseLeave(hub)
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()

    // select a host → picker resolves → onPickerOpenChange(false) →
    // pointerInHubRef is false (we left earlier) → popover collapses.
    fireEvent.click(screen.getByText(/mlab/))
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(screen.queryByLabelText(/^X/)).toBeNull()
  })
})

describe('WorkspaceRow — touch fallback (codex round-1 C17)', () => {
  beforeEach(() => {
    cleanup()
    setupHoverPopoverFixtures({ withBindings: true })
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('long-press (>=500ms) on Plus opens the popover; tap chip executes', () => {
    render(<WorkspaceRow {...hoverBaseProps} />)
    const plusBtn = screen.getByLabelText(/new tab in/i)
    const hub = plusBtn.parentElement!

    // touch start → wait 500ms → long-press fires
    fireEvent.touchStart(hub)
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()
    fireEvent.touchEnd(hub)
    // popover stays open (long-press fired)
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()
  })

  it('short tap (<500ms) on Plus triggers add-tab, NOT popover', () => {
    const onAddTabToWorkspace = vi.fn()
    render(<WorkspaceRow {...hoverBaseProps} onAddTabToWorkspace={onAddTabToWorkspace} />)
    const plusBtn = screen.getByLabelText(/new tab in/i)

    fireEvent.touchStart(plusBtn.parentElement!)
    act(() => {
      vi.advanceTimersByTime(200) // less than 500ms
    })
    fireEvent.touchEnd(plusBtn.parentElement!)
    fireEvent.click(plusBtn)

    expect(onAddTabToWorkspace).toHaveBeenCalledWith('w1')
    expect(screen.queryByLabelText(/^X/)).toBeNull()
  })

  // codex round-2 A2/F2 — long-press click suppression must be one-shot.
  // Without resetting longPressFiredRef the synthetic click after touchend
  // would leave the ref true, blocking every subsequent mouse / keyboard
  // activation of Plus until another touchstart on the hub.
  it('long-press → tap outside (close) → click Plus → onAddTabToWorkspace fires (suppressor cleared)', () => {
    const onAddTabToWorkspace = vi.fn()
    render(
      <WorkspaceRow {...hoverBaseProps} onAddTabToWorkspace={onAddTabToWorkspace} />,
    )
    const plusBtn = screen.getByLabelText(/new tab in/i)
    const hub = plusBtn.parentElement!

    // 1. long-press → popover open
    fireEvent.touchStart(hub)
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()
    fireEvent.touchEnd(hub)

    // 2. simulate the synthetic compatibility click that browsers fire after
    // touchstart/touchend: hits Plus, but onClick should suppress add-tab AND
    // clear the suppressor.
    fireEvent.click(plusBtn)
    expect(onAddTabToWorkspace).not.toHaveBeenCalled()

    // 3. tap outside to dismiss popover (also clears via the popoverOpen useEffect).
    fireEvent.pointerDown(document.body)
    expect(screen.queryByLabelText(/^X/)).toBeNull()

    // 4. click Plus again with a real mouse → MUST trigger add-tab now.
    fireEvent.click(plusBtn)
    expect(onAddTabToWorkspace).toHaveBeenCalledWith('w1')
  })

  it('tapping outside the hub closes an open touch-popover', () => {
    render(<WorkspaceRow {...hoverBaseProps} />)
    const plusBtn = screen.getByLabelText(/new tab in/i)
    const hub = plusBtn.parentElement!

    fireEvent.touchStart(hub)
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()
    fireEvent.touchEnd(hub)

    // tap outside the hub
    fireEvent.pointerDown(document.body)
    expect(screen.queryByLabelText(/^X/)).toBeNull()
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import { WorkspaceRow } from './WorkspaceRow'
import { useLayoutStore } from '../../../stores/useLayoutStore'
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
    expect(screen.getByText('alpha.example.com')).toBeInTheDocument()
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

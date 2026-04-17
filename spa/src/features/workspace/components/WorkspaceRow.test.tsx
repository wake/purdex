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
    useLayoutStore.setState({ workspaceExpanded: { 'ws-1': true } })
    renderRow(mkWs('ws-1', 'W', ['t1']), { tabsById: { t1: mkTab('t1', 'alpha') } })
    expect(screen.getByText('alpha.example.com')).toBeInTheDocument()
  })

  it('chevron toggles expand state', () => {
    renderRow(mkWs('ws-1', 'W', ['t1']), { tabsById: { t1: mkTab('t1', 'alpha') } })
    const chevron = screen.getByRole('button', { name: /expand|collapse/i })
    fireEvent.click(chevron)
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(true)
  })

  it('+ button visible when expanded, calls onAddTabToWorkspace', () => {
    useLayoutStore.setState({ workspaceExpanded: { 'ws-1': true } })
    const onAdd = vi.fn()
    renderRow(mkWs('ws-1', 'W', []), { onAddTabToWorkspace: onAdd })
    const addBtn = screen.getByRole('button', { name: /new tab in W/i })
    fireEvent.click(addBtn)
    expect(onAdd).toHaveBeenCalledWith('ws-1')
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
  })
})

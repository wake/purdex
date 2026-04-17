import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { HomeRow } from './HomeRow'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import type { Tab } from '../../../types/tab'

const mkTab = (id: string, hostname: string): Tab =>
  ({
    id,
    kind: 'new-tab',
    locked: false,
    layout: {
      type: 'leaf',
      pane: {
        id: `${id}-pane`,
        content: { kind: 'browser', url: `https://${hostname}.example.com` },
      },
    },
  }) as unknown as Tab

beforeEach(() => {
  cleanup()
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

function renderRow(overrides: Partial<React.ComponentProps<typeof HomeRow>> = {}) {
  return render(
    <DndContext>
      <HomeRow
        isActive={false}
        standaloneTabIds={[]}
        tabsById={{}}
        activeTabId={null}
        onSelectHome={() => {}}
        onSelectTab={() => {}}
        onCloseTab={() => {}}
        onMiddleClickTab={() => {}}
        onContextMenuTab={() => {}}
        {...overrides}
      />
    </DndContext>,
  )
}

describe('HomeRow', () => {
  it('renders Home label', () => {
    renderRow()
    expect(screen.getByText(/home/i)).toBeInTheDocument()
  })

  it('header click calls onSelectHome', () => {
    const onSelectHome = vi.fn()
    renderRow({ onSelectHome })
    fireEvent.click(screen.getByText(/home/i))
    expect(onSelectHome).toHaveBeenCalled()
  })

  it('tabs hidden when home not expanded', () => {
    renderRow({ standaloneTabIds: ['t1'], tabsById: { t1: mkTab('t1', 'alpha') } })
    expect(screen.queryByText('alpha.example.com')).not.toBeInTheDocument()
  })

  it('tabs shown when workspaceExpanded["home"]=true', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide', workspaceExpanded: { home: true } })
    renderRow({ standaloneTabIds: ['t1'], tabsById: { t1: mkTab('t1', 'alpha') } })
    expect(screen.getByText('alpha.example.com')).toBeInTheDocument()
  })

  it('chevron toggles home expand state', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
    renderRow({ standaloneTabIds: ['t1'], tabsById: { t1: mkTab('t1', 'alpha') } })
    const chevron = screen.getByRole('button', { name: /expand home|collapse home/i })
    fireEvent.click(chevron)
    expect(useLayoutStore.getState().workspaceExpanded['home']).toBe(true)
  })

  describe('droppable header (Phase 3 PR D)', () => {
    it('exposes header with data-testid=home-header for drop target lookup', () => {
      renderRow()
      expect(screen.getByTestId('home-header')).toBeInTheDocument()
    })
  })
})

describe('HomeRow chevron visibility', () => {
  it("hides chevron when tabPosition='top'", () => {
    useLayoutStore.setState({ tabPosition: 'top' })
    renderRow()
    expect(screen.queryByLabelText(/expand home/i)).not.toBeInTheDocument()
  })

  it("shows chevron when tabPosition='left'", () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
    renderRow()
    expect(screen.getByLabelText(/expand home/i)).toBeInTheDocument()
  })

  it("shows chevron when tabPosition='both'", () => {
    useLayoutStore.setState({ tabPosition: 'both', activityBarWidth: 'wide' })
    renderRow()
    expect(screen.getByLabelText(/expand home/i)).toBeInTheDocument()
  })
})

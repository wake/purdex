import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { ComponentProps } from 'react'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { ActivityBarNarrow } from './ActivityBarNarrow'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { __resetProfileSwitcherForTest } from '../../../stores/useProfileSwitcherStore'

// The list itself is WorkerList's business (its own tests); here it only has to be there or not.
vi.mock('../../../components/executions/WorkerList', () => ({
  WorkerList: () => <div data-testid="worker-list" />,
}))

const NO_SLAVES = { slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0 }

beforeEach(() => {
  useLocalProfilesStore.setState(NO_SLAVES)
  useLayoutStore.setState(useLayoutStore.getInitialState())
  __resetProfileSwitcherForTest()
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('ActivityBarNarrow', () => {
  it('renders Home button', () => {
    render(
      <ActivityBarNarrow
        workspaces={[]}
        activeWorkspaceId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByTitle(/home/i)).toBeInTheDocument()
  })

  it('keeps Home outside the workspace scroll region', () => {
    const { container } = render(
      <ActivityBarNarrow
        workspaces={[
          { id: 'w1', name: 'Purdex', tabs: [], activeTabId: null },
          { id: 'w2', name: 'Client A', tabs: [], activeTabId: null },
        ]}
        activeWorkspaceId="w1"
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )

    const workspaceScroll = screen.getByTestId('activity-bar-workspace-scroll')
    expect(workspaceScroll).toHaveClass('overflow-y-auto')
    expect(screen.getByTitle(/home/i).closest('[data-testid="activity-bar-workspace-scroll"]')).toBeNull()
    expect(container.querySelector('[data-testid="activity-bar-workspace-separator"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="activity-bar-workspace-separator"]')?.closest('[data-testid="activity-bar-workspace-scroll"]')).toBeNull()
  })

  describe('Home button → profile switcher', () => {
    function renderBar(onSelectHome = vi.fn()) {
      render(
        <ActivityBarNarrow
          workspaces={[{ id: 'w1', name: 'Purdex', tabs: [], activeTabId: null }]}
          activeWorkspaceId="w1"
          onSelectWorkspace={() => {}}
          onSelectHome={onSelectHome}
          onAddWorkspace={() => {}}
          onOpenHosts={() => {}}
          onOpenSettings={() => {}}
        />,
      )
      return onSelectHome
    }

    it('no slaves: a click selects Home, as today — no aria-haspopup, no menu', () => {
      const onSelectHome = renderBar()
      const home = screen.getByTitle(/home/i)
      expect(home).toBe(screen.getByTestId('home-button'))
      expect(home).not.toHaveAttribute('aria-haspopup')
      fireEvent.click(home)
      expect(onSelectHome).toHaveBeenCalledTimes(1)
      expect(screen.queryByTestId('profile-switcher-menu')).toBeNull()
    })

    it('with a slave: the button is a menu trigger and the menu opens BESIDE it, outside the overflow-hidden bar', () => {
      useLocalProfilesStore.setState({
        slaves: { s1: { id: 's1', name: 'Scratch', createdAt: 1, shownHostIds: [], world: { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null } } },
        slaveOrder: ['s1'],
      })
      const rect = (left: number, top: number, width: number, height: number): DOMRect =>
        ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON: () => ({}) }) as DOMRect
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
        if (this.dataset.testid === 'home-button') return rect(7, 8, 30, 30)
        if (this.dataset.testid === 'profile-switcher-menu') return rect(0, 0, 200, 120)
        return rect(0, 0, 0, 0)
      })
      const onSelectHome = renderBar()
      const home = screen.getByTestId('home-button')
      expect(home).toHaveAttribute('aria-haspopup', 'menu')
      expect(home).toHaveAttribute('aria-expanded', 'false')
      fireEvent.click(home)
      expect(onSelectHome).not.toHaveBeenCalled()
      expect(home).toHaveAttribute('aria-expanded', 'true')
      const menu = screen.getByTestId('profile-switcher-menu')
      expect(menu.parentElement).toBe(document.body)
      expect(menu.style.left).toBe('41px') // right of the 30 px button, not under it
      expect(menu.style.top).toBe('8px')
      expect(screen.getByTestId('profile-item-s1')).toBeInTheDocument()
    })
  })

  // Spec §4.6 (rule B.3): the Workers button opens the list in a floating panel beside the bar. The panel is local to
  // this bar — it neither reads nor writes the wide bar's workerListOpen, and the narrow/wide setting stays put.
  describe('bottom group', () => {
    function renderBar(over: Partial<ComponentProps<typeof ActivityBarNarrow>> = {}) {
      return render(
        <ActivityBarNarrow
          workspaces={[{ id: 'w1', name: 'Purdex', tabs: [], activeTabId: null }]}
          activeWorkspaceId="w1"
          onSelectWorkspace={() => {}}
          onSelectHome={() => {}}
          onAddWorkspace={() => {}}
          onOpenHosts={() => {}}
          onOpenSettings={() => {}}
          {...over}
        />,
      )
    }
    const workers = () => screen.getByRole('button', { name: 'Workers' })
    /** A real click: the mousedown reaches the document first, then the click lands on the button. */
    const press = (el: HTMLElement) => {
      fireEvent.mouseDown(el)
      fireEvent.click(el)
    }

    it('renders the shared BottomNav: New workspace, Workers, Hosts, Settings, each with a title', () => {
      renderBar()
      const nav = screen.getByTestId('bottom-nav')
      const titles = within(nav).getAllByRole('button').map((b) => b.getAttribute('title'))
      expect(titles).toEqual(['New workspace', 'Workers', 'Hosts', 'Settings'])
      expect(screen.queryByTestId('bottom-nav-compact-toggle')).toBeNull()
    })

    it('wires the other entries to the bar props', () => {
      const onAddWorkspace = vi.fn()
      const onOpenHosts = vi.fn()
      const onOpenSettings = vi.fn()
      renderBar({ onAddWorkspace, onOpenHosts, onOpenSettings })
      fireEvent.click(screen.getByRole('button', { name: 'New workspace' }))
      fireEvent.click(screen.getByRole('button', { name: 'Hosts' }))
      fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
      expect(onAddWorkspace).toHaveBeenCalledTimes(1)
      expect(onOpenHosts).toHaveBeenCalledTimes(1)
      expect(onOpenSettings).toHaveBeenCalledTimes(1)
    })

    it('Workers opens the list in a floating panel on document.body; a second click closes it', () => {
      renderBar()
      expect(workers()).toHaveAttribute('aria-pressed', 'false')
      expect(screen.queryByTestId('workers-panel')).toBeNull()

      press(workers())
      const panel = screen.getByTestId('workers-panel')
      expect(panel.parentElement).toBe(document.body)
      expect(panel).toHaveAccessibleName('Workers')
      expect(within(panel).getByTestId('worker-list')).toBeInTheDocument()
      expect(workers()).toHaveAttribute('aria-pressed', 'true')

      // The panel's outside-mousedown skips its anchor, so the button's own click is what closes it.
      press(workers())
      expect(screen.queryByTestId('workers-panel')).toBeNull()
      expect(workers()).toHaveAttribute('aria-pressed', 'false')
    })

    it('places the panel beside the button, bottom edges aligned', () => {
      const rect = (left: number, top: number, width: number, height: number): DOMRect =>
        ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON: () => ({}) }) as DOMRect
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
        if (this.getAttribute('title') === 'Workers') return rect(7, 500, 30, 30)
        if (this.dataset.testid === 'workers-panel') return rect(0, 0, 320, 200)
        return rect(0, 0, 0, 0)
      })
      renderBar()
      press(workers())
      const panel = screen.getByTestId('workers-panel')
      expect(panel.style.left).toBe('41px') // the button's right edge (37) + 4
      expect(panel.style.top).toBe('330px') // the button's bottom (530) − the panel's height (200)
    })

    it('Escape closes the panel', () => {
      renderBar()
      press(workers())
      expect(screen.getByTestId('workers-panel')).toBeInTheDocument()
      fireEvent.keyDown(document, { key: 'Escape' })
      expect(screen.queryByTestId('workers-panel')).toBeNull()
      expect(workers()).toHaveAttribute('aria-pressed', 'false')
    })

    it('a mousedown outside the panel and the button closes it', () => {
      renderBar()
      press(workers())
      fireEvent.mouseDown(document.body)
      expect(screen.queryByTestId('workers-panel')).toBeNull()
    })

    it("leaves the store's workerListOpen alone, and does not follow it", () => {
      renderBar()
      press(workers())
      expect(useLayoutStore.getState().workerListOpen).toBe(false)
      press(workers())
      expect(useLayoutStore.getState().workerListOpen).toBe(false)

      cleanup()
      useLayoutStore.setState({ workerListOpen: true })
      renderBar()
      expect(workers()).toHaveAttribute('aria-pressed', 'false')
      expect(screen.queryByTestId('workers-panel')).toBeNull()
      expect(useLayoutStore.getState().activityBarWidth).toBe(useLayoutStore.getInitialState().activityBarWidth)
    })
  })
})

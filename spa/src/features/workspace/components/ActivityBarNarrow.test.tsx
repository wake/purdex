import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { ActivityBarNarrow } from './ActivityBarNarrow'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'

const NO_SLAVES = { slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0 }

beforeEach(() => {
  useLocalProfilesStore.setState(NO_SLAVES)
  useProfileSwitcherStore.setState({ open: false })
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
        slaves: { s1: { id: 's1', name: 'Scratch', createdAt: 1, world: { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null } } },
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
})

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TabContent } from './TabContent'
import { registerPaneRenderer, clearPaneRegistry } from '../lib/pane-registry'
import { createTab } from '../types/tab'
import type { Tab, Pane } from '../types/tab'

const MockSessionRenderer = ({ pane }: { pane: Pane }) => (
  <div data-testid="session-renderer">Session: {pane.content.kind === 'tmux-session' ? pane.content.sessionCode : ''}</div>
)
const MockDashboardRenderer = () => (
  <div data-testid="dashboard-renderer">Dashboard</div>
)

beforeEach(() => {
  cleanup()
  clearPaneRegistry()
  registerPaneRenderer('tmux-session', { component: MockSessionRenderer })
  registerPaneRenderer('dashboard', { component: MockDashboardRenderer })
})

const sessionTab: Tab = {
  ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }),
  id: 't1',
}

const dashboardTab: Tab = {
  ...createTab({ kind: 'dashboard' }),
  id: 't3',
}

describe('TabContent', () => {
  it('renders registered session renderer', () => {
    render(<TabContent activeTab={sessionTab} allTabs={[sessionTab]} />)
    expect(screen.getByTestId('session-renderer')).toBeTruthy()
  })

  it('renders registered dashboard renderer', () => {
    render(<TabContent activeTab={dashboardTab} allTabs={[dashboardTab]} />)
    expect(screen.getByTestId('dashboard-renderer')).toBeTruthy()
  })

  it('renders empty state when no active tab', () => {
    render(<TabContent activeTab={null} allTabs={[]} />)
    expect(screen.getByText(/選擇或建立/)).toBeTruthy()
  })

  it('sets inert on non-active keep-alive tabs', () => {
    // First render with dashboard active to put it in the alive pool
    const { container, rerender } = render(
      <TabContent activeTab={dashboardTab} allTabs={[sessionTab, dashboardTab]} />,
    )
    // Switch to session tab — dashboard stays in pool but becomes inactive
    rerender(
      <TabContent activeTab={sessionTab} allTabs={[sessionTab, dashboardTab]} />,
    )
    const wrappers = container.querySelectorAll('[class*="absolute"]')
    // Active tab (session) should NOT have inert
    const activeWrapper = Array.from(wrappers).find((w) => w.querySelector('[data-testid="session-renderer"]'))
    expect(activeWrapper).not.toBeNull()
    expect(activeWrapper?.getAttribute('inert')).toBeNull()
    // Inactive tab (dashboard) should have inert
    const inactiveWrapper = Array.from(wrappers).find((w) => w.querySelector('[data-testid="dashboard-renderer"]'))
    expect(inactiveWrapper).not.toBeNull()
    // jsdom may not reflect `inert` as an HTML attribute; check the React prop via data attribute fallback
    expect(inactiveWrapper?.getAttribute('inert')).not.toBeNull()
  })
})

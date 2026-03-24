import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TabContent } from './TabContent'
import { registerPaneRenderer, clearPaneRegistry } from '../lib/pane-registry'
import { createTab } from '../types/tab'
import type { Tab, Pane } from '../types/tab'

const MockSessionRenderer = ({ pane }: { pane: Pane }) => (
  <div data-testid="session-renderer">Session: {pane.content.kind === 'session' ? pane.content.sessionCode : ''}</div>
)
const MockDashboardRenderer = () => (
  <div data-testid="dashboard-renderer">Dashboard</div>
)

beforeEach(() => {
  cleanup()
  clearPaneRegistry()
  registerPaneRenderer('session', { component: MockSessionRenderer })
  registerPaneRenderer('dashboard', { component: MockDashboardRenderer })
})

const sessionTab: Tab = {
  ...createTab({ kind: 'session', sessionCode: 'dev001', mode: 'terminal' }),
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
})

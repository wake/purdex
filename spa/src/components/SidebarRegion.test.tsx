import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SidebarRegion } from './SidebarRegion'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { List } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'

const DummyView = ({ isActive }: { isActive: boolean }) => (
  <div data-testid="dummy-view">{isActive ? 'active' : 'inactive'}</div>
)

function registerTestModule(id = 'test', viewId = 'test-view') {
  registerModule({
    id,
    name: 'Test',
    views: [{
      id: viewId,
      label: 'Test View',
      icon: List,
      scope: 'system',
      component: DummyView,
    }],
  })
}

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  clearModuleRegistry()
})

describe('SidebarRegion', () => {
  it('renders collapsed bar with plus button when collapsed and no views', () => {
    const { container } = render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    // Empty collapsed region still renders (with add-view-button), it no longer returns null
    expect(container.innerHTML).not.toBe('')
    expect(screen.getByTestId('add-view-button')).toBeInTheDocument()
  })

  it('renders collapsed bar when collapsed with views', () => {
    registerTestModule()
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')

    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    expect(screen.getByTestId('collapsed-bar')).toBeDefined()
  })

  it('renders Phosphor Icon in collapsed bar instead of text', () => {
    registerTestModule()
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])

    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    const bar = screen.getByTestId('collapsed-bar')
    // Should contain an SVG (Phosphor Icon), not a text character
    expect(bar.querySelector('svg')).toBeTruthy()
  })

  it('renders expanded view when pinned', () => {
    registerTestModule()
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')

    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    expect(screen.getByTestId('dummy-view')).toBeDefined()
    expect(screen.getByText('active')).toBeDefined()
  })

  it('falls back to first view when activeViewId is unset', () => {
    registerTestModule()
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    // Do NOT set activeView
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')

    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    expect(screen.getByTestId('dummy-view')).toBeDefined()
    expect(screen.getByText('active')).toBeDefined()
  })

  it('toggles region mode on collapsed bar click', () => {
    registerTestModule()
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')

    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    fireEvent.click(screen.getByTestId('collapsed-bar'))

    expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
  })

  it('has a collapse button in expanded state', () => {
    registerTestModule()
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')

    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    const collapseBtn = screen.getByTestId('collapse-button')
    expect(collapseBtn).toBeDefined()

    fireEvent.click(collapseBtn)
    expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('collapsed')
  })

  it('renders empty pinned region with gear button', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', [])
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    expect(screen.getByTestId('manage-button')).toBeInTheDocument()
    expect(screen.getByText(/加入 views/i)).toBeInTheDocument()
  })

  it('renders collapsed empty region with plus button', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', [])
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'collapsed')
    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    expect(screen.getByTestId('add-view-button')).toBeInTheDocument()
  })

  it('clicking plus on collapsed bar expands and opens manage mode', () => {
    registerTestModule()
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'collapsed')
    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    fireEvent.click(screen.getByTestId('add-view-button'))
    expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
    expect(screen.getByTestId('region-manager')).toBeInTheDocument()
  })

  it('returns null when hidden regardless of views', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'hidden')
    const { container } = render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    expect(container.firstChild).toBeNull()
  })
})

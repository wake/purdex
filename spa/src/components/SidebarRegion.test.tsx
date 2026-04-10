import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SidebarRegion } from './SidebarRegion'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { List } from '@phosphor-icons/react'

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
  clearModuleRegistry()
})

describe('SidebarRegion', () => {
  it('renders nothing when collapsed and no views', () => {
    const { container } = render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    expect(container.innerHTML).toBe('')
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
})

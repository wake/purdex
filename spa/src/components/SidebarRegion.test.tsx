import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SidebarRegion } from './SidebarRegion'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

const DummyView = ({ isActive }: { isActive: boolean }) => (
  <div data-testid="dummy-view">{isActive ? 'active' : 'inactive'}</div>
)

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
  clearModuleRegistry()
})

describe('SidebarRegion', () => {
  it('renders nothing when collapsed and no views', () => {
    const { container } = render(<SidebarRegion region="primary-sidebar" side="right" />)
    expect(container.innerHTML).toBe('')
  })

  it('renders collapsed bar when collapsed with views', () => {
    registerModule({
      id: 'test',
      name: 'Test',
      views: [{
        id: 'test-view',
        label: 'Test View',
        icon: 'List',
        scope: 'system',
        defaultRegion: 'primary-sidebar',
        component: DummyView,
      }],
    })
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')

    render(<SidebarRegion region="primary-sidebar" side="right" />)
    expect(screen.getByTestId('collapsed-bar')).toBeDefined()
  })

  it('renders expanded view when pinned', () => {
    registerModule({
      id: 'test',
      name: 'Test',
      views: [{
        id: 'test-view',
        label: 'Test View',
        icon: 'List',
        scope: 'system',
        defaultRegion: 'primary-sidebar',
        component: DummyView,
      }],
    })
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')

    render(<SidebarRegion region="primary-sidebar" side="right" />)
    expect(screen.getByTestId('dummy-view')).toBeDefined()
    expect(screen.getByText('active')).toBeDefined()
  })

  it('toggles region mode on collapsed bar click', () => {
    registerModule({
      id: 'test',
      name: 'Test',
      views: [{
        id: 'test-view',
        label: 'Test View',
        icon: 'List',
        scope: 'system',
        defaultRegion: 'primary-sidebar',
        component: DummyView,
      }],
    })
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')

    render(<SidebarRegion region="primary-sidebar" side="right" />)
    fireEvent.click(screen.getByTestId('collapsed-bar'))

    expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
  })
})

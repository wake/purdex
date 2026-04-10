import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { RegionManager } from './RegionManager'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

const DummyIcon = ({ size }: { size?: number }) => <span data-testid="icon">{size}</span>
const DummyView = () => <div>view</div>

beforeEach(() => {
  clearModuleRegistry()
  useLayoutStore.setState(useLayoutStore.getInitialState())
  registerModule({
    id: 'mod-a', name: 'Module A',
    views: [
      { id: 'view-a', label: 'View A', icon: DummyIcon, scope: 'system', component: DummyView },
      { id: 'view-b', label: 'View B', icon: DummyIcon, scope: 'workspace', component: DummyView },
    ],
  })
  registerModule({
    id: 'mod-b', name: 'Module B',
    views: [{ id: 'view-c', label: 'View C', icon: DummyIcon, scope: 'tab', component: DummyView }],
  })
})

describe('RegionManager', () => {
  it('shows enabled views and available views', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    render(<RegionManager region="primary-sidebar" />)
    expect(screen.getByText('View A')).toBeInTheDocument()
    expect(screen.getByText('View B')).toBeInTheDocument()
    expect(screen.getByText('View C')).toBeInTheDocument()
  })
  it('adds a view when clicking add button', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    render(<RegionManager region="primary-sidebar" />)
    const addButtons = screen.getAllByTestId('add-view-btn')
    fireEvent.click(addButtons[0])
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toContain('view-b')
  })
  it('removes a view when clicking remove button', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    render(<RegionManager region="primary-sidebar" />)
    const removeButtons = screen.getAllByTestId('remove-view-btn')
    fireEvent.click(removeButtons[0])
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).not.toContain('view-a')
  })
  it('shows all views as available when region is empty', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', [])
    render(<RegionManager region="primary-sidebar" />)
    const addButtons = screen.getAllByTestId('add-view-btn')
    expect(addButtons).toHaveLength(3)
  })
})

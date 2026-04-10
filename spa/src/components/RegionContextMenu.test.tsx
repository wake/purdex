import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { RegionContextMenu } from './RegionContextMenu'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

const DummyIcon = ({ size }: { size?: number }) => <span>{size}</span>
const DummyView = () => <div>view</div>

beforeEach(() => {
  clearModuleRegistry()
  useLayoutStore.setState(useLayoutStore.getInitialState())
  registerModule({
    id: 'mod-a', name: 'A',
    views: [
      { id: 'view-a', label: 'View A', icon: DummyIcon, scope: 'system', component: DummyView },
      { id: 'view-b', label: 'View B', icon: DummyIcon, scope: 'workspace', component: DummyView },
    ],
  })
})

describe('RegionContextMenu', () => {
  it('shows all views with enabled ones checked', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    render(<RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={() => {}} />)
    expect(screen.getByText('View A')).toBeInTheDocument()
    expect(screen.getByText('View B')).toBeInTheDocument()
  })
  it('adds view when clicking unchecked item', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    render(<RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={() => {}} />)
    fireEvent.click(screen.getByText('View B'))
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toContain('view-b')
  })
  it('removes view when clicking checked item', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    render(<RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={() => {}} />)
    fireEvent.click(screen.getByText('View A'))
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).not.toContain('view-a')
  })
  it('shows enabled views first in region order, then available in registry order', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-b'])
    render(<RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={() => {}} />)
    const items = screen.getAllByRole('button')
    const labels = items.map((el) => el.textContent).filter(Boolean)
    expect(labels.indexOf('View B')).toBeLessThan(labels.indexOf('View A'))
  })
  it('calls onClose when clicking outside', () => {
    const onClose = vi.fn()
    render(<RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={onClose} />)
    fireEvent.mouseDown(document.body)
    expect(onClose).toHaveBeenCalled()
  })
})

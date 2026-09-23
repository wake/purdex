import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { RegionManager } from './RegionManager'
import { useLayoutStore } from '../stores/useLayoutStore'
import { useI18nStore } from '../stores/useI18nStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

const DummyIcon = ({ size }: { size?: number }) => <span data-testid="icon">{size}</span>
const DummyView = () => <div>view</div>

beforeEach(() => {
  clearModuleRegistry()
  useLayoutStore.setState(useLayoutStore.getInitialState())
  useI18nStore.getState().setLocale('en')
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

  describe('locale-aware empty state (#1324)', () => {
    it('shows the English empty-state text for the en locale', () => {
      clearModuleRegistry()
      useLayoutStore.getState().setRegionViews('primary-sidebar', [])
      render(<RegionManager region="primary-sidebar" />)
      expect(screen.getByText('No views available')).toBeInTheDocument()
      expect(screen.queryByText('沒有可用的檢視')).not.toBeInTheDocument()
    })

    it('shows the zh-TW empty-state text for the zh-TW locale', () => {
      clearModuleRegistry()
      useI18nStore.getState().setLocale('zh-TW')
      useLayoutStore.getState().setRegionViews('primary-sidebar', [])
      render(<RegionManager region="primary-sidebar" />)
      expect(screen.getByText('沒有可用的檢視')).toBeInTheDocument()
    })
  })

  describe('locale-aware section headings (#1324)', () => {
    it('shows the English section headings for the en locale', () => {
      useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
      render(<RegionManager region="primary-sidebar" />)
      expect(screen.getByText('Enabled')).toBeInTheDocument()
      expect(screen.getByText('Available')).toBeInTheDocument()
      expect(screen.queryByText('已啟用')).not.toBeInTheDocument()
      expect(screen.queryByText('可加入')).not.toBeInTheDocument()
    })

    it('shows the zh-TW section headings for the zh-TW locale', () => {
      useI18nStore.getState().setLocale('zh-TW')
      useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
      render(<RegionManager region="primary-sidebar" />)
      expect(screen.getByText('已啟用')).toBeInTheDocument()
      expect(screen.getByText('可加入')).toBeInTheDocument()
    })
  })
})

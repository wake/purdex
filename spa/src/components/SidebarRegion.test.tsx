import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { SidebarRegion } from './SidebarRegion'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { Lightning, List } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import { useHostStore } from '../stores/useHostStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { ExecutionsView } from './executions/ExecutionsView'

vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn().mockResolvedValue({ items: [], next_cursor: '' }) }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn(() => ({ close: vi.fn() })) }))

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
  useI18nStore.getState().setLocale('en')
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

  it('renders empty pinned region with gear button (zh-TW locale)', () => {
    useI18nStore.getState().setLocale('zh-TW')
    useLayoutStore.getState().setRegionViews('primary-sidebar', [])
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
    render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
    expect(screen.getByTestId('manage-button')).toBeInTheDocument()
    expect(screen.getByText('加入 views')).toBeInTheDocument()
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

  describe('Executions view', () => {
    function registerExecutionsView() {
      registerModule({
        id: 'execution',
        name: 'Execution',
        views: [{ id: 'executions', label: 'Executions', icon: Lightning, scope: 'system', component: ExecutionsView }],
      })
    }

    beforeEach(() => {
      useHostStore.setState({
        hosts: {
          'host-a': { id: 'host-a', name: 'Mini Lab', ip: '1', port: 1, token: 't', order: 0 },
          'host-b': { id: 'host-b', name: 'Air', ip: '2', port: 2, token: 't', order: 1 },
        },
        hostOrder: ['host-a', 'host-b'], activeHostId: 'host-b', runtime: {},
      })
      useNexHostStore.setState({ byHost: {}, ensure: vi.fn().mockResolvedValue(undefined) })
    })

    it('region without the view configured renders no Executions', () => {
      registerExecutionsView()
      registerTestModule()
      useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
      expect(screen.getByTestId('dummy-view')).toBeInTheDocument()
      expect(screen.queryByTestId('executions-view')).toBeNull()
      expect(screen.queryByText('Executions')).toBeNull()
    })

    it('configured → renders with the active host id', () => {
      registerExecutionsView()
      useLayoutStore.getState().setRegionViews('primary-sidebar', ['executions'])
      useLayoutStore.getState().setActiveView('primary-sidebar', 'executions')
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
      expect(screen.getByTestId('executions-view')).toBeInTheDocument()
      expect(within(screen.getByTestId('executions-header')).getByText('Air')).toBeInTheDocument()
      expect(useNexHostStore.getState().ensure).toHaveBeenCalledWith('host-b')
    })
  })

  describe('locale-aware labels (#1324)', () => {
    it('collapsed add-view button title is English for the en locale', () => {
      render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
      const btn = screen.getByTestId('add-view-button')
      expect(btn).toHaveAttribute('title', 'Manage views')
      expect(btn.getAttribute('title')).not.toBe('管理 views')
    })

    it('collapsed add-view button title is zh-TW for the zh-TW locale', () => {
      useI18nStore.getState().setLocale('zh-TW')
      render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
      expect(screen.getByTestId('add-view-button')).toHaveAttribute('title', '管理 views')
    })

    it('expanded manage button title is English for the en locale', () => {
      registerTestModule()
      useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
      useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')

      render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
      expect(screen.getByTestId('manage-button')).toHaveAttribute('title', 'Manage views')
    })

    it('expanded manage button title is zh-TW for the zh-TW locale', () => {
      registerTestModule()
      useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
      useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      useI18nStore.getState().setLocale('zh-TW')

      render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
      expect(screen.getByTestId('manage-button')).toHaveAttribute('title', '管理 views')
    })

    it('empty pinned region shows the English empty-state text for the en locale', () => {
      useLayoutStore.getState().setRegionViews('primary-sidebar', [])
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
      expect(screen.getByText('Add views')).toBeInTheDocument()
      expect(screen.queryByText('加入 views')).not.toBeInTheDocument()
    })

    it('empty pinned region shows the zh-TW empty-state text for the zh-TW locale', () => {
      useI18nStore.getState().setLocale('zh-TW')
      useLayoutStore.getState().setRegionViews('primary-sidebar', [])
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
      expect(screen.getByText('加入 views')).toBeInTheDocument()
    })
  })
})

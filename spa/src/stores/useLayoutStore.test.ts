import { describe, it, expect, beforeEach } from 'vitest'
import { useLayoutStore } from './useLayoutStore'
import type { SidebarRegion } from '../types/layout'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
  clearModuleRegistry()
})

describe('useLayoutStore', () => {
  describe('initial state', () => {
    it('has 4 regions with default values', () => {
      const { regions } = useLayoutStore.getState()
      const regionIds: SidebarRegion[] = [
        'primary-sidebar',
        'primary-panel',
        'secondary-panel',
        'secondary-sidebar',
      ]
      for (const id of regionIds) {
        expect(regions[id]).toBeDefined()
        expect(regions[id].views).toEqual([])
        expect(regions[id].activeViewId).toBeUndefined()
        expect(regions[id].mode).toBe('collapsed')
      }
    })

    it('sidebars default to 240px, panels to 200px', () => {
      const { regions } = useLayoutStore.getState()
      expect(regions['primary-sidebar'].width).toBe(240)
      expect(regions['secondary-sidebar'].width).toBe(240)
      expect(regions['primary-panel'].width).toBe(200)
      expect(regions['secondary-panel'].width).toBe(200)
    })

    it('layout mode defaults: width=narrow, tabPosition=top, wideSize=240', () => {
      const state = useLayoutStore.getState()
      expect(state.activityBarWidth).toBe('narrow')
      expect(state.tabPosition).toBe('top')
      expect(state.activityBarWideSize).toBe(240)
      expect(state.workspaceExpanded).toEqual({})
    })
  })

  describe('setRegionMode', () => {
    it('changes mode for a region', () => {
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
    })
  })

  describe('setActivityBarWidth', () => {
    it('narrow → wide', () => {
      useLayoutStore.getState().setActivityBarWidth('wide')
      expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    })

    it('wide → narrow', () => {
      useLayoutStore.setState({ activityBarWidth: 'wide' })
      useLayoutStore.getState().setActivityBarWidth('narrow')
      expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
    })

    it('refuses narrow when tabPosition=left', () => {
      useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
      useLayoutStore.getState().setActivityBarWidth('narrow')
      expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    })

    it('allows wide when tabPosition=left', () => {
      useLayoutStore.setState({ activityBarWidth: 'narrow', tabPosition: 'left' })
      useLayoutStore.getState().setActivityBarWidth('wide')
      expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    })
  })

  describe('toggleActivityBarWidth', () => {
    it('toggles narrow ↔ wide', () => {
      useLayoutStore.getState().toggleActivityBarWidth()
      expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
      useLayoutStore.getState().toggleActivityBarWidth()
      expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
    })

    it('no-op when currently wide and tabPosition=left', () => {
      useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
      useLayoutStore.getState().toggleActivityBarWidth()
      expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    })
  })

  describe('setActivityBarWideSize', () => {
    it('updates value', () => {
      useLayoutStore.getState().setActivityBarWideSize(300)
      expect(useLayoutStore.getState().activityBarWideSize).toBe(300)
    })

    it('clamps below 120', () => {
      useLayoutStore.getState().setActivityBarWideSize(50)
      expect(useLayoutStore.getState().activityBarWideSize).toBe(120)
    })

    it('clamps above 600', () => {
      useLayoutStore.getState().setActivityBarWideSize(800)
      expect(useLayoutStore.getState().activityBarWideSize).toBe(600)
    })
  })

  describe('setRegionWidth', () => {
    it('changes width for a region', () => {
      useLayoutStore.getState().setRegionWidth('primary-sidebar', 300)
      expect(useLayoutStore.getState().regions['primary-sidebar'].width).toBe(300)
    })

    it('enforces minimum width of 120', () => {
      useLayoutStore.getState().setRegionWidth('primary-sidebar', 50)
      expect(useLayoutStore.getState().regions['primary-sidebar'].width).toBe(120)
    })

    it('enforces maximum width of 600', () => {
      useLayoutStore.getState().setRegionWidth('primary-sidebar', 800)
      expect(useLayoutStore.getState().regions['primary-sidebar'].width).toBe(600)
    })
  })

  describe('setActiveView', () => {
    it('sets the active view for a region', () => {
      useLayoutStore.getState().setActiveView('primary-sidebar', 'session-list')
      expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('session-list')
    })

    it('clears active view with undefined', () => {
      useLayoutStore.getState().setActiveView('primary-sidebar', 'session-list')
      useLayoutStore.getState().setActiveView('primary-sidebar', undefined)
      expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBeUndefined()
    })
  })

  describe('setRegionViews', () => {
    it('sets the view list for a region', () => {
      useLayoutStore.getState().setRegionViews('primary-sidebar', ['session-list', 'prompts'])
      expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['session-list', 'prompts'])
    })
  })

  describe('toggleRegion', () => {
    it('cycles collapsed → pinned → collapsed', () => {
      const store = useLayoutStore.getState()
      expect(store.regions['primary-sidebar'].mode).toBe('collapsed')

      store.toggleRegion('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')

      useLayoutStore.getState().toggleRegion('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('collapsed')
    })
  })

  describe('toggleVisibility', () => {
    it('hides a pinned region and remembers previousMode', () => {
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      useLayoutStore.getState().toggleVisibility('primary-sidebar')
      const region = useLayoutStore.getState().regions['primary-sidebar']
      expect(region.mode).toBe('hidden')
      expect(region.previousMode).toBe('pinned')
    })

    it('hides a collapsed region and remembers previousMode', () => {
      // default is collapsed
      useLayoutStore.getState().toggleVisibility('primary-sidebar')
      const region = useLayoutStore.getState().regions['primary-sidebar']
      expect(region.mode).toBe('hidden')
      expect(region.previousMode).toBe('collapsed')
    })

    it('restores to previousMode when unhiding', () => {
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'collapsed')
      useLayoutStore.getState().toggleVisibility('primary-sidebar') // hide
      useLayoutStore.getState().toggleVisibility('primary-sidebar') // restore
      const region = useLayoutStore.getState().regions['primary-sidebar']
      expect(region.mode).toBe('collapsed')
      expect(region.previousMode).toBeUndefined()
    })

    it('defaults to pinned when no previousMode', () => {
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'hidden')
      useLayoutStore.getState().toggleVisibility('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
    })
  })
})

describe('addView', () => {
  it('appends a view to the region', () => {
    useLayoutStore.getState().addView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-a'])
  })
  it('appends to existing views', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    useLayoutStore.getState().addView('primary-sidebar', 'view-b')
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-a', 'view-b'])
  })
  it('ignores duplicate view', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    useLayoutStore.getState().addView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-a'])
  })
})

describe('removeView', () => {
  it('removes a view from the region', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    useLayoutStore.getState().removeView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-b'])
  })
  it('resets activeViewId to first when active view is removed', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'view-a')
    useLayoutStore.getState().removeView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('view-b')
  })
  it('sets activeViewId to undefined when last view removed', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'view-a')
    useLayoutStore.getState().removeView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBeUndefined()
  })
  it('does not change activeViewId when non-active view is removed', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'view-b')
    useLayoutStore.getState().removeView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('view-b')
  })
})

describe('reorderViews', () => {
  it('reorders views', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['a', 'b', 'c'])
    useLayoutStore.getState().reorderViews('primary-sidebar', ['c', 'a', 'b'])
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['c', 'a', 'b'])
  })
  it('discards extra ids and appends missing ids', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['a', 'b', 'c'])
    useLayoutStore.getState().reorderViews('primary-sidebar', ['b', 'x'])
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['b', 'a', 'c'])
  })
})

describe('reconcileViews', () => {
  it('removes stale view IDs', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['valid-view', 'stale-view'])
    registerModule({
      id: 'test-mod',
      name: 'Test',
      views: [{
        id: 'valid-view',
        label: 'Valid',
        icon: () => null,
        scope: 'workspace',
        component: () => null,
      }],
    })
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['valid-view'])
  })

  it('fixes activeViewId when stale', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['valid-view', 'stale-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'stale-view')
    registerModule({
      id: 'test-mod',
      name: 'Test',
      views: [{
        id: 'valid-view',
        label: 'Valid',
        icon: () => null,
        scope: 'workspace',
        component: () => null,
      }],
    })
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('valid-view')
  })

  it('sets defaults when all regions empty', () => {
    registerModule({
      id: 'test-mod',
      name: 'Test',
      views: [{
        id: 'file-tree-workspace',
        label: 'File Tree',
        icon: () => null,
        scope: 'workspace',
        component: () => null,
      }],
    })
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['file-tree-workspace'])
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('file-tree-workspace')
  })

  it('preserves valid views unchanged', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    registerModule({
      id: 'test-mod',
      name: 'Test',
      views: [
        {
          id: 'view-a',
          label: 'A',
          icon: () => null,
          scope: 'workspace',
          component: () => null,
        },
        {
          id: 'view-b',
          label: 'B',
          icon: () => null,
          scope: 'workspace',
          component: () => null,
        },
      ],
    })
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-a', 'view-b'])
  })

  it('preserves valid activeViewId', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'view-b')
    registerModule({
      id: 'test-mod',
      name: 'Test',
      views: [
        {
          id: 'view-a',
          label: 'A',
          icon: () => null,
          scope: 'workspace',
          component: () => null,
        },
        {
          id: 'view-b',
          label: 'B',
          icon: () => null,
          scope: 'workspace',
          component: () => null,
        },
      ],
    })
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('view-b')
  })

  it('no-ops when module registry is empty', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['some-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'some-view')
    // No registerModule — registry is empty
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['some-view'])
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('some-view')
  })

  it('keeps allEmpty when file-tree-workspace is not registered', () => {
    registerModule({
      id: 'test-mod',
      name: 'Test',
      views: [{
        id: 'other-view',
        label: 'Other',
        icon: () => null,
        scope: 'workspace',
        component: () => null,
      }],
    })
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual([])
  })

  it('removes stale views from non-primary-sidebar regions', () => {
    useLayoutStore.getState().setRegionViews('secondary-sidebar', ['valid-view', 'stale-view'])
    registerModule({
      id: 'test-mod',
      name: 'Test',
      views: [{
        id: 'valid-view',
        label: 'Valid',
        icon: () => null,
        scope: 'workspace',
        component: () => null,
      }],
    })
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['secondary-sidebar'].views).toEqual(['valid-view'])
  })

  it('preserves undefined activeViewId when no stale views', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    // activeViewId is undefined by default
    registerModule({
      id: 'test-mod',
      name: 'Test',
      views: [{
        id: 'view-a',
        label: 'A',
        icon: () => null,
        scope: 'workspace',
        component: () => null,
      }],
    })
    useLayoutStore.getState().reconcileViews()
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBeUndefined()
  })
})

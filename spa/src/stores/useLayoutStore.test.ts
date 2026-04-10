import { describe, it, expect, beforeEach } from 'vitest'
import { useLayoutStore } from './useLayoutStore'
import type { SidebarRegion } from '../types/tab'

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
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
  })

  describe('setRegionMode', () => {
    it('changes mode for a region', () => {
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
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
    it('toggles between hidden and pinned', () => {
      // Start from collapsed, toggle visibility → hidden
      useLayoutStore.getState().toggleVisibility('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('hidden')

      // Toggle again → pinned
      useLayoutStore.getState().toggleVisibility('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')

      // Toggle again → hidden
      useLayoutStore.getState().toggleVisibility('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('hidden')
    })

    it('hides a pinned region', () => {
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      useLayoutStore.getState().toggleVisibility('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('hidden')
    })
  })
})

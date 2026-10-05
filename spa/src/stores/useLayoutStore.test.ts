import { describe, it, expect, beforeEach } from 'vitest'
import {
  useLayoutStore,
  healLayoutInvariant,
  WORKER_LIST_MIN,
  WORKER_LIST_MAX,
  WORKER_LIST_DEFAULT,
} from './useLayoutStore'
import { STORAGE_KEYS } from '../lib/storage'
import { PROJECTIONS } from '../lib/profile/projections'

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

describe('useLayoutStore', () => {
  describe('initial state', () => {
    it('layout mode defaults: width=narrow, tabPosition=top, wideSize=240', () => {
      const state = useLayoutStore.getState()
      expect(state.activityBarWidth).toBe('narrow')
      expect(state.tabPosition).toBe('top')
      expect(state.activityBarWideSize).toBe(240)
      expect(state.workspaceExpanded).toEqual({})
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

    it('allows narrow when tabPosition=both (top tabs still available)', () => {
      useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'both' })
      useLayoutStore.getState().setActivityBarWidth('narrow')
      expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
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

    it('toggles wide → narrow when tabPosition=both', () => {
      useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'both' })
      useLayoutStore.getState().toggleActivityBarWidth()
      expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
    })
  })

  describe('setTabPosition', () => {
    it('sets to left and forces activityBarWidth=wide', () => {
      useLayoutStore.setState({ activityBarWidth: 'narrow', tabPosition: 'top' })
      useLayoutStore.getState().setTabPosition('left')
      expect(useLayoutStore.getState().tabPosition).toBe('left')
      expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    })

    it('sets to top without changing activityBarWidth (wide stays)', () => {
      useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
      useLayoutStore.getState().setTabPosition('top')
      expect(useLayoutStore.getState().tabPosition).toBe('top')
      expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    })

    it('sets to top without changing activityBarWidth (narrow stays narrow)', () => {
      useLayoutStore.setState({ activityBarWidth: 'narrow', tabPosition: 'top' })
      useLayoutStore.getState().setTabPosition('top')
      expect(useLayoutStore.getState().tabPosition).toBe('top')
      expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
    })

    it('sets to both preserving current activityBarWidth (narrow stays narrow)', () => {
      useLayoutStore.setState({ activityBarWidth: 'narrow', tabPosition: 'top' })
      useLayoutStore.getState().setTabPosition('both')
      expect(useLayoutStore.getState().tabPosition).toBe('both')
      expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
    })

    it('sets to both preserving current activityBarWidth (wide stays wide)', () => {
      useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
      useLayoutStore.getState().setTabPosition('both')
      expect(useLayoutStore.getState().tabPosition).toBe('both')
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

  describe('toggleWorkspaceExpanded', () => {
    it('toggles from undefined → true', () => {
      useLayoutStore.getState().toggleWorkspaceExpanded('ws-1')
      expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(true)
    })

    it('toggles from true → false', () => {
      useLayoutStore.setState({ workspaceExpanded: { 'ws-1': true } })
      useLayoutStore.getState().toggleWorkspaceExpanded('ws-1')
      expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(false)
    })

    it('per-ws isolation', () => {
      useLayoutStore.getState().toggleWorkspaceExpanded('ws-1')
      useLayoutStore.getState().toggleWorkspaceExpanded('ws-2')
      expect(useLayoutStore.getState().workspaceExpanded).toEqual({
        'ws-1': true,
        'ws-2': true,
      })
    })

    it('supports "home" key', () => {
      useLayoutStore.getState().toggleWorkspaceExpanded('home')
      expect(useLayoutStore.getState().workspaceExpanded['home']).toBe(true)
    })
  })

  describe('reconcileWorkspaceExpanded', () => {
    it('prunes keys not in provided ws list, preserves "home"', () => {
      useLayoutStore.setState({
        workspaceExpanded: {
          'ws-alive': true,
          'ws-deleted': true,
          home: true,
        },
      })
      useLayoutStore.getState().reconcileWorkspaceExpanded(['ws-alive'])
      expect(useLayoutStore.getState().workspaceExpanded).toEqual({
        'ws-alive': true,
        home: true,
      })
    })

    it('is no-op when all keys are alive or "home"', () => {
      useLayoutStore.setState({
        workspaceExpanded: { 'ws-a': true, home: false },
      })
      const before = useLayoutStore.getState().workspaceExpanded
      useLayoutStore.getState().reconcileWorkspaceExpanded(['ws-a'])
      expect(useLayoutStore.getState().workspaceExpanded).toEqual(before)
    })
  })

  describe('healLayoutInvariant', () => {
    it('forces width=wide when state has {narrow, left}', () => {
      const healed = healLayoutInvariant({ activityBarWidth: 'narrow', tabPosition: 'left' })
      expect(healed.activityBarWidth).toBe('wide')
    })

    it('leaves {narrow, both} untouched (top tabs keep tabs reachable)', () => {
      const healed = healLayoutInvariant({ activityBarWidth: 'narrow', tabPosition: 'both' })
      expect(healed.activityBarWidth).toBe('narrow')
    })

    it('leaves valid combinations untouched', () => {
      expect(healLayoutInvariant({ activityBarWidth: 'narrow', tabPosition: 'top' }).activityBarWidth).toBe('narrow')
      expect(healLayoutInvariant({ activityBarWidth: 'wide', tabPosition: 'top' }).activityBarWidth).toBe('wide')
      expect(healLayoutInvariant({ activityBarWidth: 'wide', tabPosition: 'left' }).activityBarWidth).toBe('wide')
      expect(healLayoutInvariant({ activityBarWidth: 'wide', tabPosition: 'both' }).activityBarWidth).toBe('wide')
    })
  })

  describe('worker list and compact bottom group', () => {
    it('defaults: list closed, height 240, bottom group not compact', () => {
      const state = useLayoutStore.getState()
      expect(state.workerListOpen).toBe(false)
      expect(state.workerListHeight).toBe(WORKER_LIST_DEFAULT)
      expect(WORKER_LIST_DEFAULT).toBe(240)
      expect(state.bottomNavCompact).toBe(false)
    })

    it('setWorkerListOpen sets the flag', () => {
      useLayoutStore.getState().setWorkerListOpen(true)
      expect(useLayoutStore.getState().workerListOpen).toBe(true)
      useLayoutStore.getState().setWorkerListOpen(false)
      expect(useLayoutStore.getState().workerListOpen).toBe(false)
    })

    it('toggleWorkerListOpen flips the flag', () => {
      useLayoutStore.getState().toggleWorkerListOpen()
      expect(useLayoutStore.getState().workerListOpen).toBe(true)
      useLayoutStore.getState().toggleWorkerListOpen()
      expect(useLayoutStore.getState().workerListOpen).toBe(false)
    })

    it('setWorkerListHeight stores an in-range value', () => {
      useLayoutStore.getState().setWorkerListHeight(321)
      expect(useLayoutStore.getState().workerListHeight).toBe(321)
    })

    it('setWorkerListHeight clamps below WORKER_LIST_MIN (96)', () => {
      expect(WORKER_LIST_MIN).toBe(96)
      useLayoutStore.getState().setWorkerListHeight(10)
      expect(useLayoutStore.getState().workerListHeight).toBe(96)
    })

    it('setWorkerListHeight clamps above WORKER_LIST_MAX (800)', () => {
      expect(WORKER_LIST_MAX).toBe(800)
      useLayoutStore.getState().setWorkerListHeight(5000)
      expect(useLayoutStore.getState().workerListHeight).toBe(800)
    })

    it('setBottomNavCompact sets the flag', () => {
      useLayoutStore.getState().setBottomNavCompact(true)
      expect(useLayoutStore.getState().bottomNavCompact).toBe(true)
      useLayoutStore.getState().setBottomNavCompact(false)
      expect(useLayoutStore.getState().bottomNavCompact).toBe(false)
    })

    it('toggleBottomNavCompact flips the flag', () => {
      useLayoutStore.getState().toggleBottomNavCompact()
      expect(useLayoutStore.getState().bottomNavCompact).toBe(true)
      useLayoutStore.getState().toggleBottomNavCompact()
      expect(useLayoutStore.getState().bottomNavCompact).toBe(false)
    })

    it('partialize persists the three fields', () => {
      useLayoutStore.setState({ workerListOpen: true, workerListHeight: 333, bottomNavCompact: true })
      const partialize = useLayoutStore.persist.getOptions().partialize!
      expect(partialize(useLayoutStore.getState())).toMatchObject({
        workerListOpen: true,
        workerListHeight: 333,
        bottomNavCompact: true,
      })
    })

    it('Profile Sync still projects only tabPosition from the layout store', () => {
      // The new fields are device-local: they must not join PROJECTIONS (spec §4.1).
      const prefix = `${STORAGE_KEYS.LAYOUT}.`
      const layoutPaths = Object.values(PROJECTIONS)
        .flat()
        .filter((path) => path.startsWith(prefix) || path.startsWith(`!${prefix}`))
      expect(layoutPaths).toEqual(['purdex-layout.tabPosition'])
    })
  })

  describe('a stored payload from before the regions were removed', () => {
    const KEY = STORAGE_KEYS.LAYOUT

    beforeEach(() => localStorage.removeItem(KEY))

    it('hydrates without throwing and drops regions on the next write', async () => {
      localStorage.setItem(KEY, JSON.stringify({
        state: {
          regions: {
            'primary-sidebar': { views: ['file-tree-workspace'], activeViewId: 'file-tree-workspace', width: 240, mode: 'pinned' },
            'primary-panel': { views: [], width: 200, mode: 'collapsed' },
            'secondary-panel': { views: [], width: 200, mode: 'hidden', previousMode: 'collapsed' },
            'secondary-sidebar': { views: ['executions'], width: 240, mode: 'collapsed' },
          },
          activityBarWidth: 'wide',
          tabPosition: 'top',
          activityBarWideSize: 300,
          workspaceExpanded: { home: true },
          workerListOpen: true,
          workerListHeight: 320,
          bottomNavCompact: true,
        },
        version: 1,
      }))

      await expect(useLayoutStore.persist.rehydrate()).resolves.not.toThrow()

      const state = useLayoutStore.getState()
      expect(state.activityBarWidth).toBe('wide')
      expect(state.activityBarWideSize).toBe(300)
      expect(state.workerListOpen).toBe(true)
      expect(state.workerListHeight).toBe(320)
      expect(state.bottomNavCompact).toBe(true)

      const partialize = useLayoutStore.persist.getOptions().partialize!
      expect(partialize(state)).not.toHaveProperty('regions')

      useLayoutStore.getState().setActivityBarWideSize(310)
      const written = JSON.parse(localStorage.getItem(KEY)!)
      expect(written.state).not.toHaveProperty('regions')
      expect(written.state.activityBarWideSize).toBe(310)
    })
  })
})

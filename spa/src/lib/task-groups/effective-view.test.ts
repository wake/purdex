// spa/src/lib/task-groups/effective-view.test.ts — the one reader of which sidebar view is in force (task groups spec §3, TG-3a-2): the task-group view only when it is the stored view, the sidebar is wide and the
// tabs are not in the sidebar (`tabPosition` is not `left`).
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { effectiveView, effectiveViewOf, useEffectiveView } from './effective-view'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'

const set = (stored: 'workspace' | 'groups', width: 'narrow' | 'wide', tabPosition: 'top' | 'left' | 'both') => {
  useTeamUiStore.setState({ sidebarView: stored })
  useLayoutStore.setState({ activityBarWidth: width, tabPosition })
}

describe('effectiveViewOf', () => {
  it.each([
    ['groups', 'wide', 'top', 'groups'],
    ['groups', 'wide', 'both', 'groups'],
    ['groups', 'narrow', 'both', 'workspace'], // a narrow sidebar has no task-group view
    ['groups', 'narrow', 'top', 'workspace'],
    ['groups', 'wide', 'left', 'workspace'], // the tabs sit in the sidebar: workspace behaviour
    ['workspace', 'wide', 'top', 'workspace'], // the stored view is off
    ['workspace', 'wide', 'both', 'workspace'],
    ['workspace', 'narrow', 'left', 'workspace'],
  ] as const)('stored %s, %s sidebar, tabs %s → %s', (stored, width, pos, want) => {
    expect(effectiveViewOf(stored, width, pos)).toBe(want)
  })
})

describe('effectiveView / useEffectiveView read the stores', () => {
  beforeEach(() => set('workspace', 'wide', 'top'))

  it('the plain reader follows the stores', () => {
    expect(effectiveView()).toBe('workspace')
    set('groups', 'wide', 'both')
    expect(effectiveView()).toBe('groups')
    set('groups', 'wide', 'left')
    expect(effectiveView()).toBe('workspace')
  })

  it('the hook re-renders when any of the three moves', () => {
    set('groups', 'wide', 'both')
    const { result } = renderHook(() => useEffectiveView())
    expect(result.current).toBe('groups')
    act(() => { useLayoutStore.setState({ activityBarWidth: 'narrow' }) })
    expect(result.current).toBe('workspace')
    act(() => { useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' }) })
    expect(result.current).toBe('workspace')
    act(() => { useLayoutStore.setState({ tabPosition: 'top' }) })
    expect(result.current).toBe('groups')
    act(() => { useTeamUiStore.setState({ sidebarView: 'workspace' }) })
    expect(result.current).toBe('workspace')
  })
})

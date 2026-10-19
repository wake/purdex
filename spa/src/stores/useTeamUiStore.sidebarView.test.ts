// spa/src/stores/useTeamUiStore.sidebarView.test.ts — which view the sidebar shows on THIS device (name pool v2 spec §7, §9.6): the workspace list or the task groups. Device-local like the rest of
// the store (purdex-team-ui), never synced; a value that is neither is the workspace view.
import { describe, it, expect, beforeEach } from 'vitest'
import { useTeamUiStore } from './useTeamUiStore'

const get = () => useTeamUiStore.getState()

beforeEach(() => {
  localStorage.clear()
  useTeamUiStore.setState({ sidebarView: 'workspace' })
})

describe('sidebarView', () => {
  it('is the workspace view by default', () => {
    expect(get().sidebarView).toBe('workspace')
  })

  it('setSidebarView changes it and persists it under purdex-team-ui', () => {
    get().setSidebarView('groups')
    expect(get().sidebarView).toBe('groups')
    expect(JSON.parse(localStorage.getItem('purdex-team-ui')!).state.sidebarView).toBe('groups')
  })

  it('setting the same view again does not write', () => {
    const before = get()
    get().setSidebarView('workspace')
    expect(get()).toBe(before)
  })

  it('reads a saved one back, and a saved value that is neither view is the workspace view', async () => {
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { sidebarView: 'groups' }, version: 0 }))
    await useTeamUiStore.persist.rehydrate()
    expect(get().sidebarView).toBe('groups')
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { sidebarView: 'grid' }, version: 0 }))
    await useTeamUiStore.persist.rehydrate()
    expect(get().sidebarView).toBe('workspace')
  })
})

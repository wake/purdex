// spa/src/lib/open-session-tab.team.test.ts — the session list opens a team MEMBER inside its lead's group (spec R11); a
// lead and a non-team session open as they always did.
import { describe, it, expect, beforeEach } from 'vitest'
import { openSessionTab } from './open-session-tab'
import { KEY, resetTeamStores, seedScene, tabShowing, wsTabs } from './team/__tests__/team-fixture'
import { useTabStore } from '../stores/useTabStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { useSessionStore } from '../stores/useSessionStore'
import type { Session } from './host-api'

beforeEach(resetTeamStores)

const session = (name: string): Session => ({ code: `code-${name}`, name, mode: 'terminal', cwd: '~' }) as Session

const scene = () => seedScene({
  members: [['A', 'a-tm'], ['B', 'b-tm']],
  tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null], ['y', null]],
  workspaces: [{ id: 'w1', tabs: ['lead', 'x', 'ma', 'y'] }, { id: 'w2', tabs: [] }],
  activeWorkspaceId: 'w2',
})

describe('openSessionTab and teams', () => {
  it('session list opens a member into the group (after the group\'s last tab, in the lead\'s workspace)', () => {
    scene()
    const id = openSessionTab('h1', session('b-tm'))
    expect(id).not.toBeNull()
    expect(wsTabs('w1')).toEqual(['lead', 'x', 'ma', id, 'y'].filter((t) => t !== undefined)) // after ma, the group's last tab
    expect(wsTabs('w2')).toEqual([])
  })

  it('a member that already has a tab is switched to, never opened twice', () => {
    scene()
    const before = Object.keys(useTabStore.getState().tabs).length
    expect(openSessionTab('h1', session('a-tm'))).toBe('ma')
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(before)
    expect(useTabStore.getState().activeTabId).toBe('ma')
  })

  it('a collapsed group is expanded by it (R10 reaches the session list too)', () => {
    scene()
    useTeamUiStore.getState().setCollapsed(KEY, true)
    openSessionTab('h1', session('b-tm'))
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
  })

  it('session list opens a lead / non-team session as before: a new tab in the active workspace, even when one exists', () => {
    scene()
    const lead = openSessionTab('h1', session('lead-tm'))
    expect(lead).not.toBeNull()
    expect(lead).not.toBe('lead')
    expect(wsTabs('w2')).toEqual([lead])
    const plain = openSessionTab('h1', session('not-a-team'))
    expect(wsTabs('w2')).toEqual([lead, plain])
    expect(tabShowing('not-a-team')).toBe(plain)
  })

  it('with no team anywhere it is exactly today\'s behaviour', () => {
    resetTeamStores()
    const id = openSessionTab('h1', session('solo'))
    expect(id === null || useTabStore.getState().tabs[id] !== undefined).toBe(true)
  })
})

describe('a stale session of a recognised member', () => {
  it('opens no tab when the host no longer lists it (the session-list row was stale)', () => {
    scene()
    const before = Object.keys(useTabStore.getState().tabs)
    // the roster still names B, the host's list no longer holds it
    useSessionStore.setState({ sessions: { h1: [{ code: 'code-lead-tm', name: 'lead-tm', mode: 'terminal', cwd: '~' }] as never } })
    expect(openSessionTab('h1', session('b-tm'))).toBeNull()
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(before)
  })
})

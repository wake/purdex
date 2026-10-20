// spa/src/lib/task-groups/apply-reorder.test.ts — a reorder decision becomes store writes (TG-3e).
import { beforeEach, describe, expect, it } from 'vitest'
import { applyReorder } from './apply-reorder'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { createTab } from '../../types/tab'

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('applyReorder', () => {
  it('rank: sets every rank in one change', () => {
    const a = createTab({ kind: 'tmux-session', hostId: 'h', sessionCode: 'a', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    const b = createTab({ kind: 'tmux-session', hostId: 'h', sessionCode: 'b', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    useTabStore.getState().addTab(a)
    useTabStore.getState().addTab(b)
    applyReorder({ kind: 'rank', writes: [{ tabId: a.id, rank: 'F' }, { tabId: b.id, rank: 'M' }] })
    expect(useTabStore.getState().tabs[a.id].groupRank).toBe('F')
    expect(useTabStore.getState().tabs[b.id].groupRank).toBe('M')
  })

  it('members: writes this device\'s member order of the team', () => {
    applyReorder({ kind: 'members', teamKey: 'h\u0000t', order: ['B', 'A'] })
    expect(useTeamUiStore.getState().memberOrder['h\u0000t']).toEqual(['B', 'A'])
  })

  it('ignore: writes nothing', () => {
    const before = useTabStore.getState().tabs
    applyReorder({ kind: 'ignore' })
    expect(useTabStore.getState().tabs).toBe(before)
  })
})

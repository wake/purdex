// spa/src/lib/pool/submitter-tab.test.ts — the tab a job's submitter session sits in: found by the agent session id of a pane (the same source the tab's hourglass uses), the case of the id ignored; an empty session or one no tab holds is none.
import { describe, it, expect, beforeEach } from 'vitest'
import { findSubmitterTab } from './submitter-tab'
import { useTabStore } from '../../stores/useTabStore'
import type { PaneLayout, Tab } from '../../types/tab'

const tmux = (id: string, code: string, sessionId?: string): PaneLayout => ({
  type: 'leaf',
  pane: { id, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: '', ...(sessionId ? { rebuild: { sessionName: code, tmuxInstance: '', agent: { type: 'cc', sessionId, updatedAt: 1 }, capturedAt: 1 } } : {}) } },
})
const tab = (id: string, layout: PaneLayout): Tab => ({ id, layout } as unknown as Tab)
const seed = (...tabs: Tab[]) => useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])) } as never)

beforeEach(() => seed())

describe('findSubmitterTab', () => {
  it('finds the tab whose pane holds the session, and that pane', () => {
    seed(tab('t1', tmux('p1', 'a', 'sess-1')), tab('t2', tmux('p2', 'b', 'sess-2')))
    expect(findSubmitterTab('sess-2')).toEqual({ tabId: 't2', paneId: 'p2' })
  })

  it('ignores the case of the session id (the daemon and the pane may spell it differently)', () => {
    seed(tab('t1', tmux('p1', 'a', 'SESS-Abc')))
    expect(findSubmitterTab('sess-abc')).toEqual({ tabId: 't1', paneId: 'p1' })
    expect(findSubmitterTab('SESS-ABC')).toEqual({ tabId: 't1', paneId: 'p1' })
  })

  it('is the pane inside a split that holds the session, not the first pane of the tab', () => {
    const split: PaneLayout = { type: 'split', id: 's', direction: 'h', sizes: [50, 50], children: [tmux('p1', 'a', 'other'), tmux('p2', 'b', 'sess-1')] } as unknown as PaneLayout
    seed(tab('t1', split))
    expect(findSubmitterTab('sess-1')).toEqual({ tabId: 't1', paneId: 'p2' })
  })

  it('none for a session no tab holds, for a pane with no recorded session, and for an empty session', () => {
    seed(tab('t1', tmux('p1', 'a')), tab('t2', tmux('p2', 'b', 'sess-2')))
    expect(findSubmitterTab('sess-9')).toBeNull()
    expect(findSubmitterTab('')).toBeNull()
  })
})

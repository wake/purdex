// spa/src/lib/rebuild/reconcile-host.test.ts — a `sessions` payload against the panes on screen.
//
// The conversation rebuild pane (conversation entity spec §13.4, R-4-3) is a closed terminal pane with no session
// code of its own: the host reconciler skips every pane that is already `terminated`, so a payload for the host
// must leave its binding, its record and its conversation exactly as they were — including when the payload
// carries a session under the very name the pane would create, and when the pane has no generation to adopt into.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useTabStore } from '../../stores/useTabStore'
import { findPane } from '../pane-tree'
import type { Session } from '../host-api'
import type { ConversationRow } from '../nex/conversations-api'
import { conversationRebuildContent } from '../nex/open-conversation-rebuild'
import type { PaneContent, Tab, TmuxSessionContent } from '../../types/tab'
import { reconcileHostSessions } from './reconcile-host'

vi.mock('./cwd-probe', () => ({ probeMissingCwds: vi.fn(), probeSessionCwd: vi.fn(), resetCwdProbes: vi.fn() }))
vi.mock('./provenance-probe', () => ({ probeSessionProvenance: vi.fn(), resetProvenanceProbes: vi.fn() }))

const H = 'h1'
const OLD = '111:1000'
const NEW = '222:2000'
const row: ConversationRow = {
  session_id: 'aaaaaaaa-1111-2222-3333-444444444444', title: 'Fix the login bug', title_source: 'ai',
  cwd: '/w/proj', cwd_exists: true, last_activity_at: 5, last_in: 'terminal',
}
const live = (code: string, name: string, tmux_instance = NEW): Session => ({ code, name, cwd: '', mode: 'terminal', tmux_instance })

function seed(conversation: TmuxSessionContent): void {
  const tab: Tab = {
    id: 't1', pinned: false, locked: false, createdAt: 1,
    layout: {
      type: 'split', id: 's1', direction: 'h', sizes: [34, 33, 33],
      children: [
        { type: 'leaf', pane: { id: 'live', content: { kind: 'tmux-session', hostId: H, sessionCode: 'c1', mode: 'terminal', cachedName: 'gone', tmuxInstance: OLD } } },
        { type: 'leaf', pane: { id: 'restarted', content: { kind: 'tmux-session', hostId: H, sessionCode: 'c2', mode: 'terminal', cachedName: 'dev', tmuxInstance: OLD, terminated: 'tmux-restarted' } } },
        { type: 'leaf', pane: { id: 'conv', content: conversation } },
      ],
    },
  }
  useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
}

function pane(paneId: string): PaneContent {
  const found = findPane(useTabStore.getState().tabs.t1.layout, paneId)
  if (!found) throw new Error(`fixture: ${paneId}`)
  return found.content
}

beforeEach(() => {
  useHostStore.setState({
    hosts: { [H]: { id: H, name: H, ip: '127.0.0.1', port: 7860, token: null, order: 0 } },
    hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected', attachReady: true } },
  })
  useShownHostsStore.setState({ ids: [H] })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
})

describe('reconcileHostSessions — a conversation-ended pane (R-4-3)', () => {
  it('keeps terminated, generation, record and conversation while its siblings are reconciled', () => {
    const conversation = conversationRebuildContent(H, row, 'proj-2', OLD, 1)
    seed(conversation)

    // `c1` is gone, `dev` is back under a new code, and a session already carries the pane's generated name.
    reconcileHostSessions(H, [live('c9', 'dev'), live('c7', 'proj-2')])

    expect(pane('conv')).toEqual(conversation)
    // The same payload did act on this host: the closed pane is closed, the restarted one revived by name.
    expect(pane('live')).toMatchObject({ terminated: 'session-closed' })
    expect(pane('restarted')).toMatchObject({ sessionCode: 'c9', tmuxInstance: NEW })
    expect((pane('restarted') as TmuxSessionContent).terminated).toBeUndefined()
  })

  it('a pane opened without a generation does not adopt the payload one', () => {
    const conversation = conversationRebuildContent(H, row, 'proj-2', '', 1)
    seed(conversation)

    reconcileHostSessions(H, [live('c9', 'dev'), live('c7', 'proj-2')])

    expect(pane('conv')).toEqual(conversation)
  })
})

// spa/src/lib/badge/badge-report.test.ts — a window's Dock-badge report (app-badge spec §3.2, §4.1): the session keys
// it resolved (unread on a tab, plus cards it resolved) and every open card by identity.
import { describe, expect, it } from 'vitest'
import type { Approval } from '../team/types'
import { createTab, type PaneLayout, type PaneRebuildRecord, type Tab } from '../../types/tab'
import { badgeReport, knownSessionIdsOf, type BadgeReportInput } from './badge-report'

/** A complete rebuild record (`sessionName` and `tmuxInstance` are required) whose agent has Claude Code session id `sessionId`. */
const rebuildOf = (sessionName: string, sessionId: string): PaneRebuildRecord => ({
  sessionName, tmuxInstance: 'i1', agent: { type: 'cc', sessionId, updatedAt: 1 }, capturedAt: 1,
})
const tmuxTab = (hostId: string, sessionCode: string, rebuildSessionId?: string): Tab => createTab({
  kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: sessionCode, tmuxInstance: 'i1',
  ...(rebuildSessionId ? { rebuild: rebuildOf(sessionCode, rebuildSessionId) } : {}),
})
const tabsOf = (...tabs: Tab[]): Record<string, Tab> => Object.fromEntries(tabs.map((t) => [t.id, t]))

const card = (id: string, over: Partial<Approval['origin']> = {}, kind: Approval['kind'] = 'lead'): Approval => ({
  id, kind, host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: '_x', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux: '', ...over },
  payload: {}, state: 'open', created_at: 1, deadline_at: 2, lease_until: 3,
})

/** `h1` has two sessions: code `aaaaaa` named `work`, code `bbbbbb` named `play`. */
const SESSIONS = { h1: [{ code: 'aaaaaa', name: 'work' }, { code: 'bbbbbb', name: 'play' }] }

function report(over: Partial<BadgeReportInput> = {}) {
  const tabs = over.tabs ?? {}
  return badgeReport({
    unread: {}, tabs, cards: [], sessions: SESSIONS, knownSessionIds: knownSessionIdsOf(tabs), ...over,
  })
}

describe('badgeReport — unread', () => {
  it('an unread session shown by a tab counts; one with no tab does not', () => {
    const r = report({ tabs: tabsOf(tmuxTab('h1', 'aaaaaa')), unread: { 'h1:aaaaaa': true, 'h1:bbbbbb': true } })
    expect(r.sessions).toEqual(['h1:aaaaaa'])
  })

  it('a session on a tab that is not unread does not count (false is not unread)', () => {
    const r = report({ tabs: tabsOf(tmuxTab('h1', 'aaaaaa')), unread: { 'h1:aaaaaa': false } })
    expect(r.sessions).toEqual([])
  })

  it('a worker (execution) pane counts like a session pane', () => {
    const exec = createTab({ kind: 'execution', executionId: 'e1', host: 'h1' })
    const r = report({ tabs: tabsOf(exec), unread: { 'h1:exec-e1': true } })
    expect(r.sessions).toEqual(['h1:exec-e1'])
  })

  it('a session shown only by a secondary pane of a split tab counts (every pane of every tab is scanned)', () => {
    const base = tmuxTab('h1', 'aaaaaa')
    const second = tmuxTab('h1', 'bbbbbb')
    const layout: PaneLayout = {
      type: 'split', id: 's', direction: 'h', sizes: [50, 50],
      children: [base.layout, second.layout],
    }
    const r = report({ tabs: tabsOf({ ...base, layout }), unread: { 'h1:bbbbbb': true } })
    expect(r.sessions).toEqual(['h1:bbbbbb'])
  })

  it('a pane whose session ended counts nothing: a restarted tmux server hands its code out again (#1840)', () => {
    const dead = createTab({
      kind: 'tmux-session', hostId: 'h1', sessionCode: 'aaaaaa', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1',
      terminated: 'session-closed',
    })
    const r = report({ tabs: tabsOf(dead), unread: { 'h1:aaaaaa': true } })
    expect(r.sessions).toEqual([])
  })

  it('an ended pane does not hide a live pane of the same key elsewhere', () => {
    const dead = createTab({
      kind: 'tmux-session', hostId: 'h1', sessionCode: 'aaaaaa', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1',
      terminated: 'session-closed',
    })
    const r = report({ tabs: tabsOf(dead, tmuxTab('h1', 'aaaaaa')), unread: { 'h1:aaaaaa': true } })
    expect(r.sessions).toEqual(['h1:aaaaaa'])
  })

  it('tabs of every workspace count (the report scans the whole tab store, not the active tab)', () => {
    const r = report({
      tabs: tabsOf(tmuxTab('h1', 'aaaaaa'), tmuxTab('h1', 'bbbbbb')),
      unread: { 'h1:aaaaaa': true, 'h1:bbbbbb': true },
    })
    expect(r.sessions).toEqual(['h1:aaaaaa', 'h1:bbbbbb'])
  })
})

describe('badgeReport — cards', () => {
  it('a card whose tmux session resolves counts its session whether or not that session has a tab', () => {
    const c = card('c1', { tmux: 'play:@1.%2' })
    const r = report({ cards: [{ hostId: 'h1', approval: c }] })
    expect(r.sessions).toEqual(['h1:bbbbbb'])
    expect(r.cards).toEqual([{ id: 'h1\u0000c1', session: 'h1:bbbbbb', fallback: 'c:h1:S-c1' }])
  })

  it('a card with no tmux origin stays unresolved: no session, fallback c:host:session_id', () => {
    const r = report({ cards: [{ hostId: 'h1', approval: card('c1') }] })
    expect(r.sessions).toEqual([])
    expect(r.cards).toEqual([{ id: 'h1\u0000c1', fallback: 'c:h1:S-c1' }])
  })

  it('a tmux session the host list does not have stays unresolved', () => {
    const r = report({ cards: [{ hostId: 'h1', approval: card('c1', { tmux: 'gone:@1.%1' }) }] })
    expect(r.cards[0].session).toBeUndefined()
  })

  it('a host with no session list at all stays unresolved', () => {
    const r = report({ cards: [{ hostId: 'h9', approval: card('c1', { tmux: 'work:@1.%1' }) }] })
    expect(r.cards[0].session).toBeUndefined()
  })

  it('no origin session_id → fallback a:host:approvalId', () => {
    const r = report({ cards: [{ hostId: 'h1', approval: card('c1', { session_id: '' }) }] })
    expect(r.cards).toEqual([{ id: 'h1\u0000c1', fallback: 'a:h1:c1' }])
  })

  it('a hook card counts like any other', () => {
    const r = report({ cards: [{ hostId: 'h1', approval: card('k1', { tmux: 'work:@1.%1' }, 'hook_ask') }] })
    expect(r.sessions).toEqual(['h1:aaaaaa'])
    expect(r.cards).toHaveLength(1)
  })

  it('the same session with an unread event and a card is one session', () => {
    const r = report({
      tabs: tabsOf(tmuxTab('h1', 'aaaaaa')), unread: { 'h1:aaaaaa': true },
      cards: [{ hostId: 'h1', approval: card('c1', { tmux: 'work:@1.%1' }) }, { hostId: 'h1', approval: card('c2', { tmux: 'work:@1.%2' }) }],
    })
    expect(r.sessions).toEqual(['h1:aaaaaa'])
    expect(r.cards.map((c) => c.id)).toEqual(['h1\u0000c1', 'h1\u0000c2'])
  })

  it('the card id is the host id and the approval id joined by NUL, the same in every window', () => {
    const r = report({ cards: [{ hostId: 'a:b', approval: card('x') }] })
    expect(r.cards[0].id).toBe('a:b\u0000x')
  })
})

describe('badgeReport — resolution check against a recreated session (spec §4.1)', () => {
  const c = card('c1', { tmux: 'work:@1.%1', session_id: 'NEW' })

  it('a known Claude Code session id that equals origin.session_id resolves', () => {
    const tabs = tabsOf(tmuxTab('h1', 'aaaaaa', 'NEW'))
    const r = report({ tabs, cards: [{ hostId: 'h1', approval: c }] })
    expect(r.cards[0].session).toBe('h1:aaaaaa')
  })

  it('a known id that differs (same name, recreated session) leaves the card unresolved, counted by its fallback', () => {
    const tabs = tabsOf(tmuxTab('h1', 'aaaaaa', 'OLD'))
    const r = report({ tabs, cards: [{ hostId: 'h1', approval: c }] })
    expect(r.cards).toEqual([{ id: 'h1\u0000c1', fallback: 'c:h1:NEW' }])
    expect(r.sessions).toEqual([])
  })

  it('an ended pane\'s recorded id is no evidence: ended OLD + live NEW, the card of OLD stays unresolved (#1840)', () => {
    const dead = createTab({
      kind: 'tmux-session', hostId: 'h1', sessionCode: 'aaaaaa', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1',
      terminated: 'tmux-restarted',
      rebuild: rebuildOf('work', 'OLD'),
    })
    const tabs = tabsOf(dead, tmuxTab('h1', 'aaaaaa', 'NEW'))
    const r = report({ tabs, cards: [{ hostId: 'h1', approval: card('c1', { tmux: 'work:@1.%1', session_id: 'OLD' }) }] })
    expect(r.cards).toEqual([{ id: 'h1\u0000c1', fallback: 'c:h1:OLD' }])
    expect(r.sessions).toEqual([])
  })

  it('two live panes of one key with different agents: a card of either resolves', () => {
    const tabs = tabsOf(tmuxTab('h1', 'aaaaaa', 'S1'), tmuxTab('h1', 'aaaaaa', 'S2'))
    for (const sid of ['S1', 'S2']) {
      const r = report({ tabs, cards: [{ hostId: 'h1', approval: card('c1', { tmux: 'work:@1.%1', session_id: sid }) }] })
      expect(r.cards[0].session).toBe('h1:aaaaaa')
    }
  })

  it('no known id → the name match stands', () => {
    const r = report({ tabs: tabsOf(tmuxTab('h1', 'aaaaaa')), cards: [{ hostId: 'h1', approval: c }] })
    expect(r.cards[0].session).toBe('h1:aaaaaa')
  })

  it('a known id with no origin session_id cannot be confirmed: unresolved, a: fallback', () => {
    const tabs = tabsOf(tmuxTab('h1', 'aaaaaa', 'OLD'))
    const r = report({ tabs, cards: [{ hostId: 'h1', approval: card('c1', { tmux: 'work:@1.%1', session_id: '' }) }] })
    expect(r.cards).toEqual([{ id: 'h1\u0000c1', fallback: 'a:h1:c1' }])
  })
})

describe('badgeReport — output', () => {
  it('sessions and cards are sorted, so equal reports compare equal', () => {
    const tabs = tabsOf(tmuxTab('h1', 'bbbbbb'), tmuxTab('h1', 'aaaaaa'))
    const r = report({
      tabs, unread: { 'h1:bbbbbb': true, 'h1:aaaaaa': true },
      cards: [{ hostId: 'h1', approval: card('z') }, { hostId: 'h1', approval: card('a') }],
    })
    expect(r.sessions).toEqual(['h1:aaaaaa', 'h1:bbbbbb'])
    expect(r.cards.map((x) => x.id)).toEqual(['h1\u0000a', 'h1\u0000z'])
  })

  it('nothing → an empty report', () => {
    expect(report()).toEqual({ sessions: [], cards: [] })
  })
})

describe('knownSessionIdsOf', () => {
  it('collects the recorded agent session ids of every pane showing the key, none for a pane without a record', () => {
    const t = tmuxTab('h1', 'aaaaaa', 'S1')
    const u = tmuxTab('h1', 'aaaaaa', 'S2')
    const v = tmuxTab('h1', 'bbbbbb')
    const known = knownSessionIdsOf(tabsOf(t, u, v))
    expect([...known('h1:aaaaaa')].sort()).toEqual(['S1', 'S2'])
    expect(known('h1:bbbbbb')).toEqual([])
    expect(known('h1:nope')).toEqual([])
  })
})

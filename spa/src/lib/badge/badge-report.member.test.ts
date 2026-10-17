// spa/src/lib/badge/badge-report.member.test.ts — member spec R2: a team member's unread counts nowhere on the Dock,
// its cards count as before.
import { describe, expect, it } from 'vitest'
import type { Approval } from '../team/types'
import { createTab, type Tab } from '../../types/tab'
import { badgeReport, knownSessionIdsOf, type BadgeReportInput } from './badge-report'

const tmuxTab = (hostId: string, sessionCode: string): Tab => createTab({
  kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: sessionCode, tmuxInstance: 'i1',
})
const tabsOf = (...tabs: Tab[]): Record<string, Tab> => Object.fromEntries(tabs.map((t) => [t.id, t]))
const card = (id: string, tmux: string): Approval => ({
  id, kind: 'lead', host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: '_x', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux },
  payload: {}, state: 'open', created_at: 1, deadline_at: 2, lease_until: 3,
})
const SESSIONS = { h1: [{ code: 'aaaaaa', name: 'work' }, { code: 'bbbbbb', name: 'play' }] }

function report(over: Partial<BadgeReportInput> = {}) {
  const tabs = over.tabs ?? {}
  return badgeReport({ unread: {}, tabs, cards: [], sessions: SESSIONS, knownSessionIds: knownSessionIdsOf(tabs), ...over })
}

describe('badgeReport — team members (R2)', () => {
  const tabs = tabsOf(tmuxTab('h1', 'aaaaaa'), tmuxTab('h1', 'bbbbbb'))
  const unread = { 'h1:aaaaaa': true, 'h1:bbbbbb': true }

  it('a member\'s unread is not reported, a non-member\'s is', () => {
    expect(report({ tabs, unread, members: new Set(['h1:aaaaaa']) }).sessions).toEqual(['h1:bbbbbb'])
  })

  it('without a member set every unread counts (as before)', () => {
    expect(report({ tabs, unread }).sessions).toEqual(['h1:aaaaaa', 'h1:bbbbbb'])
  })

  it('a member\'s open card still counts, with or without unread', () => {
    const r = report({
      tabs, unread, members: new Set(['h1:aaaaaa']),
      cards: [{ hostId: 'h1', approval: card('c1', 'work:@1.%1') }],
    })
    expect(r.sessions).toEqual(['h1:aaaaaa', 'h1:bbbbbb'])
    expect(r.cards[0].session).toBe('h1:aaaaaa')
  })

  it('a member\'s card counts even when its session has no tab', () => {
    const r = report({ members: new Set(['h1:aaaaaa']), cards: [{ hostId: 'h1', approval: card('c1', 'work:@1.%1') }] })
    expect(r.sessions).toEqual(['h1:aaaaaa'])
  })
})

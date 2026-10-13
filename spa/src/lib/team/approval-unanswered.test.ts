// spa/src/lib/team/approval-unanswered.test.ts — when an open card counts as "nobody answered" (relay v2 plan, App contract 3 +
// 88's rule for RA-1): (a) the row carries `unanswered_at > 0`, or (b) it is past `deadline_at` AND this App's pause data (the
// paused list or a roster row, on the SAME host) shows the card's session holding `relay_unanswered`. The daemon sends no
// event op when the last card runs out, so (b) is what an App that had the card open all along has to go on.
import { describe, it, expect } from 'vitest'
import { isCardUnanswered, nearestDeadline, sessionHoldsUnanswered, type PauseSources } from './approval-unanswered'
import type { Approval } from './types'
import type { PausedSession } from './paused-api'
import type { RosterMember, RosterSession, TeamRoster } from './roster'

const SENTINEL = Date.UTC(9999, 11, 31)
const card = (extra: Partial<Approval> = {}, session = 'S1'): Approval => ({
  id: 'c1', kind: 'self_relay', host_id: 'd1',
  origin: { session_id: session, ref: '_aaaaaa', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
  payload: { op_id: 'o', used_percentage: 50, window: 1 },
  state: 'open', created_at: 1_000, deadline_at: 10_000, lease_until: 5_000,
  ...extra,
})
const paused = (id: string, causes: PausedSession['causes']): PausedSession => ({ session_id: id, ref: '_x', title: '', role: 'solo', causes, since: 1, lead_ref: '', bounced: 0 })
const seat = (id: string, causes?: PausedSession['causes']): RosterSession => ({ session_id: id, ref: '_x', address: 'h/x', live: true, ...(causes ? { paused: { causes, since: 1 } } : {}) })
const team = (lead: RosterSession, members: RosterSession[] = []): TeamRoster => ({
  id: 't', host_id: 'd1', created_at: 1, team_name: '', team_label: '', lead,
  members: members.map((m) => ({ ...m, state: 'active', origin: 'spawn', joined_at: 1 }) as RosterMember),
})
const none: PauseSources = { paused: {}, roster: {} }
const withPaused = (hostId: string, id: string, causes: PausedSession['causes']): PauseSources => ({ paused: { [hostId]: { sessions: [paused(id, causes)], loadedAt: 1 } }, roster: {} })

describe('sessionHoldsUnanswered', () => {
  it('reads the paused list of that host', () => {
    expect(sessionHoldsUnanswered('h1', 'S1', withPaused('h1', 'S1', ['quota', 'relay_unanswered']))).toBe(true)
    expect(sessionHoldsUnanswered('h1', 'S1', withPaused('h1', 'S1', ['quota']))).toBe(false)
  })
  it('reads a roster row (lead or member) of that host', () => {
    const lead: PauseSources = { paused: {}, roster: { h1: [team(seat('S1', ['relay_unanswered']))] } }
    const member: PauseSources = { paused: {}, roster: { h1: [team(seat('L'), [seat('S1', ['relay_unanswered'])])] } }
    expect(sessionHoldsUnanswered('h1', 'S1', lead)).toBe(true)
    expect(sessionHoldsUnanswered('h1', 'S1', member)).toBe(true)
    expect(sessionHoldsUnanswered('h1', 'S1', { paused: {}, roster: { h1: [team(seat('S1', ['quota']))] } })).toBe(false)
  })
  it('the same session id on another host does not count', () => {
    expect(sessionHoldsUnanswered('h1', 'S1', withPaused('h2', 'S1', ['relay_unanswered']))).toBe(false)
    expect(sessionHoldsUnanswered('h1', 'S1', { paused: {}, roster: { h2: [team(seat('S1', ['relay_unanswered']))] } })).toBe(false)
  })
  it('no pause data at all: false', () => {
    expect(sessionHoldsUnanswered('h1', 'S1', none)).toBe(false)
  })
})

describe('isCardUnanswered', () => {
  // Mutation gate: drop rule (b) → the "opened card past its deadline" cases go red.
  it('(a) unanswered_at > 0 is enough, with no pause data', () => {
    expect(isCardUnanswered('h1', card({ unanswered_at: 9_000, deadline_at: SENTINEL }), 0, none)).toBe(true)
  })
  it('(b) past the deadline and the session holds relay_unanswered (paused list or roster)', () => {
    expect(isCardUnanswered('h1', card(), 10_001, withPaused('h1', 'S1', ['relay_unanswered']))).toBe(true)
    expect(isCardUnanswered('h1', card(), 10_001, { paused: {}, roster: { h1: [team(seat('S1', ['relay_unanswered']))] } })).toBe(true)
  })
  it('past the deadline without the pause cause: an ordinary card (no guessing)', () => {
    expect(isCardUnanswered('h1', card(), 10_001, none)).toBe(false)
    expect(isCardUnanswered('h1', card(), 10_001, withPaused('h1', 'S1', ['quota']))).toBe(false)
  })
  it('the pause cause before the deadline: still an ordinary card', () => {
    expect(isCardUnanswered('h1', card(), 9_999, withPaused('h1', 'S1', ['relay_unanswered']))).toBe(false)
  })
  it('another host with the same session id: not unanswered', () => {
    expect(isCardUnanswered('h1', card(), 10_001, withPaused('h2', 'S1', ['relay_unanswered']))).toBe(false)
  })
  it('another session paused: not unanswered', () => {
    expect(isCardUnanswered('h1', card(), 10_001, withPaused('h1', 'S2', ['relay_unanswered']))).toBe(false)
  })
  it('unanswered_at 0 or absent follows (b) only', () => {
    expect(isCardUnanswered('h1', card({ unanswered_at: 0 }), 0, none)).toBe(false)
  })
})

describe('nearestDeadline', () => {
  const entry = (hostId: string, id: string, deadline_at: number, extra: Partial<Approval> = {}) => ({ hostId, approval: card({ id, deadline_at, ...extra }) })
  const entries = (...es: ReturnType<typeof entry>[]) => Object.fromEntries(es.map((e) => [`${e.hostId}/${e.approval.id}`, e]))
  it('the smallest deadline_at; null when nothing is open', () => {
    expect(nearestDeadline({}, 0, none)).toBeNull()
    expect(nearestDeadline(entries(entry('h1', 'a', 900), entry('h2', 'b', 300), entry('h3', 'c', 600)), 0, none)).toBe(300)
  })
  it('skips a card that is unanswered by either rule; null when only those are open', () => {
    const e = entries(entry('h1', 'a', SENTINEL, { unanswered_at: 5 }), entry('h1', 'b', 700, { origin: { ...card().origin, session_id: 'S9' } }))
    expect(nearestDeadline(e, 0, none)).toBe(700)
    // b is past its deadline and its session holds relay_unanswered → skipped too.
    expect(nearestDeadline(e, 701, withPaused('h1', 'S9', ['relay_unanswered']))).toBeNull()
  })
})

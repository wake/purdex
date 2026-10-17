// spa/src/lib/team/roster.fresh.test.ts — `RosterSession.fresh_handoff` (relay v2 contract 5, `relay.half.v1`): checked per
// session, a malformed value reads as "none" and never drops the session or the frame.
import { describe, it, expect } from 'vitest'
import { parseFreshHandoff, parseRosterEvent } from './roster'

const lead = { session_id: 's-lead', ref: '_aaaaaa', address: 'mlab/lead-aa', live: true }
const member = { session_id: 's-m1', ref: '_bbbbbb', address: 'mlab/m1-bb', live: false, state: 'active', origin: 'spawned', joined_at: 5 }
const team = { id: 't1', host_id: 'd1', created_at: 1, team_name: '', team_label: '', lead, members: [member] }
const frame = (t: unknown) => JSON.stringify({ op: 'snapshot', teams: [t] })
const good = { op_id: 'op-1', path: '/w/handoff.md', written_at: 1_700_000_000_000 }

function parsed(t: unknown) {
  const r = parseRosterEvent(frame(t))
  if (typeof r === 'string') throw new Error(r)
  return r.teams[0]
}

describe('roster fresh_handoff', () => {
  it('is kept on a lead and on a member', () => {
    const t = parsed({ ...team, lead: { ...lead, fresh_handoff: good }, members: [{ ...member, fresh_handoff: good }] })
    expect(t.lead.fresh_handoff).toEqual(good)
    expect(t.members[0].fresh_handoff).toEqual(good)
  })

  it('is absent when the wire has none', () => {
    const t = parsed(team)
    expect('fresh_handoff' in t.lead).toBe(false)
    expect('fresh_handoff' in t.members[0]).toBe(false)
  })

  it.each([
    ['null', null],
    ['a string', 'x'],
    ['an array', [good]],
    ['no path', { op_id: 'op-1', written_at: 1 }],
    ['an empty path', { ...good, path: '' }],
    ['a non-string path', { ...good, path: 3 }],
    ['no op_id', { path: '/p', written_at: 1 }],
    ['no written_at', { op_id: 'o', path: '/p' }],
    ['a string written_at', { ...good, written_at: '1700' }],
    ['an infinite written_at', { ...good, written_at: Infinity }],
    ['a written_at too big for a Date', { ...good, written_at: 1e20 }],
    ['a written_at too small for a Date', { ...good, written_at: -1e20 }],
    ['a written_at just past the Date range', { ...good, written_at: 8.64e15 + 2000 }],
  ])('a malformed value (%s) reads as none and keeps the session and the frame', (_name, bad) => {
    const t = parsed({ ...team, lead: { ...lead, fresh_handoff: bad }, members: [{ ...member, fresh_handoff: good }] })
    expect('fresh_handoff' in t.lead).toBe(false)
    expect(t.lead.session_id).toBe('s-lead')
    expect(t.members[0].fresh_handoff).toEqual(good) // the other seat is untouched
  })

  it('the edges of the Date range are kept', () => {
    expect(parseFreshHandoff({ ...good, written_at: 8.64e15 })).toEqual({ ...good, written_at: 8.64e15 })
    expect(parseFreshHandoff({ ...good, written_at: -8.64e15 })).toEqual({ ...good, written_at: -8.64e15 })
  })

  it('parseFreshHandoff drops unknown extra fields', () => {
    expect(parseFreshHandoff({ ...good, junk: 1 })).toEqual(good)
  })
})

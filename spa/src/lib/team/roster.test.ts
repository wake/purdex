// spa/src/lib/team/roster.test.ts — the `team.roster` wire (plan PL-2b′): checked whole, dropped whole.
import { describe, it, expect } from 'vitest'
import { parseRosterEvent, type TeamRoster } from './roster'

const lead = { session_id: 's-lead', ref: '_aaaaaa', address: 'mlab/lead-aa', title: 'Lead', name: 'lead', tmux_session: 'lead', live: true }
const member = {
  session_id: 's-m1', ref: '_bbbbbb', address: 'mlab/m1-bb', live: false,
  state: 'active', origin: 'spawned', joined_at: 5,
}
const team: TeamRoster = { id: 't1', host_id: 'd1', created_at: 1, lead, members: [member] }
const v = (o: unknown) => JSON.stringify(o)

describe('parseRosterEvent', () => {
  it('accepts a snapshot and a changed (as a JSON string)', () => {
    expect(parseRosterEvent(v({ op: 'snapshot', teams: [team] }))).toEqual({ op: 'snapshot', teams: [team] })
    expect(parseRosterEvent(v({ op: 'changed', teams: [] }))).toEqual({ op: 'changed', teams: [] })
  })

  it('accepts an already-parsed value', () => {
    expect(parseRosterEvent({ op: 'changed', teams: [team] })).toEqual({ op: 'changed', teams: [team] })
  })

  it('accepts the optional model / effort / context of a session', () => {
    const rich = {
      ...lead, model: 'opus', effort: 'high',
      context: { used_percentage: null, window: 200000, model_id: 'claude-opus', effort: 'high', at: 9 },
    }
    const t = { ...team, lead: rich }
    expect(parseRosterEvent({ op: 'snapshot', teams: [t] })).toEqual({ op: 'snapshot', teams: [t] })
    const t2 = { ...team, lead: { ...lead, context: { used_percentage: 41.5, window: 1, at: 2 } } }
    expect(typeof parseRosterEvent({ op: 'snapshot', teams: [t2] })).toBe('object')
  })

  it.each([
    ['not JSON', 'nope{'],
    ['not an object', v([1])],
    ['null', 'null'],
    ['unknown op', v({ op: 'removed', teams: [] })],
    ['no op', v({ teams: [] })],
    ['teams is not an array', v({ op: 'snapshot', teams: {} })],
    ['teams is null', v({ op: 'snapshot', teams: null })],
    ['a team is not an object', v({ op: 'snapshot', teams: [1] })],
    ['a team lacks an id', v({ op: 'snapshot', teams: [{ ...team, id: undefined }] })],
    ['created_at is not a number', v({ op: 'snapshot', teams: [{ ...team, created_at: '1' }] })],
    ['lead is missing', v({ op: 'snapshot', teams: [{ ...team, lead: undefined }] })],
    ['lead.live is not a boolean', v({ op: 'snapshot', teams: [{ ...team, lead: { ...lead, live: 1 } }] })],
    ['members is null', v({ op: 'snapshot', teams: [{ ...team, members: null }] })],
    ['a member lacks joined_at', v({ op: 'snapshot', teams: [{ ...team, members: [{ ...member, joined_at: undefined }] }] })],
    ['a member lacks origin', v({ op: 'snapshot', teams: [{ ...team, members: [{ ...member, origin: undefined }] }] })],
    ['a member is a bare session', v({ op: 'snapshot', teams: [{ ...team, members: [lead] }] })],
    ['a title is a number', v({ op: 'snapshot', teams: [{ ...team, lead: { ...lead, title: 3 } }] })],
    ['context.window is a string', v({ op: 'snapshot', teams: [{ ...team, lead: { ...lead, context: { used_percentage: 1, window: 'x', at: 1 } } }] })],
    ['context.used_percentage is a string', v({ op: 'snapshot', teams: [{ ...team, lead: { ...lead, context: { used_percentage: '1', window: 1, at: 1 } } }] })],
  ])('drops the whole frame: %s', (_label, value) => {
    expect(typeof parseRosterEvent(value)).toBe('string')
  })

  it('drops the whole frame when one of two teams is bad', () => {
    expect(typeof parseRosterEvent(v({ op: 'snapshot', teams: [team, { ...team, id: 7 }] }))).toBe('string')
  })
})

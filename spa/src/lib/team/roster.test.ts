// spa/src/lib/team/roster.test.ts — the `team.roster` wire (plan PL-2b′): checked whole, dropped whole.
import { describe, it, expect } from 'vitest'
import { parseRosterEvent, type TeamRoster } from './roster'

const lead = { session_id: 's-lead', ref: '_aaaaaa', address: 'mlab/lead-aa', title: 'Lead', name: 'lead', tmux_session: 'lead', live: true }
const member = {
  session_id: 's-m1', ref: '_bbbbbb', address: 'mlab/m1-bb', live: false,
  state: 'active', origin: 'spawned', joined_at: 5,
}
const team: TeamRoster = { id: 't1', host_id: 'd1', created_at: 1, team_name: '', team_label: '', lead, members: [member] }
const v = (o: unknown) => JSON.stringify(o)

describe('parseRosterEvent', () => {
  it('accepts a snapshot and a changed (as a JSON string)', () => {
    expect(parseRosterEvent(v({ op: 'snapshot', teams: [team] }))).toEqual({ op: 'snapshot', teams: [team] })
    expect(parseRosterEvent(v({ op: 'changed', teams: [] }))).toEqual({ op: 'changed', teams: [] })
  })

  it('accepts an already-parsed value', () => {
    expect(parseRosterEvent({ op: 'changed', teams: [team] })).toEqual({ op: 'changed', teams: [team] })
  })

  it('keeps a team name as sent', () => {
    const named = { ...team, team_name: '驗收 team', team_label: '' }
    expect(parseRosterEvent(v({ op: 'snapshot', teams: [named] }))).toEqual({ op: 'snapshot', teams: [named] })
  })

  it('normalises a team from a daemon that predates names (no team_name, or not a string) to ""', () => {
    const { team_name: _omit, ...old } = team
    void _omit
    for (const t of [old, { ...old, team_name: null }, { ...old, team_name: 7 }]) {
      const r = parseRosterEvent(v({ op: 'snapshot', teams: [t] }))
      expect(r).toEqual({ op: 'snapshot', teams: [{ ...team, team_name: '', team_label: '' }] })
      expect((r as { teams: TeamRoster[] }).teams[0].team_name).toBe('')
    }
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

describe('parseRosterEvent — a team id cannot carry the team-key separator', () => {
  it('drops a frame whose team id contains NUL, whole', () => {
    expect(typeof parseRosterEvent(v({ op: 'snapshot', teams: [team, { ...team, id: 'b\u0000c' }] }))).toBe('string')
  })
})


describe('max_members / in_use on the roster', () => {
  const frame = (t: Record<string, unknown>) => JSON.stringify({ op: 'changed', teams: [{ ...team, ...t }] })

  it('are kept when both are integers', () => {
    const r = parseRosterEvent(frame({ max_members: 4, in_use: 2 }))
    expect(typeof r).toBe('object')
    const t = (r as { teams: TeamRoster[] }).teams[0]
    expect(t.max_members).toBe(4)
    expect(t.in_use).toBe(2)
  })

  it('are absent from a daemon that does not send them (the frame is still valid)', () => {
    const t = (parseRosterEvent(frame({})) as { teams: TeamRoster[] }).teams[0]
    expect(t.max_members).toBeUndefined()
    expect(t.in_use).toBeUndefined()
  })

  it('a cap without its usage, or a non-integer, is dropped as a pair', () => {
    for (const bad of [{ max_members: 4 }, { in_use: 2 }, { max_members: '4', in_use: 2 }, { max_members: 4.5, in_use: 2 }, { max_members: null, in_use: null }]) {
      const t = (parseRosterEvent(frame(bad)) as { teams: TeamRoster[] }).teams[0]
      expect(t.max_members).toBeUndefined()
      expect(t.in_use).toBeUndefined()
    }
  })
})

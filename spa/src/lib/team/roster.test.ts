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

  it('host_alias and host_id are parsed only when strings', () => {
    const good = { ...member, host_id: 'dm-b', host_alias: 'b26' }
    const bad = { ...member, session_id: 's-m2', host_id: 7, host_alias: { x: 1 } }
    const t = { ...team, lead: { ...lead, host_id: '', host_alias: null }, members: [good, bad] }
    const r = parseRosterEvent(v({ op: 'snapshot', teams: [t] }))
    if (typeof r === 'string') throw new Error(r)
    const [g, b] = r.teams[0].members
    expect(g).toMatchObject({ host_id: 'dm-b', host_alias: 'b26' })
    expect('host_id' in b || 'host_alias' in b).toBe(false)
    expect(r.teams[0].lead.host_id).toBe('')
    expect('host_alias' in r.teams[0].lead).toBe(false)
    // an old roster without them parses exactly as before
    expect(parseRosterEvent(v({ op: 'snapshot', teams: [team] }))).toEqual({ op: 'snapshot', teams: [team] })
  })

  it('context_unavailable is kept only as boolean true; anything else reads as absent, never drops the frame', () => {
    const parse = (extra: Record<string, unknown>) => {
      const r = parseRosterEvent(v({ op: 'snapshot', teams: [{ ...team, members: [{ ...member, ...extra }] }] }))
      if (typeof r === 'string') throw new Error(r)
      return r.teams[0].members[0]
    }
    expect(parse({ context_unavailable: true }).context_unavailable).toBe(true)
    for (const bad of [false, 'true', 1, null, {}]) expect('context_unavailable' in parse({ context_unavailable: bad })).toBe(false)
    expect('context_unavailable' in parse({})).toBe(false)
  })

  it('a member state outside the known four is kept as sent (display treats it as active)', () => {
    const r = parseRosterEvent(v({ op: 'snapshot', teams: [{ ...team, members: [{ ...member, state: 'joining' }, { ...member, session_id: 's2', state: 'weird' }] }] }))
    if (typeof r === 'string') throw new Error(r)
    expect(r.teams[0].members.map((m) => m.state)).toEqual(['joining', 'weird'])
  })

  it('host_alias is bounded and printable; host_id bounded and control-free; invalid ones are dropped, never the frame', () => {
    const parse = (extra: Record<string, unknown>) => {
      const r = parseRosterEvent(v({ op: 'snapshot', teams: [{ ...team, members: [{ ...member, ...extra }] }] }))
      if (typeof r === 'string') throw new Error(r)
      return r.teams[0].members[0]
    }
    expect(parse({ host_alias: '工作站 B-26' }).host_alias).toBe('工作站 B-26') // a clean CJK alias is kept intact
    expect(parse({ host_alias: 'a'.repeat(64) }).host_alias).toHaveLength(64)
    for (const bad of [
      'a'.repeat(65), '', '   ', 'x\ny', 'x\u0000y', 'x\u001by', 'x\u007fy', 'x\u0085y', 'x​y', 'x‮y', 'x⁦y', 'x﻿y',
    ]) {
      expect('host_alias' in parse({ host_alias: bad }), JSON.stringify(bad)).toBe(false)
    }
    expect(parse({ host_id: 'd'.repeat(128) }).host_id).toHaveLength(128)
    expect('host_id' in parse({ host_id: 'd'.repeat(129) })).toBe(false)
    expect('host_id' in parse({ host_id: 'a\nb' })).toBe(false)
    expect(parse({ host_id: '' }).host_id).toBe('') // "" is the lead's host and stays
  })

  it('a host_id that was sent but cannot be trusted is marked host_untrusted, never read as local', () => {
    const parse = (extra: Record<string, unknown>) => {
      const r = parseRosterEvent(v({ op: 'snapshot', teams: [{ ...team, members: [{ ...member, ...extra }] }] }))
      if (typeof r === 'string') throw new Error(r)
      return r.teams[0].members[0]
    }
    for (const bad of ['d'.repeat(129), 'a\nb', 'a\u202eb', 7, true, { x: 1 }, ['d']]) {
      const m = parse({ host_id: bad, host_alias: 'b26' })
      expect(m.host_untrusted, JSON.stringify(bad)).toBe(true)
      expect('host_id' in m).toBe(false)
    }
    for (const fine of [{}, { host_id: '' }, { host_id: 'dm-b' }, { host_alias: 'only-alias' }]) {
      expect('host_untrusted' in parse(fine), JSON.stringify(fine)).toBe(false)
    }
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

  it('an impossible cap is dropped as a pair: outside 1-8, a negative usage, or more in use than the cap', () => {
    for (const bad of [{ max_members: 0, in_use: 0 }, { max_members: 9, in_use: 1 }, { max_members: -1, in_use: -1 }, { max_members: 1, in_use: 5 }, { max_members: 4, in_use: -1 }]) {
      const t = (parseRosterEvent(frame(bad)) as { teams: TeamRoster[] }).teams[0]
      expect(t.max_members).toBeUndefined()
      expect(t.in_use).toBeUndefined()
    }
    const edge = (parseRosterEvent(frame({ max_members: 8, in_use: 8 })) as { teams: TeamRoster[] }).teams[0]
    expect(edge).toMatchObject({ max_members: 8, in_use: 8 })
    const low = (parseRosterEvent(frame({ max_members: 1, in_use: 0 })) as { teams: TeamRoster[] }).teams[0]
    expect(low).toMatchObject({ max_members: 1, in_use: 0 })
  })

  it('a cap without its usage, or a non-integer, is dropped as a pair', () => {
    for (const bad of [{ max_members: 4 }, { in_use: 2 }, { max_members: '4', in_use: 2 }, { max_members: 4.5, in_use: 2 }, { max_members: null, in_use: null }]) {
      const t = (parseRosterEvent(frame(bad)) as { teams: TeamRoster[] }).teams[0]
      expect(t.max_members).toBeUndefined()
      expect(t.in_use).toBeUndefined()
    }
  })
})

describe('team_color on the roster (TR-1)', () => {
  const frame = (t: Record<string, unknown>) => JSON.stringify({ op: 'changed', teams: [{ ...team, ...t }] })
  const parsed = (t: Record<string, unknown>) => (parseRosterEvent(frame(t)) as { teams: TeamRoster[] }).teams[0]

  it('an integer 0-7 is kept', () => {
    for (const c of [0, 3, 7]) expect(parsed({ team_color: c }).team_color).toBe(c)
  })

  it('absent, null and bad values are automatic (the field is absent, the frame is still valid)', () => {
    for (const bad of [{}, { team_color: null }, { team_color: 8 }, { team_color: -1 }, { team_color: 2.5 }, { team_color: '3' }, { team_color: {} }]) {
      expect('team_color' in parsed(bad)).toBe(false)
    }
  })
})

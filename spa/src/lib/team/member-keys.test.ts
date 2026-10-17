// spa/src/lib/team/member-keys.test.ts — the set of sessions that are active team members (member spec §3.2, R4).
import { describe, it, expect } from 'vitest'
import { activeMemberKeys } from './member-keys'
import type { RosterMember, RosterSession, TeamRoster } from './roster'

const sess = (id: string, tmux?: string, extra: Partial<RosterSession> = {}): RosterSession => ({
  session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true, ...(tmux ? { tmux_session: tmux } : {}), ...extra,
})
const mem = (id: string, tmux?: string, extra: Partial<RosterMember> = {}): RosterMember => ({
  ...sess(id, tmux), state: 'active', origin: 'spawned', joined_at: 1, ...extra,
})
const team = (lead: RosterSession, members: RosterMember[], hostId = 'daemon-a'): TeamRoster => ({
  id: 't1', host_id: hostId, created_at: 1, team_name: '', team_label: '', lead, members,
})
const LIVE = new Set(['h1', 'h2'])
const row = (code: string, name: string) => ({ code, name })

describe('activeMemberKeys', () => {
  it('maps an active member to hostId:code by its tmux session name', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'worker')])] },
      sessionsByHost: { h1: [row('aaaaaa', 'lead'), row('bbbbbb', 'worker')] },
    })
    expect([...keys]).toEqual(['h1:bbbbbb'])
  })

  it('never includes the lead', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [])] },
      sessionsByHost: { h1: [row('aaaaaa', 'lead')] },
    })
    expect(keys.size).toBe(0)
  })

  it('joining / releasing / killing are not members; active is, and so is a state a newer daemon may add', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [
        mem('A', 'a'), mem('B', 'b', { state: 'releasing' }), mem('C', 'c', { state: 'joining' }), mem('D', 'd', { state: 'killing' }), mem('E', 'e', { state: 'future' }),
      ])] },
      sessionsByHost: { h1: [row('aaaaaa', 'a'), row('bbbbbb', 'b'), row('cccccc', 'c'), row('dddddd', 'd'), row('eeeeee', 'e')] },
    })
    expect([...keys].sort()).toEqual(['h1:aaaaaa', 'h1:eeeeee'])
  })

  it('maps by the session NAME, not by the code', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'bbbbbb')])] },
      sessionsByHost: { h1: [row('bbbbbb', 'other'), row('cccccc', 'bbbbbb')] },
    })
    expect([...keys]).toEqual(['h1:cccccc'])
  })

  it('R4: a seat whose session is not in the list is not a member', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'worker'), mem('N')])] },
      sessionsByHost: { h1: [row('aaaaaa', 'lead')] },
    })
    expect(keys.size).toBe(0)
  })

  it('R4: no session list for the host yet → none of its sessions', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'worker')])] },
      sessionsByHost: {},
    })
    expect(keys.size).toBe(0)
  })

  it('R4: no roster for the host → nothing', () => {
    const keys = activeMemberKeys({ liveRosterHosts: LIVE, liveSessionHosts: LIVE, rosterByHost: {}, sessionsByHost: { h1: [row('aaaaaa', 'worker')] } })
    expect(keys.size).toBe(0)
  })

  it('maps a remote member to the host its daemon id names (seatHostOf)', () => {
    const remote = mem('R', 'rw', { host_id: 'daemon-b', host_alias: 'b' })
    const input = {
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [remote])] },
      sessionsByHost: { h1: [row('aaaaaa', 'rw')], h2: [row('bbbbbb', 'rw')] },
    }
    expect([...activeMemberKeys({ ...input, hostIdByDaemonId: { 'daemon-b': 'h2' } })]).toEqual(['h2:bbbbbb'])
    // the lead host's same-named session is not the member
    expect(activeMemberKeys(input).size).toBe(0)
  })

  it('R4: a remote member whose host session list has not arrived is not a member', () => {
    const remote = mem('R', 'rw', { host_id: 'daemon-b' })
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [remote])] },
      sessionsByHost: { h1: [row('aaaaaa', 'rw')] },
      hostIdByDaemonId: { 'daemon-b': 'h2' },
    })
    expect(keys.size).toBe(0)
  })

  it('collects members across two hosts', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: {
        h1: [team(sess('L1', 'l1'), [mem('A', 'a')])],
        h2: [team(sess('L2', 'l2'), [mem('B', 'b')], 'daemon-b')],
      },
      sessionsByHost: { h1: [row('aaaaaa', 'a')], h2: [row('bbbbbb', 'b')] },
    })
    expect([...keys].sort()).toEqual(['h1:aaaaaa', 'h2:bbbbbb'])
  })

  describe('R4: the seat host\'s session list must be from its current connection', () => {
    const remote = mem('R', 'rw', { host_id: 'daemon-b' })
    const base = {
      liveRosterHosts: new Set(['h1']),
      rosterByHost: { h1: [team(sess('L', 'lead'), [remote, mem('S', 'sw')])] },
      sessionsByHost: { h1: [row('aaaaaa', 'sw')], h2: [row('bbbbbb', 'rw')] },
      hostIdByDaemonId: { 'daemon-b': 'h2' },
    }
    it('cross-host seat: seat host dropped (list stale) → not a member; list landed → member', () => {
      expect(activeMemberKeys({ ...base, liveSessionHosts: new Set(['h1']) }).has('h2:bbbbbb')).toBe(false)
      expect(activeMemberKeys({ ...base, liveSessionHosts: new Set(['h1', 'h2']) }).has('h2:bbbbbb')).toBe(true)
    })
    it('same-host seat: its own list not live → not a member', () => {
      expect(activeMemberKeys({ ...base, liveSessionHosts: new Set(['h2']) }).has('h1:aaaaaa')).toBe(false)
      expect(activeMemberKeys({ ...base, liveSessionHosts: new Set(['h1']) }).has('h1:aaaaaa')).toBe(true)
    })
    it('roster live but seat host list not, and the reverse → neither is a member', () => {
      expect(activeMemberKeys({ ...base, liveSessionHosts: new Set() }).size).toBe(0)
      expect(activeMemberKeys({ ...base, liveRosterHosts: new Set(), liveSessionHosts: new Set(['h1', 'h2']) }).size).toBe(0)
    })
  })

  describe('tmux generation (MQ-2b, R4)', () => {
    const run = (seatInst: string | undefined, rowInst: string | undefined) => [...activeMemberKeys({
      liveRosterHosts: LIVE, liveSessionHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'worker', seatInst === undefined ? {} : { tmux_instance: seatInst })])] },
      sessionsByHost: { h1: [{ code: 'bbbbbb', name: 'worker', ...(rowInst === undefined ? {} : { tmux_instance: rowInst }) }] },
    })]
    it('equal instances: a member', () => { expect(run('100:1', '100:1')).toEqual(['h1:bbbbbb']) })
    it('tmux restarted (different instance): not a member', () => { expect(run('100:1', '200:2')).toEqual([]) })
    it('session row instance "" never matches', () => { expect(run('100:1', '')).toEqual([]) })
    it('session row without an instance never matches', () => { expect(run('100:1', undefined)).toEqual([]) })
    it('seat without an instance: name mapping as before', () => {
      expect(run(undefined, '200:2')).toEqual(['h1:bbbbbb'])
      expect(run(undefined, undefined)).toEqual(['h1:bbbbbb'])
    })
  })
})

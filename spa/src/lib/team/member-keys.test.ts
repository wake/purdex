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
      liveRosterHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'worker')])] },
      sessionsByHost: { h1: [row('aaaaaa', 'lead'), row('bbbbbb', 'worker')] },
    })
    expect([...keys]).toEqual(['h1:bbbbbb'])
  })

  it('never includes the lead', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [])] },
      sessionsByHost: { h1: [row('aaaaaa', 'lead')] },
    })
    expect(keys.size).toBe(0)
  })

  it('counts only state "active" (joining / releasing / killing are not members)', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [
        mem('A', 'a'), mem('B', 'b', { state: 'releasing' }), mem('C', 'c', { state: 'joining' }), mem('D', 'd', { state: 'killing' }),
      ])] },
      sessionsByHost: { h1: [row('aaaaaa', 'a'), row('bbbbbb', 'b'), row('cccccc', 'c'), row('dddddd', 'd')] },
    })
    expect([...keys]).toEqual(['h1:aaaaaa'])
  })

  it('maps by the session NAME, not by the code', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'bbbbbb')])] },
      sessionsByHost: { h1: [row('bbbbbb', 'other'), row('cccccc', 'bbbbbb')] },
    })
    expect([...keys]).toEqual(['h1:cccccc'])
  })

  it('R4: a seat whose session is not in the list is not a member', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'worker'), mem('N')])] },
      sessionsByHost: { h1: [row('aaaaaa', 'lead')] },
    })
    expect(keys.size).toBe(0)
  })

  it('R4: no session list for the host yet → none of its sessions', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [mem('M', 'worker')])] },
      sessionsByHost: {},
    })
    expect(keys.size).toBe(0)
  })

  it('R4: no roster for the host → nothing', () => {
    const keys = activeMemberKeys({ liveRosterHosts: LIVE, rosterByHost: {}, sessionsByHost: { h1: [row('aaaaaa', 'worker')] } })
    expect(keys.size).toBe(0)
  })

  it('maps a remote member to the host its daemon id names (seatHostOf)', () => {
    const remote = mem('R', 'rw', { host_id: 'daemon-b', host_alias: 'b' })
    const input = {
      liveRosterHosts: LIVE,
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
      liveRosterHosts: LIVE,
      rosterByHost: { h1: [team(sess('L', 'lead'), [remote])] },
      sessionsByHost: { h1: [row('aaaaaa', 'rw')] },
      hostIdByDaemonId: { 'daemon-b': 'h2' },
    })
    expect(keys.size).toBe(0)
  })

  it('collects members across two hosts', () => {
    const keys = activeMemberKeys({
      liveRosterHosts: LIVE,
      rosterByHost: {
        h1: [team(sess('L1', 'l1'), [mem('A', 'a')])],
        h2: [team(sess('L2', 'l2'), [mem('B', 'b')], 'daemon-b')],
      },
      sessionsByHost: { h1: [row('aaaaaa', 'a')], h2: [row('bbbbbb', 'b')] },
    })
    expect([...keys].sort()).toEqual(['h1:aaaaaa', 'h2:bbbbbb'])
  })
})

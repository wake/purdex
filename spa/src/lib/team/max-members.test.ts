import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApprovalApiError } from './approval-api'
import { configureMaxMembers, resetMaxMembers, setMaxMembers, teamKey, useMaxMembersStore } from './max-members'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import type { RosterSession, TeamRoster } from './roster'
import type { MaxMembersView } from './types'

// The cap stepper's write path: absolute value, one request per team, no optimistic value, failures toast.

const H = 'host-a'
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const team = (id: string, over: Partial<TeamRoster> = {}): TeamRoster => ({
  id, host_id: 'd', created_at: 1, team_name: '', team_label: '', lead: sess(`lead-${id}`), members: [], max_members: 2, in_use: 1, ...over,
})
const target = { hostId: H, teamId: 't1', label: 'lead-1' }

interface Call { hostId: string; teamId: string; value: number; resolve: (v: MaxMembersView) => void; reject: (e: unknown) => void }
let calls: Call[]
let toasts: string[]
let identity: string | null

beforeEach(() => {
  resetMaxMembers()
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply(H, [team('t1'), team('t2')])
  calls = []
  toasts = []
  identity = 'ep1:tok'
  configureMaxMembers({
    put: (hostId, teamId, value) => new Promise<MaxMembersView>((resolve, reject) => { calls.push({ hostId, teamId, value, resolve, reject }) }),
    toast: (m) => { toasts.push(m) },
    message: (key, params) => `${key}|${Object.entries(params).map(([k, v]) => `${k}=${v}`).join(',')}`,
    hostLabel: () => 'mlab',
    identity: () => identity,
  })
})

const flush = () => new Promise((r) => setTimeout(r, 0))
const capOf = (id: string) => useTeamRosterStore.getState().byHost[H].find((t) => t.id === id)

describe('setMaxMembers', () => {
  it('sends the absolute value at once (no coalescing, no optimistic value)', async () => {
    void setMaxMembers(target, 3)
    expect(calls).toHaveLength(1)
    expect(calls[0]).toMatchObject({ hostId: H, teamId: 't1', value: 3 })
    expect(capOf('t1')?.max_members).toBe(2) // unchanged until the daemon answers
    expect(useMaxMembersStore.getState().inflight[teamKey(H, 't1')]).toMatchObject({ identity: 'ep1:tok' })
  })

  it('applies the answer to the roster (max_members and in_use) and ends the flight', async () => {
    const p = setMaxMembers(target, 3)
    calls[0].resolve({ team_id: 't1', max_members: 3, in_use: 1 })
    await p
    expect(capOf('t1')).toMatchObject({ max_members: 3, in_use: 1 })
    expect(capOf('t2')).toMatchObject({ max_members: 2 }) // another team untouched
    expect(useMaxMembersStore.getState().inflight).toEqual({})
    expect(toasts).toEqual([])
  })

  it('one request per team: a second click while one is out sends nothing', async () => {
    void setMaxMembers(target, 3)
    void setMaxMembers(target, 4)
    expect(calls).toHaveLength(1)
    calls[0].resolve({ team_id: 't1', max_members: 3, in_use: 1 })
    await flush()
    void setMaxMembers(target, 4) // free again
    expect(calls).toHaveLength(2)
    expect(calls[1].value).toBe(4)
  })

  it('another team is not held up by the first', () => {
    void setMaxMembers(target, 3)
    void setMaxMembers({ hostId: H, teamId: 't2', label: 'lead-2' }, 4)
    expect(calls.map((c) => c.teamId)).toEqual(['t1', 't2'])
  })

  it('a roster event that arrived while the request was out is newer than the answer: the answer is not put over it', async () => {
    const p = setMaxMembers(target, 3)
    // the daemon's `changed` event (for this very write, plus a member who joined) reaches the roster first
    useTeamRosterStore.getState().apply(H, [team('t1', { max_members: 3, in_use: 2 }), team('t2')])
    calls[0].resolve({ team_id: 't1', max_members: 3, in_use: 1 })
    await p
    expect(capOf('t1')).toMatchObject({ max_members: 3, in_use: 2 })
  })

  it('409 max_below_in_use toasts the daemon\'s member count', async () => {
    const p = setMaxMembers(target, 1)
    calls[0].reject(new ApprovalApiError(409, 'max_below_in_use', 'x', null, { in_use: 3 }))
    await p
    expect(toasts).toEqual(['unattended.cap.below|n=3'])
    expect(capOf('t1')?.max_members).toBe(2)
    expect(useMaxMembersStore.getState().inflight).toEqual({})
  })

  it('409 without a count still toasts, with a placeholder', async () => {
    const p = setMaxMembers(target, 1)
    calls[0].reject(new ApprovalApiError(409, 'max_below_in_use'))
    await p
    expect(toasts).toEqual(['unattended.cap.below|n=?'])
  })

  it('404 not_found says the team ended', async () => {
    const p = setMaxMembers(target, 3)
    calls[0].reject(new ApprovalApiError(404, 'not_found'))
    await p
    expect(toasts).toEqual(['unattended.cap.ended|'])
  })

  it('any other failure toasts with the host, the lead\'s name and the code', async () => {
    for (const [e, code] of [[new ApprovalApiError(0, 'network'), 'network'], [new ApprovalApiError(400, 'bad_request'), 'bad_request'], [new Error('boom'), 'error']] as const) {
      toasts.length = 0
      calls.length = 0
      const p = setMaxMembers(target, 3)
      calls[0].reject(e)
      await p
      expect(toasts).toEqual([`unattended.cap.failed|host=mlab,session=lead-1,code=${code}`])
    }
  })

  it('a host that is gone sends nothing', () => {
    identity = null
    void setMaxMembers(target, 3)
    expect(calls).toHaveLength(0)
    expect(useMaxMembersStore.getState().inflight).toEqual({})
  })

  it('an answer or a failure from a host that was re-pointed meanwhile is the old daemon\'s: ignored, flight ended', async () => {
    const ok = setMaxMembers(target, 3)
    identity = 'ep2:tok'
    calls[0].resolve({ team_id: 't1', max_members: 3, in_use: 1 })
    await ok
    expect(capOf('t1')?.max_members).toBe(2)
    identity = 'ep1:tok'
    const bad = setMaxMembers(target, 3)
    identity = 'ep3:tok'
    calls[1].reject(new ApprovalApiError(409, 'max_below_in_use', '', null, { in_use: 3 }))
    await bad
    expect(toasts).toEqual([])
    expect(useMaxMembersStore.getState().inflight).toEqual({})
  })

  it('a request that went to the old daemon does not hold the team on the new one, and cannot end the new one\'s flight', async () => {
    const old = setMaxMembers(target, 3) // out to ep1, never answers yet
    identity = 'ep2:tok' // re-pointed ...
    useMaxMembersStore.getState().forgetHost(H) // ... and the host's state forgotten, as unattended-support does
    expect(useMaxMembersStore.getState().inflight).toEqual({})
    void setMaxMembers(target, 4) // the new daemon takes a request at once
    expect(calls).toHaveLength(2)
    expect(calls[1].value).toBe(4)
    calls[0].resolve({ team_id: 't1', max_members: 3, in_use: 1 }) // the old one finally settles
    await old
    expect(useMaxMembersStore.getState().inflight[teamKey(H, 't1')]).toMatchObject({ identity: 'ep2:tok' }) // the new flight stands
    void setMaxMembers(target, 5)
    expect(calls).toHaveLength(2) // and still holds the team
  })

  it('even without a forget, an entry left by an earlier daemon is replaced by a request to the current one', async () => {
    void setMaxMembers(target, 3)
    identity = 'ep2:tok'
    void setMaxMembers(target, 4)
    expect(calls).toHaveLength(2)
  })

  it('forgetHost clears only that host\'s flights', () => {
    useMaxMembersStore.getState().begin(teamKey(H, 't1'), { token: 1, identity: 'a' })
    useMaxMembersStore.getState().begin(teamKey('host-b', 't1'), { token: 2, identity: 'b' })
    useMaxMembersStore.getState().forgetHost(H)
    expect(Object.keys(useMaxMembersStore.getState().inflight)).toEqual([teamKey('host-b', 't1')])
  })

  it('an answer for a team the roster no longer has changes nothing', async () => {
    const p = setMaxMembers(target, 3)
    useTeamRosterStore.getState().apply(H, [team('t2')])
    calls[0].resolve({ team_id: 't1', max_members: 3, in_use: 1 })
    await p
    expect(useTeamRosterStore.getState().byHost[H].map((t) => t.id)).toEqual(['t2'])
  })

  it('uses the real writer by default (the seam is only for tests)', async () => {
    resetMaxMembers()
    const spy = vi.fn()
    configureMaxMembers({ identity: () => null, toast: spy })
    await setMaxMembers(target, 3) // no host: nothing is sent, nothing is toasted
    expect(spy).not.toHaveBeenCalled()
  })
})

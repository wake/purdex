// spa/src/hooks/useSessionSeat.test.ts — who a sidebar session row is (name pool v2 spec §9.6, NA-5b): the persona behind a (host, session code), from what the App already holds and without asking for
// anything. The team roster's seat first (the lead and the members of the host's own teams, by tmux name and generation), then the host's cached /api/peers row (only when its tmux generation is the
// session's own; an unknown on either side is never a match). Neither knows it, or the address ends in no persona's code → null and the row keeps its agent icon. Reactive: a store update changes the answer.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useSessionSeat } from './useSessionSeat'
import { usePeerStore, emptyPeerHostEntry, type PeerRow } from '../stores/usePeerStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useSessionStore } from '../stores/useSessionStore'
import { usePersonaStore } from '../stores/usePersonaStore'
import { makeCollection } from '../lib/names/__fixtures__/personas'
import type { RosterSession, TeamRoster } from '../lib/team/roster'

const H = 'host-a'
const INSTANCE = '4242:1700000000'
const session = (code: string, name: string, tmux_instance: string | undefined = INSTANCE) => ({ code, name, tmux_instance }) as never

const peerRow = (address: string, tmuxInstance = INSTANCE): PeerRow => ({ address, ref: '_abc123', title: '', titleSource: '', deliverable: true, reason: '', tmuxInstance, agent: null })
const setPeers = (rows: Record<string, PeerRow>) => act(() => { usePeerStore.setState({ byHost: { [H]: { ...emptyPeerHostEntry(), rows, fetchedAt: 1 } } }) })

const sess = (id: string, over: Partial<RosterSession> = {}): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/proj-${id}`, live: true, ...over })
const team = (lead: RosterSession, members: RosterSession[] = []): TeamRoster => ({ id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '', lead, members: members.map((m) => ({ ...m, state: 'active', origin: 'spawned', joined_at: 1 })) })

beforeEach(() => {
  usePeerStore.setState({ byHost: {} })
  useTeamRosterStore.getState().reset()
  useSessionStore.setState({ sessions: { [H]: [session('S1', 'proj-1'), session('S2', 'proj-2')] } })
  usePersonaStore.setState({ byHost: { [H]: { status: 'ready', collection: makeCollection(['freya', 'kenji']) } } } as never)
})

describe('useSessionSeat', () => {
  it('the host\'s cached peers row, when its tmux generation is the session\'s own → the persona', () => {
    setPeers({ S1: peerRow('mlab/proj-1-freya') })
    const { result } = renderHook(() => useSessionSeat(H, 'S1'))
    expect(result.current).toMatchObject({ code: 'freya', name: 'Freya' })
  })

  it.each([
    ['the generations differ', peerRow('mlab/proj-1-freya', '9999:1')],
    ['the row\'s generation is unknown', peerRow('mlab/proj-1-freya', '')],
  ])('%s → null (an unknown is never a match)', (_n, row) => {
    setPeers({ S1: row })
    expect(renderHook(() => useSessionSeat(H, 'S1')).result.current).toBeNull()
  })

  it('the session\'s own generation unknown → null', () => {
    useSessionStore.setState({ sessions: { [H]: [session('S1', 'proj-1', '')] } })
    setPeers({ S1: peerRow('mlab/proj-1-freya') })
    expect(renderHook(() => useSessionSeat(H, 'S1')).result.current).toBeNull()
  })

  it('an address that ends in no persona\'s code, no row, no host entry, and no code → null', () => {
    setPeers({ S1: peerRow('mlab/proj-1-nobody') })
    expect(renderHook(() => useSessionSeat(H, 'S1')).result.current).toBeNull()
    expect(renderHook(() => useSessionSeat(H, 'S2')).result.current).toBeNull()
    expect(renderHook(() => useSessionSeat('other-host', 'S1')).result.current).toBeNull()
    expect(renderHook(() => useSessionSeat(H, undefined)).result.current).toBeNull()
  })

  it('the team roster\'s seat (by tmux name) is the persona too, and beats the peers row', () => {
    useTeamRosterStore.getState().apply(H, [team(sess('L', { tmux_session: 'proj-1', tmux_instance: INSTANCE, address: 'mlab/proj-1-kenji' }))])
    setPeers({ S1: peerRow('mlab/proj-1-freya') })
    expect(renderHook(() => useSessionSeat(H, 'S1')).result.current).toMatchObject({ code: 'kenji' })
  })

  it('a roster member is found the same way; a seat of another tmux generation is not used', () => {
    useTeamRosterStore.getState().apply(H, [team(sess('L', { tmux_session: 'x' }), [sess('M', { tmux_session: 'proj-2', tmux_instance: INSTANCE, address: 'mlab/proj-2-freya' })])])
    expect(renderHook(() => useSessionSeat(H, 'S2')).result.current).toMatchObject({ code: 'freya' })
    useTeamRosterStore.getState().apply(H, [team(sess('L', { tmux_session: 'x' }), [sess('M', { tmux_session: 'proj-2', tmux_instance: '9999:1', address: 'mlab/proj-2-freya' })])])
    expect(renderHook(() => useSessionSeat(H, 'S2')).result.current).toBeNull()
  })

  it('follows the stores: a peers answer that lands later, and a roster that changes, change the answer', () => {
    const { result } = renderHook(() => useSessionSeat(H, 'S1'))
    expect(result.current).toBeNull()
    setPeers({ S1: peerRow('mlab/proj-1-freya') })
    expect(result.current).toMatchObject({ code: 'freya' })
    act(() => { useTeamRosterStore.getState().apply(H, [team(sess('L', { tmux_session: 'proj-1', tmux_instance: INSTANCE, address: 'mlab/proj-1-kenji' }))]) })
    expect(result.current).toMatchObject({ code: 'kenji' })
  })

  it('never asks for anything: no peers refresh', () => {
    const refresh = vi.spyOn(usePeerStore.getState(), 'refresh')
    setPeers({ S1: peerRow('mlab/proj-1-freya') })
    renderHook(() => useSessionSeat(H, 'S1'))
    expect(refresh).not.toHaveBeenCalled()
    refresh.mockRestore()
  })
})

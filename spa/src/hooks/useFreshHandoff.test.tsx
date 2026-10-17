// spa/src/hooks/useFreshHandoff.test.tsx — which row has a handoff file written (relay v2 contract 5): a tab is matched by
// (hostId, code) and by nothing else; a team row reads the roster's own `fresh_handoff`; the mark follows the stores.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { freshByCode, useFreshHandoffOfRosterSession, useFreshHandoffOfSeat, useFreshHandoffOfTab } from './useFreshHandoff'
import { useRelayFreshStore } from '../stores/useRelayFreshStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { HOST, KEY, resetTeamStores, seedScene, sess, tabOn } from '../lib/team/__tests__/team-fixture'
import type { RelayFresh } from '../lib/relay/fresh'
import type { Tab } from '../types/tab'

const entry = (over: Partial<RelayFresh> = {}): RelayFresh => ({ session_id: 'S1', ref: '_s1', op_id: 'op1', path: '/w/handoff.md', written_at: 5000, code: 'code-x-tm', ...over })
const put = (list: RelayFresh[], host = HOST) => act(() => useRelayFreshStore.getState().apply(host, list))
const view = { path: '/w/handoff.md', writtenAt: 5000 }

beforeEach(() => {
  useRelayFreshStore.getState().reset()
  resetTeamStores()
})

describe('freshByCode', () => {
  it('matches the code exactly, takes the last written of a repeated code, never matches an empty code', () => {
    const list = [entry({ code: 'a', written_at: 1, path: '/old' }), entry({ code: 'a', written_at: 9, path: '/new' }), entry({ code: '' as never }), entry({ code: undefined })]
    expect(freshByCode(list, 'a')?.path).toBe('/new')
    expect(freshByCode(list, 'b')).toBeUndefined()
    expect(freshByCode(list, '')).toBeUndefined()
    expect(freshByCode(undefined, 'a')).toBeUndefined()
  })
})

describe('useFreshHandoffOfTab', () => {
  const tab = tabOn('t', 'x-tm') // host h1, code code-x-tm

  it('is the list entry with the tab\'s (hostId, code)', () => {
    put([entry()])
    expect(renderHook(() => useFreshHandoffOfTab(tab)).result.current).toEqual(view)
  })

  it('follows the list: the mark is gone the moment the entry is', () => {
    put([entry()])
    const { result } = renderHook(() => useFreshHandoffOfTab(tab))
    expect(result.current).toEqual(view)
    put([])
    expect(result.current).toBeNull()
  })

  it('an entry without a code never marks a tab, even when its session id, ref or name look like the tab\'s', () => {
    // the tab's tmux name is x-tm and its code code-x-tm; the entry names the session in every other way it can
    put([entry({ code: undefined, session_id: 'code-x-tm', ref: 'code-x-tm', op_id: 'x-tm' })])
    expect(renderHook(() => useFreshHandoffOfTab(tab)).result.current).toBeNull()
  })

  it('another code, or the same code on another host, is not this tab', () => {
    put([entry({ code: 'code-other' })])
    expect(renderHook(() => useFreshHandoffOfTab(tab)).result.current).toBeNull()
    put([entry()], 'h2')
    put([])
    expect(renderHook(() => useFreshHandoffOfTab(tab)).result.current).toBeNull()
  })

  it('a tab that shows no tmux session, a terminated one, and one with no code are never marked', () => {
    put([entry({ code: '' as never }), entry({ code: 'code-x-tm' })])
    const none = tabOn('n', null)
    const base = tabOn('b', 'x-tm')
    const pane = (base.layout as { pane: { id: string; content: Record<string, unknown> } }).pane
    const terminated = { ...base, layout: { type: 'leaf', pane: { ...pane, content: { ...pane.content, terminated: 'session-closed' } } } } as unknown as Tab
    const noCode = { ...base, layout: { type: 'leaf', pane: { ...pane, content: { ...pane.content, sessionCode: '' } } } } as unknown as Tab
    expect(renderHook(() => useFreshHandoffOfTab(none)).result.current).toBeNull()
    expect(renderHook(() => useFreshHandoffOfTab(terminated)).result.current).toBeNull()
    expect(renderHook(() => useFreshHandoffOfTab(noCode)).result.current).toBeNull()
  })

  it('a team\'s lead row reads the roster, so a list entry without a code still marks it', () => {
    seedScene({ tabs: [['lead', 'lead-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }] })
    const leadTab = tabOn('lead', 'lead-tm')
    expect(renderHook(() => useFreshHandoffOfTab(leadTab, KEY)).result.current).toBeNull()
    const teams = useTeamRosterStore.getState().byHost[HOST]
    act(() => useTeamRosterStore.getState().apply(HOST, [{ ...teams[0], lead: { ...teams[0].lead, fresh_handoff: { op_id: 'o', path: '/lead.md', written_at: 7 } } }]))
    expect(renderHook(() => useFreshHandoffOfTab(leadTab, KEY)).result.current).toEqual({ path: '/lead.md', writtenAt: 7 })
    // without the team key the same tab asks the list only
    expect(renderHook(() => useFreshHandoffOfTab(leadTab)).result.current).toBeNull()
  })
})

describe('the roster hooks', () => {
  it('useFreshHandoffOfRosterSession reads the session\'s fresh_handoff', () => {
    const s = { ...sess('L', 'lead-tm'), fresh_handoff: { op_id: 'o', path: '/p.md', written_at: 3 } }
    expect(renderHook(() => useFreshHandoffOfRosterSession(s)).result.current).toEqual({ path: '/p.md', writtenAt: 3 })
    expect(renderHook(() => useFreshHandoffOfRosterSession(sess('L', 'lead-tm'))).result.current).toBeNull()
    expect(renderHook(() => useFreshHandoffOfRosterSession(undefined)).result.current).toBeNull()
  })

  it('useFreshHandoffOfSeat follows the roster store for a lead and for a member, and only for that seat', () => {
    seedScene({ members: [['A', 'a-tm'], ['B', 'b-tm']], tabs: [], workspaces: [] })
    const a = renderHook(() => useFreshHandoffOfSeat(KEY, 'A'))
    const b = renderHook(() => useFreshHandoffOfSeat(KEY, 'B'))
    expect(a.result.current).toBeNull()
    const team = useTeamRosterStore.getState().byHost[HOST][0]
    const withA = { ...team, members: team.members.map((m) => (m.session_id === 'A' ? { ...m, fresh_handoff: { op_id: 'o', path: '/a.md', written_at: 4 } } : m)) }
    act(() => useTeamRosterStore.getState().apply(HOST, [withA]))
    expect(a.result.current).toEqual({ path: '/a.md', writtenAt: 4 })
    expect(b.result.current).toBeNull()
    act(() => useTeamRosterStore.getState().apply(HOST, [team]))
    expect(a.result.current).toBeNull()
  })
})

// spa/src/stores/useHookCardStore.test.ts — the open hook cards (hook_ask / hook_permission) this window knows of.
// They draw no card in the Mac App (the terminal is the answer surface) but the Dock badge counts them (app-badge
// spec §1), so they are held here: a snapshot replaces the host's whole set, opened adds, closed removes.
import { beforeEach, describe, expect, it } from 'vitest'
import type { Approval } from '../lib/team/types'
import { TOMBSTONES_PER_HOST, useHookCardStore } from './useHookCardStore'

const ask = (id: string, over: Partial<Approval> = {}): Approval => ({
  id, kind: 'hook_ask', host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: '_x', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux: 'work:@1.%1' },
  payload: {}, state: 'open', created_at: 1, deadline_at: 2, lease_until: 3,
  ...over,
})
const ids = () => Object.values(useHookCardStore.getState().entries).map((e) => `${e.hostId}/${e.approval.id}`).sort()

beforeEach(() => useHookCardStore.getState().reset())

describe('useHookCardStore', () => {
  it('a snapshot replaces the host\'s whole set and leaves other hosts alone', () => {
    const s = useHookCardStore.getState()
    s.applySnapshot('h1', [ask('a'), ask('b')])
    s.applySnapshot('h2', [ask('c')])
    s.applySnapshot('h1', [ask('b'), ask('d')])
    expect(ids()).toEqual(['h1/b', 'h1/d', 'h2/c'])
  })

  it('a snapshot keeps only open rows', () => {
    useHookCardStore.getState().applySnapshot('h1', [ask('a'), ask('b', { state: 'dismissed' })])
    expect(ids()).toEqual(['h1/a'])
  })

  it('opened adds (once); closed removes', () => {
    const s = useHookCardStore.getState()
    expect(s.applyOpened('h1', ask('a'))).toBe(true)
    expect(s.applyOpened('h1', ask('a'))).toBe(false)
    expect(ids()).toEqual(['h1/a'])
    s.applyClosed('h1', ask('a', { state: 'answered_local' }))
    expect(ids()).toEqual([])
  })

  it('a closed that came before its opened is not revived by the late opened', () => {
    const s = useHookCardStore.getState()
    s.applyClosed('h1', ask('a', { state: 'dismissed' }))
    expect(s.applyOpened('h1', ask('a'))).toBe(false)
    expect(ids()).toEqual([])
  })

  it('a snapshot is authoritative: it lists a tombstoned id as open again', () => {
    const s = useHookCardStore.getState()
    s.applyClosed('h1', ask('a', { state: 'dismissed' }))
    s.applySnapshot('h1', [ask('a')])
    expect(ids()).toEqual(['h1/a'])
  })

  it('tombstones are capped per host, oldest dropped', () => {
    const s = useHookCardStore.getState()
    for (let i = 0; i <= TOMBSTONES_PER_HOST; i++) s.applyClosed('h1', ask(`t${i}`, { state: 'dismissed' }))
    expect(useHookCardStore.getState().closedIds.h1).toHaveLength(TOMBSTONES_PER_HOST)
    expect(useHookCardStore.getState().closedIds.h1).not.toContain('t0')
  })

  it('forgetHost drops that host\'s cards and tombstones only', () => {
    const s = useHookCardStore.getState()
    s.applySnapshot('h1', [ask('a')])
    s.applySnapshot('h2', [ask('b')])
    s.applyClosed('h1', ask('z', { state: 'dismissed' }))
    s.forgetHost('h1')
    expect(ids()).toEqual(['h2/b'])
    expect(useHookCardStore.getState().closedIds.h1).toBeUndefined()
  })

  it('the entry key is the shared card identity hostId NUL approvalId', () => {
    useHookCardStore.getState().applyOpened('h1', ask('a'))
    expect(Object.keys(useHookCardStore.getState().entries)).toEqual(['h1\u0000a'])
  })
})

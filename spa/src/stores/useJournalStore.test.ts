// spa/src/stores/useJournalStore.test.ts — per-unit data, merging, staleness and forgetting (J-2a T-2a.4).
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getJournal = vi.fn()
const getTasks = vi.fn()
const getRoadmap = vi.fn()
vi.mock('../lib/journal/api', () => ({
  getJournal: (...a: unknown[]) => getJournal(...a),
  getTasks: (...a: unknown[]) => getTasks(...a),
  getRoadmap: (...a: unknown[]) => getRoadmap(...a),
}))

import { MAX_UNITS, isStale, selectUnit, useJournalStore } from './useJournalStore'
import { parseJournalPage, parseRoadmap, parseTasks } from '../lib/journal/parse'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../lib/journal/view-memory'
import { T0, wireConfirm, wireEntry, wirePage, wireRoadmap, wireTasks } from '../lib/journal/fixtures'

const page = (over: Record<string, unknown> = {}) => ({ kind: 'ok', value: parseJournalPage(wirePage(over)) })
const st = () => useJournalStore.getState()
const unit = (host = 'h1', sess = 's1') => selectUnit(st(), host, sess)!
const ids = (host = 'h1', sess = 's1') => unit(host, sess).entries.map((e) => e.id)
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

beforeEach(() => {
  useJournalStore.getState().reset()
  clearViewMemos()
  getJournal.mockReset(); getTasks.mockReset(); getRoadmap.mockReset()
})

describe('entries order', () => {
  it('sorts by (at, id) whatever order the page came in, and equal times by id', async () => {
    getJournal.mockResolvedValue(page({ entries: [wireEntry('b', { at: T0 }), wireEntry('c', { at: T0 - 5 }), wireEntry('a', { at: T0 }), wireEntry('d', { at: T0 + 9 })] }))
    await st().loadJournal('h1', 's1')
    expect(ids()).toEqual(['c', 'a', 'b', 'd'])
  })

  it('a page merge keeps the order across the pages (older page, equal at with the held rows)', async () => {
    getJournal.mockResolvedValueOnce(page({ entries: [wireEntry('m2', { at: T0 }), wireEntry('m1', { at: T0 })], more: true }))
    await st().loadJournal('h1', 's1')
    getJournal.mockResolvedValueOnce(page({ entries: [wireEntry('z9', { at: T0 - 100 }), wireEntry('m0', { at: T0 })], more: false }))
    await st().loadJournal('h1', 's1', { older: true })
    expect(ids()).toEqual(['z9', 'm0', 'm1', 'm2'])
    expect(unit().more).toBe(false)
  })

  it('an older page is asked with the cursor of the oldest held entry; nothing is asked once there is no more', async () => {
    getJournal.mockResolvedValueOnce(page({ entries: [wireEntry('x', { at: Date.UTC(2026, 9, 14, 6) })], more: true }))
    await st().loadJournal('h1', 's1')
    getJournal.mockResolvedValueOnce(page({ entries: [wireEntry('w', { at: T0 - 1e9 })], more: false }))
    await st().loadJournal('h1', 's1', { older: true })
    expect(getJournal.mock.calls[1][2]).toMatch(/^2026-\d\d:x$/)
    expect(await st().loadJournal('h1', 's1', { older: true })).toEqual({ kind: 'ok' })
    expect(getJournal).toHaveBeenCalledTimes(2)
  })

  it('a re-read of the newest page keeps the older months and their "more"', async () => {
    getJournal.mockResolvedValueOnce(page({ entries: [wireEntry('b', { at: T0 })], more: true }))
    await st().loadJournal('h1', 's1')
    getJournal.mockResolvedValueOnce(page({ entries: [wireEntry('a', { at: T0 - 9 })], more: false }))
    await st().loadJournal('h1', 's1', { older: true })
    getJournal.mockResolvedValueOnce(page({ entries: [wireEntry('b', { at: T0 }), wireEntry('c', { at: T0 + 1 })], more: true }))
    await st().loadJournal('h1', 's1')
    expect(ids()).toEqual(['a', 'b', 'c'])
    expect(unit().more).toBe(false)
  })

  it('the pinned band is newest first by at, equal times by id (descending)', async () => {
    getJournal.mockResolvedValue(page({ pinned: [wireConfirm('p1', { at: T0 }), wireConfirm('p3', { at: T0 + 5 }), wireConfirm('p2', { at: T0 })], open_confirms: 3 }))
    await st().loadJournal('h1', 's1')
    expect(unit().pinned.map((e) => e.id)).toEqual(['p3', 'p2', 'p1'])
    expect(unit().openConfirms).toBe(3)
  })
})

describe('unit data', () => {
  it('a person_name that changes on a refresh replaces the old one', async () => {
    getJournal.mockResolvedValueOnce(page({ person_name: 'Wake' }))
    await st().loadJournal('h1', 's1')
    expect(unit().personName).toBe('Wake')
    getJournal.mockResolvedValueOnce(page({ person_name: '小明' }))
    await st().loadJournal('h1', 's1')
    expect(unit().personName).toBe('小明')
    getJournal.mockResolvedValueOnce(page({ person_name: undefined }))
    await st().loadJournal('h1', 's1')
    expect(unit().personName).toBe('')
  })

  it('two sessions of one unit (a lead and a member) share it; units of other hosts are separate', async () => {
    getJournal.mockResolvedValue(page())
    await st().loadJournal('h1', 's1')
    await st().loadJournal('h1', 's2')
    await st().loadJournal('h2', 's1')
    expect(unit('h1', 's1')).toBe(unit('h1', 's2'))
    expect(Object.keys(st().byHost.h1.units)).toEqual(['team:tm1'])
    expect(unit('h2', 's1')).not.toBe(unit('h1', 's1'))
  })

  it('reads tasks and roadmap into the unit once its page is held; before that there is no unit', async () => {
    expect(await st().loadTasks('h1', 's1')).toEqual({ kind: 'no_unit' })
    getJournal.mockResolvedValue(page())
    await st().loadJournal('h1', 's1')
    getTasks.mockResolvedValue({ kind: 'ok', value: parseTasks(wireTasks()) })
    getRoadmap.mockResolvedValue({ kind: 'ok', value: parseRoadmap(wireRoadmap()) })
    await st().loadTasks('h1', 's1')
    await st().loadRoadmap('h1', 's1')
    expect(unit().tasks).toHaveLength(3)
    expect(unit().roadmap?.[0].name).toBe('Journal')
  })

  it('none, remote and failure are results and write nothing', async () => {
    getJournal.mockResolvedValueOnce({ kind: 'none' })
    expect(await st().loadJournal('h1', 's1')).toEqual({ kind: 'none' })
    getJournal.mockResolvedValueOnce({ kind: 'remote', leadHostId: 'd', leadSessionId: 'x' })
    expect(await st().loadJournal('h1', 's1')).toEqual({ kind: 'remote', leadHostId: 'd', leadSessionId: 'x' })
    getJournal.mockRejectedValueOnce(new Error('boom'))
    expect(await st().loadJournal('h1', 's1')).toEqual({ kind: 'failed' })
    expect(st().byHost.h1).toBeUndefined()
  })

  it('keeps at most MAX_UNITS units per host, the least recently touched out first', async () => {
    for (let i = 0; i < MAX_UNITS + 3; i++) {
      getJournal.mockResolvedValueOnce(page({ unit: { kind: 'solo', id: `u${i}`, label: 'L' } }))
      await st().loadJournal('h1', `s${i}`)
    }
    expect(Object.keys(st().byHost.h1.units)).toHaveLength(MAX_UNITS)
    expect(selectUnit(st(), 'h1', 's0')).toBeUndefined()
    expect(selectUnit(st(), 'h1', `s${MAX_UNITS + 2}`)).toBeDefined()
  })
})

describe('journal.changed → stale flags only', () => {
  const loaded = async () => {
    getJournal.mockResolvedValue(page())
    getTasks.mockResolvedValue({ kind: 'ok', value: [] })
    getRoadmap.mockResolvedValue({ kind: 'ok', value: [] })
    await st().loadJournal('h1', 's1')
    await st().loadTasks('h1', 's1')
    await st().loadRoadmap('h1', 's1')
  }

  it('what: tasks marks the tasks only', async () => {
    await loaded()
    st().markStale('h1', 'team:tm1', 'tasks')
    expect([isStale(unit(), 'entries'), isStale(unit(), 'tasks'), isStale(unit(), 'roadmap')]).toEqual([false, true, false])
  })

  it('what: roadmap marks every unit of the host (the roadmap is the project\'s)', async () => {
    await loaded()
    getJournal.mockResolvedValue(page({ unit: { kind: 'solo', id: 'other', label: 'L' } }))
    await st().loadJournal('h1', 's2')
    st().markStale('h1', 'team:tm1', 'roadmap')
    expect(isStale(unit('h1', 's1'), 'roadmap')).toBe(true)
    expect(isStale(unit('h1', 's2'), 'roadmap')).toBe(true)
  })

  it('an event for a unit not held, or a host not held, changes nothing and does not bump the revision', async () => {
    await loaded()
    const rev = st().staleRev
    st().markStale('h1', 'solo:nope', 'entries')
    st().markStale('h9', 'team:tm1', 'entries')
    expect(st().staleRev).toBe(rev)
    expect(isStale(unit(), 'entries')).toBe(false)
  })

  it('a read that started before the event leaves it stale; one that starts after clears it', async () => {
    await loaded()
    const d = deferred<unknown>()
    getJournal.mockReturnValueOnce(d.promise)
    const inflight = st().loadJournal('h1', 's1')
    st().markStale('h1', 'team:tm1', 'entries') // lands while the read is out
    d.resolve(page())
    await inflight
    expect(isStale(unit(), 'entries')).toBe(true)
    getJournal.mockResolvedValueOnce(page())
    await st().loadJournal('h1', 's1')
    expect(isStale(unit(), 'entries')).toBe(false)
  })

  it('an older page does not clear the entries flag', async () => {
    await loaded()
    getJournal.mockResolvedValueOnce(page({ more: true }))
    await st().loadJournal('h1', 's1')
    st().markStale('h1', 'team:tm1', 'entries')
    getJournal.mockResolvedValueOnce(page({ entries: [wireEntry('old', { at: 1 })] }))
    await st().loadJournal('h1', 's1', { older: true })
    expect(isStale(unit(), 'entries')).toBe(true)
  })
})

describe('forgetting and fencing', () => {
  it('forgetHost drops the host\'s units, routes, view memos and support, and leaves other hosts alone', async () => {
    getJournal.mockResolvedValue(page())
    await st().loadJournal('h1', 's1')
    await st().loadJournal('h2', 's1')
    st().setSupport('h1', true)
    st().setRoute('h1', 's1', { kind: 'follow', hostId: 'h2', sessionId: 'x', leadHostId: 'd2' })
    st().setRoute('h2', 's9', { kind: 'follow', hostId: 'h1', sessionId: 'y', leadHostId: 'd1' })
    patchViewMemo('h1', 'team:tm1', { journalOpen: true })
    patchViewMemo('h2', 'team:tm1', { journalOpen: true })
    st().forgetHost('h1')
    expect(st().byHost.h1).toBeUndefined()
    expect(st().sticky.h1).toBeUndefined()
    expect(st().routes).toEqual({}) // h1's own route and h2's route that followed h1
    expect(readViewMemo('h1', 'team:tm1').journalOpen).toBe(false)
    expect(readViewMemo('h2', 'team:tm1').journalOpen).toBe(true)
    expect(st().byHost.h2).toBeDefined()
  })

  it('a read still out when its host is forgotten does not land', async () => {
    const d = deferred<unknown>()
    getJournal.mockReturnValueOnce(d.promise)
    const inflight = st().loadJournal('h1', 's1')
    st().forgetHost('h1')
    d.resolve(page())
    expect(await inflight).toEqual({ kind: 'dropped' })
    expect(st().byHost.h1).toBeUndefined()
  })

  it('a read still out when the connection ends does not land; the held data stays', async () => {
    getJournal.mockResolvedValueOnce(page())
    await st().loadJournal('h1', 's1')
    const d = deferred<unknown>()
    getJournal.mockReturnValueOnce(d.promise)
    const inflight = st().loadJournal('h1', 's1')
    st().fence('h1')
    d.resolve(page({ entries: [wireEntry('late')] }))
    expect(await inflight).toEqual({ kind: 'dropped' })
    expect(ids()).not.toContain('late')
    expect(unit()).toBeDefined()
  })
  it('an older request of the same resource finishing after a newer one does not overwrite it', async () => {
    const d1 = deferred<unknown>(); const d2 = deferred<unknown>()
    getJournal.mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise)
    const first = st().loadJournal('h1', 's1')
    const second = st().loadJournal('h1', 's1')
    d2.resolve(page({ entries: [wireEntry('new')] }))
    expect(await second).toEqual({ kind: 'ok' })
    d1.resolve(page({ entries: [wireEntry('old')] }))
    expect(await first).toEqual({ kind: 'dropped' })
    expect(ids()).toEqual(['new'])
  })
  it('the newest read of a unit wins across the sessions that share it', async () => {
    getJournal.mockResolvedValue(page())
    await st().loadJournal('h1', 's1')
    await st().loadJournal('h1', 's2')
    const d1 = deferred<unknown>(); const d2 = deferred<unknown>()
    getTasks.mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise)
    const first = st().loadTasks('h1', 's1')
    const second = st().loadTasks('h1', 's2')
    d2.resolve({ kind: 'ok', value: parseTasks(wireTasks()) })
    expect(await second).toEqual({ kind: 'ok' })
    d1.resolve({ kind: 'ok', value: [] })
    expect(await first).toEqual({ kind: 'dropped' })
    expect(unit('h1', 's1').tasks).toEqual(parseTasks(wireTasks()))
  })
  it('a tasks read whose session was remapped to another unit meanwhile does not land on the new unit', async () => {
    getJournal.mockResolvedValueOnce(page())
    await st().loadJournal('h1', 's1')
    const d = deferred<unknown>()
    getTasks.mockReturnValueOnce(d.promise)
    const inflight = st().loadTasks('h1', 's1')
    getJournal.mockResolvedValueOnce(page({ unit: { kind: 'team', id: 'tm2', label: 'Other' } }))
    await st().loadJournal('h1', 's1')
    d.resolve({ kind: 'ok', value: parseTasks(wireTasks()) })
    await inflight
    expect(unit('h1', 's1').tasks).toBeNull()
  })
})

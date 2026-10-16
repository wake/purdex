// spa/src/stores/useJournalStore.pin.test.ts — optimistic pin / unpin with a mutation generation per entry (J-2c, plan §2 "Pins"):
// the screen follows the click at once; a failed PUT reverts only when that mutation's value is still what the entry shows; a
// server read that lands later always wins.
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getJournal = vi.fn()
const setPinned = vi.fn()
vi.mock('../lib/journal/api', () => ({
  getJournal: (...a: unknown[]) => getJournal(...a),
  getTasks: vi.fn(),
  getRoadmap: vi.fn(),
  setPinned: (...a: unknown[]) => setPinned(...a),
}))

import { selectUnit, useJournalStore } from './useJournalStore'
import { useUndoToast } from './useUndoToast'
import { parseJournalPage } from '../lib/journal/parse'
import { clearViewMemos } from '../lib/journal/view-memory'
import { T0, wireConfirm, wireEntry, wirePage } from '../lib/journal/fixtures'

const page = (over: Record<string, unknown> = {}) => ({ kind: 'ok', value: parseJournalPage(wirePage(over)) })
const st = () => useJournalStore.getState()
const unit = () => selectUnit(st(), 'h1', 's1')!
const band = () => unit().pinned.map((e) => e.id)
const shown = (id: string) => unit().entries.find((e) => e.id === id)!.pinned
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const OK = { kind: 'ok', value: true }
const toast = () => useUndoToast.getState().toast?.message ?? null

beforeEach(async () => {
  useJournalStore.getState().reset()
  clearViewMemos()
  getJournal.mockReset(); setPinned.mockReset()
  useUndoToast.setState({ toast: null, notice: null })
  getJournal.mockResolvedValue(page({
    entries: [wireEntry('a', { at: T0 }), wireEntry('b', { at: T0 + 10 }), wireConfirm('c', { at: T0 + 5 })],
    pinned: [wireConfirm('c', { at: T0 + 5 })],
  }))
  await st().loadJournal('h1', 's1')
})

describe('setPin', () => {
  it('shows the new state before the PUT answers, and the band stays newest first', async () => {
    const d = deferred<unknown>()
    setPinned.mockReturnValue(d.promise)
    const p = st().setPin('h1', 's1', 'b', true)
    expect(shown('b')).toBe(true)
    expect(band()).toEqual(['b', 'c'])
    expect(setPinned).toHaveBeenCalledWith('h1', 's1', 'b', true)
    d.resolve(OK)
    await p
    expect(shown('b')).toBe(true)
    expect(toast()).toBeNull()
  })
  it('an unpin takes it out of the band (a 待確認 too; its items are untouched)', async () => {
    setPinned.mockResolvedValue(OK)
    await st().setPin('h1', 's1', 'c', false)
    expect(shown('c')).toBe(false)
    expect(band()).toEqual([])
    expect(unit().entries.find((e) => e.id === 'c')!.items).toEqual([{ text: 'git?', done: true }, { text: 'tasks?', done: false }])
  })
  it('a failed PUT puts it back and says so', async () => {
    setPinned.mockRejectedValue(new Error('boom'))
    await st().setPin('h1', 's1', 'b', true)
    expect(shown('b')).toBe(false)
    expect(band()).toEqual(['c'])
    expect(toast()).toBeTruthy()
  })
  it('an answer that is not ok (404 / 409) counts as a failure', async () => {
    setPinned.mockResolvedValue({ kind: 'none' })
    await st().setPin('h1', 's1', 'b', true)
    expect(shown('b')).toBe(false)
    expect(toast()).toBeTruthy()
  })
  it('pin then quickly unpin, the first PUT fails late: the unpin is not undone (M: unconditional revert)', async () => {
    const first = deferred<unknown>()
    setPinned.mockReturnValueOnce(first.promise).mockResolvedValueOnce(OK)
    const p1 = st().setPin('h1', 's1', 'b', true)
    const p2 = st().setPin('h1', 's1', 'b', false)
    await p2
    expect(shown('b')).toBe(false)
    first.reject(new Error('late'))
    await p1
    expect(shown('b')).toBe(false)
    expect(band()).toEqual(['c'])
    expect(toast()).toBeNull()
  })
  it('pin, unpin, pin again: the first PUT failing late does not undo the last click (M: unconditional revert)', async () => {
    const first = deferred<unknown>()
    setPinned.mockReturnValueOnce(first.promise).mockResolvedValue(OK)
    const p1 = st().setPin('h1', 's1', 'b', true)
    await st().setPin('h1', 's1', 'b', false)
    await st().setPin('h1', 's1', 'b', true)
    // the PUTs overlapped, so the unit is read once more at the end: the server holds the last click
    getJournal.mockResolvedValue(page({
      entries: [wireEntry('a', { at: T0 }), wireEntry('b', { at: T0 + 10, pinned: true }), wireConfirm('c', { at: T0 + 5 })],
      pinned: [wireEntry('b', { at: T0 + 10, pinned: true }), wireConfirm('c', { at: T0 + 5 })],
    }))
    first.reject(new Error('late'))
    await p1
    expect(shown('b')).toBe(true)
    expect(band()).toEqual(['b', 'c'])
    expect(toast()).toBeNull()
  })
  it('the first PUT fails while the unpin is still in flight: the unpin keeps the display (M: no generation check)', async () => {
    const first = deferred<unknown>()
    const second = deferred<unknown>()
    setPinned.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const p1 = st().setPin('h1', 's1', 'b', true)
    const p2 = st().setPin('h1', 's1', 'b', false)
    first.reject(new Error('late'))
    await p1
    expect(shown('b')).toBe(false)
    expect(toast()).toBeNull()
    second.resolve(OK)
    await p2
    expect(shown('b')).toBe(false)
  })
  it('both fail: the entry ends as it was before the first click', async () => {
    const first = deferred<unknown>()
    const second = deferred<unknown>()
    setPinned.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const p1 = st().setPin('h1', 's1', 'b', true)
    const p2 = st().setPin('h1', 's1', 'b', false)
    first.reject(new Error('x'))
    await p1
    second.reject(new Error('y'))
    await p2
    expect(shown('b')).toBe(false)
    expect(band()).toEqual(['c'])
  })
  it('a server read that lands while the pin is pending wins over a later failure (M: revert after the read)', async () => {
    const d = deferred<unknown>()
    setPinned.mockReturnValue(d.promise)
    const p = st().setPin('h1', 's1', 'b', true)
    getJournal.mockResolvedValue(page({
      entries: [wireEntry('a', { at: T0 }), wireEntry('b', { at: T0 + 10, pinned: true }), wireConfirm('c', { at: T0 + 5 })],
      pinned: [wireEntry('b', { at: T0 + 10, pinned: true }), wireConfirm('c', { at: T0 + 5 })],
    }))
    await st().loadJournal('h1', 's1')
    expect(shown('b')).toBe(true)
    d.reject(new Error('late'))
    await p
    expect(shown('b')).toBe(true)
    expect(band()).toEqual(['b', 'c'])
  })
  it('a server read that says the opposite replaces the optimistic value, and a later success does not bring it back', async () => {
    const d = deferred<unknown>()
    setPinned.mockReturnValue(d.promise)
    const p = st().setPin('h1', 's1', 'b', true)
    expect(shown('b')).toBe(true)
    await st().loadJournal('h1', 's1') // the fixture page: b is not pinned
    expect(shown('b')).toBe(false)
    d.resolve(OK)
    await p
    expect(shown('b')).toBe(false)
  })
  it('a read sent BEFORE the click that lands after it carries the old state: the click stays, also after the PUT succeeds (M: unconditional drop)', async () => {
    const read = deferred<unknown>()
    getJournal.mockReturnValueOnce(read.promise)
    const rp = st().loadJournal('h1', 's1') // out before the click
    const put = deferred<unknown>()
    setPinned.mockReturnValue(put.promise)
    const p = st().setPin('h1', 's1', 'b', true)
    read.resolve(page({
      entries: [wireEntry('a', { at: T0 }), wireEntry('b', { at: T0 + 10 }), wireConfirm('c', { at: T0 + 5 })],
      pinned: [wireConfirm('c', { at: T0 + 5 })],
    }))
    await rp
    expect(shown('b')).toBe(true)
    expect(band()).toEqual(['b', 'c'])
    put.resolve(OK)
    await p
    expect(shown('b')).toBe(true)
    expect(band()).toEqual(['b', 'c'])
  })
  it('a read sent AFTER the click wins, and a later success does not bring the click back', async () => {
    const put = deferred<unknown>()
    setPinned.mockReturnValue(put.promise)
    const p = st().setPin('h1', 's1', 'b', true)
    await st().loadJournal('h1', 's1') // started after the click: b is not pinned
    expect(shown('b')).toBe(false)
    put.resolve(OK)
    await p
    expect(shown('b')).toBe(false)
  })
  it('two PUTs of one entry that finish in the opposite order: the unit is read once more after the last one (M: no re-read)', async () => {
    const first = deferred<unknown>()
    const second = deferred<unknown>()
    setPinned.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    getJournal.mockClear()
    const p1 = st().setPin('h1', 's1', 'b', true)
    const p2 = st().setPin('h1', 's1', 'b', false)
    second.resolve(OK)
    await p2
    expect(getJournal).not.toHaveBeenCalled() // the first PUT is still out
    first.resolve(OK)
    await p1
    expect(getJournal).toHaveBeenCalledTimes(1)
    expect(getJournal).toHaveBeenCalledWith('h1', 's1', undefined)
  })
  it('a lone PUT does not read again', async () => {
    setPinned.mockResolvedValue(OK)
    getJournal.mockClear()
    await st().setPin('h1', 's1', 'b', true)
    expect(getJournal).not.toHaveBeenCalled()
  })
  it('a pin on an entry the unit does not hold does nothing', async () => {
    await st().setPin('h1', 's1', 'nope', true)
    expect(setPinned).not.toHaveBeenCalled()
  })
})

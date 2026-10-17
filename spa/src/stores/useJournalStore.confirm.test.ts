// spa/src/stores/useJournalStore.confirm.test.ts — hand checks on 待確認 items (JV-3, spec U8 / §3.4): optimistic, rolled back on a
// failed write, the band and the open count follow, a read of the server has the last word.
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getJournal = vi.fn()
const setConfirmItem = vi.fn()
vi.mock('../lib/journal/api', () => ({
  getJournal: (...a: unknown[]) => getJournal(...a),
  getTasks: vi.fn(),
  getRoadmap: vi.fn(),
  setConfirmItem: (...a: unknown[]) => setConfirmItem(...a),
}))

import { selectUnit, useJournalStore } from './useJournalStore'
import { useUndoToast } from './useUndoToast'
import { parseJournalPage } from '../lib/journal/parse'
import { clearViewMemos } from '../lib/journal/view-memory'
import { T0, wireConfirm, wirePage } from '../lib/journal/fixtures'

const st = () => useJournalStore.getState()
const unit = () => selectUnit(st(), 'h1', 's1')!
const wirePageOf = (items: [string, boolean][]) => {
  const c = wireConfirm('c1', { at: T0, items: items.map(([text, done]) => ({ text, done })) })
  const open = items.filter(([, d]) => !d).length
  return { kind: 'ok', value: parseJournalPage(wirePage({ entries: [c], confirms: open > 0 ? [c] : [], open_confirms: open })) }
}
const ITEMS: [string, boolean][] = [['a', true], ['b', false]]
const done = (idx: number) => unit().entries[0].items[idx].done

beforeEach(async () => {
  st().reset()
  clearViewMemos()
  getJournal.mockReset(); setConfirmItem.mockReset()
  useUndoToast.setState({ toast: null, notice: null })
  getJournal.mockResolvedValue(wirePageOf(ITEMS))
  st().setSupport('h1', true, true)
  await st().loadJournal('h1', 's1')
})

describe('setConfirm', () => {
  it('checks at once: the item, the band and the open count follow; the write is asked with the reader session', async () => {
    setConfirmItem.mockResolvedValue({ kind: 'ok', value: true })
    const p = st().setConfirm('h1', 's1', 'c1', 1, true)
    expect(done(1)).toBe(true) // before the write answers
    expect(unit().confirms).toEqual([]) // every item checked: it leaves the band
    expect(unit().openConfirms).toBe(0)
    await p
    expect(setConfirmItem).toHaveBeenCalledWith('h1', 's1', 'c1', 1, true)
    expect(done(1)).toBe(true)
  })
  it('unchecking an answered entry brings it back to the band and raises the count', async () => {
    setConfirmItem.mockResolvedValue({ kind: 'ok', value: true })
    await st().setConfirm('h1', 's1', 'c1', 1, true)
    await st().setConfirm('h1', 's1', 'c1', 0, false)
    expect(unit().confirms.map((e) => e.id)).toEqual(['c1'])
    expect(unit().openConfirms).toBe(1)
    expect(setConfirmItem).toHaveBeenLastCalledWith('h1', 's1', 'c1', 0, false)
  })
  it('a failed write restores item, band and count, and says so', async () => {
    setConfirmItem.mockResolvedValue({ kind: 'none' })
    await st().setConfirm('h1', 's1', 'c1', 1, true)
    expect(done(1)).toBe(false)
    expect(unit().confirms.map((e) => e.id)).toEqual(['c1'])
    expect(unit().openConfirms).toBe(1)
    expect(useUndoToast.getState().toast?.message).toBeTruthy()
  })
  it('a write that throws restores too', async () => {
    setConfirmItem.mockRejectedValue(new Error('boom'))
    await st().setConfirm('h1', 's1', 'c1', 1, true)
    expect(done(1)).toBe(false)
    expect(unit().openConfirms).toBe(1)
  })
  it('without journal.confirm.v1 nothing is written and nothing changes', async () => {
    st().setSupport('h1', true, false)
    await st().setConfirm('h1', 's1', 'c1', 1, true)
    expect(setConfirmItem).not.toHaveBeenCalled()
    expect(done(1)).toBe(false)
  })
  it('a click that changes nothing (already that state) writes nothing', async () => {
    await st().setConfirm('h1', 's1', 'c1', 0, true)
    expect(setConfirmItem).not.toHaveBeenCalled()
  })
  it('a read that started after the click has the last word over a failed rollback', async () => {
    let fail!: () => void
    setConfirmItem.mockReturnValue(new Promise((_r, rej) => { fail = () => rej(new Error('late')) }))
    const p = st().setConfirm('h1', 's1', 'c1', 1, true)
    getJournal.mockResolvedValue(wirePageOf([['a', true], ['b', true]])) // journal.changed re-read: the server did get it
    await st().loadJournal('h1', 's1')
    fail()
    await p
    expect(done(1)).toBe(true)
    expect(unit().confirms).toEqual([])
  })
  it('a read sent before the click keeps the click on screen', async () => {
    let ack!: (v: unknown) => void
    setConfirmItem.mockReturnValue(new Promise((r) => { ack = r }))
    let land!: (v: unknown) => void
    getJournal.mockReturnValueOnce(new Promise((r) => { land = r }))
    const read = st().loadJournal('h1', 's1')
    const click = st().setConfirm('h1', 's1', 'c1', 1, true)
    land(wirePageOf(ITEMS)) // sent before the click, so it carries the old state
    await read
    expect(done(1)).toBe(true)
    expect(unit().confirms).toEqual([])
    ack({ kind: 'ok', value: true })
    await click
  })
})

describe('setConfirm of an entry only the band holds (an older session / month)', () => {
  const bandOnly = async (items: [string, boolean][]) => {
    st().reset()
    const c = wireConfirm('c9', { at: T0, items: items.map(([text, d]) => ({ text, done: d })) })
    getJournal.mockResolvedValue({ kind: 'ok', value: parseJournalPage(wirePage({ entries: [], confirms: [c], open_confirms: 1 })) })
    st().setSupport('h1', true, true)
    await st().loadJournal('h1', 's1')
  }
  const band = () => unit().confirms.map((e) => e.id)
  it('a failed write on the last item puts the entry back in the band, restores the count and lets it be checked again', async () => {
    await bandOnly([['a', false]])
    expect(unit().entries).toEqual([])
    setConfirmItem.mockResolvedValue({ kind: 'none' })
    await st().setConfirm('h1', 's1', 'c9', 0, true)
    expect(band()).toEqual(['c9'])
    expect(unit().confirms[0].items[0].done).toBe(false)
    expect(unit().openConfirms).toBe(1)
    setConfirmItem.mockResolvedValue({ kind: 'ok', value: true })
    await st().setConfirm('h1', 's1', 'c9', 0, true)
    expect(band()).toEqual([])
    expect(unit().openConfirms).toBe(0)
  })
  it('a successful write leaves the band for good', async () => {
    await bandOnly([['a', false]])
    setConfirmItem.mockResolvedValue({ kind: 'ok', value: true })
    await st().setConfirm('h1', 's1', 'c9', 0, true)
    expect(band()).toEqual([])
    expect(unit().openConfirms).toBe(0)
  })
  it('two items of one entry, one write ok and one failed: the failed one comes back, the ok one stays checked', async () => {
    await bandOnly([['a', false], ['b', false]])
    setConfirmItem.mockResolvedValueOnce({ kind: 'ok', value: true }).mockResolvedValueOnce({ kind: 'none' })
    await st().setConfirm('h1', 's1', 'c9', 0, true)
    await st().setConfirm('h1', 's1', 'c9', 1, true)
    expect(band()).toEqual(['c9'])
    expect(unit().confirms[0].items.map((i) => i.done)).toEqual([true, false])
    expect(unit().openConfirms).toBe(1)
  })
  it('the first item fails after the second succeeded: the restored entry keeps the second checked', async () => {
    await bandOnly([['a', false], ['b', false]])
    let failA!: () => void
    setConfirmItem.mockReturnValueOnce(new Promise((r) => { failA = () => r({ kind: 'none' }) })).mockResolvedValueOnce({ kind: 'ok', value: true })
    const pa = st().setConfirm('h1', 's1', 'c9', 0, true)
    await st().setConfirm('h1', 's1', 'c9', 1, true)
    expect(band()).toEqual([])
    failA()
    await pa
    expect(band()).toEqual(['c9'])
    expect(unit().confirms[0].items.map((i) => i.done)).toEqual([false, true])
    expect(unit().openConfirms).toBe(1)
  })
  it('a journal.changed re-read during the write has the last word over the rollback', async () => {
    await bandOnly([['a', false]])
    let fail!: () => void
    setConfirmItem.mockReturnValue(new Promise((r) => { fail = () => r({ kind: 'none' }) }))
    const p = st().setConfirm('h1', 's1', 'c9', 0, true)
    getJournal.mockResolvedValue({ kind: 'ok', value: parseJournalPage(wirePage({ entries: [], confirms: [], open_confirms: 0 })) })
    await st().loadJournal('h1', 's1')
    fail()
    await p
    expect(band()).toEqual([])
    expect(unit().openConfirms).toBe(0)
  })
})

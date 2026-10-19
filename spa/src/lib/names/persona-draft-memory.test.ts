import { describe, it, expect, beforeEach } from 'vitest'
import {
  rememberDraft, recallDraft, forgetDraft, clearDraftMemory, beginSave, endSave, savePending, subscribeSave,
  getOutcome, setOutcome,
} from './persona-draft-memory'
import { makePersona } from './__fixtures__/personas'

describe('persona draft memory', () => {
  beforeEach(() => clearDraftMemory())

  it('remembers one draft and the base it was opened on', () => {
    const base = { hostId: 'h1', revision: 3, stamp: { at: 5, host: 'h1' } }
    rememberDraft({ key: 'hiro', draft: makePersona('hiro', { name: 'Hiro Two' }), base })
    expect(recallDraft()).toEqual({ key: 'hiro', draft: makePersona('hiro', { name: 'Hiro Two' }), base })
  })

  it('hands out a copy, so editing the recalled draft does not change the memory', () => {
    rememberDraft({ key: 'hiro', draft: makePersona('hiro'), base: null })
    const first = recallDraft()!
    first.draft.name = 'Changed'
    expect(recallDraft()!.draft.name).toBe('Hiro')
  })

  it('allows one save in flight at a time, across whoever asks, and tells subscribers when it ends', () => {
    const seen: boolean[] = []
    const stop = subscribeSave(() => seen.push(savePending()))
    expect(beginSave()).toBe(true)
    expect(beginSave()).toBe(false) // a second page instance, or a second click
    expect(savePending()).toBe(true)
    endSave()
    expect(savePending()).toBe(false)
    expect(beginSave()).toBe(true)
    expect(seen).toEqual([true, false, true])
    stop()
    endSave()
    expect(seen).toEqual([true, false, true]) // unsubscribed
  })

  it('clearDraftMemory also ends a save in flight (test isolation)', () => {
    beginSave()
    clearDraftMemory()
    expect(savePending()).toBe(false)
  })

  it('holds the outcome of the last save and tells subscribers, with a stable snapshot between changes', () => {
    let calls = 0
    const stop = subscribeSave(() => { calls++ })
    expect(getOutcome()).toEqual({ note: null, error: null })
    const before = getOutcome()
    expect(getOutcome()).toBe(before) // useSyncExternalStore needs the same object until something changes
    setOutcome({ note: 'saved' })
    expect(getOutcome()).toEqual({ note: 'saved', error: null })
    setOutcome({ error: 'boom' })
    expect(getOutcome()).toEqual({ note: 'saved', error: 'boom' })
    setOutcome({ note: null, error: null })
    expect(getOutcome()).toEqual({ note: null, error: null })
    expect(calls).toBe(3)
    stop()
  })

  it('clearDraftMemory clears the outcome too', () => {
    setOutcome({ note: 'x', error: 'y' })
    clearDraftMemory()
    expect(getOutcome()).toEqual({ note: null, error: null })
  })

  it('forgets it', () => {
    rememberDraft({ key: 'hiro', draft: makePersona('hiro'), base: null })
    forgetDraft()
    expect(recallDraft()).toBeNull()
  })
})

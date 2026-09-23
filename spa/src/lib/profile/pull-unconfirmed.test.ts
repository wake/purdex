// spa/src/lib/profile/pull-unconfirmed.test.ts — the notice that a pull was stopped because the hosts moved (#1366),
// kept under its OWN key: writing it must never write the profile control plane (see the module's header).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../storage/keys'
import { clearPullUnconfirmed, pullUnconfirmedSnapshot, readPullUnconfirmed, subscribePullUnconfirmed, writePullUnconfirmed } from './pull-unconfirmed'

const KEY = STORAGE_KEYS.PROFILE_PULL_UNCONFIRMED
const NOTICE = { hostId: 'host-1', profileId: 'p_000000000001', at: 1_000 }

beforeEach(() => localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe('pull-unconfirmed', () => {
  it('starts null; written, read back, cleared — under its own key and nothing else', () => {
    expect(readPullUnconfirmed()).toBeNull()
    expect(writePullUnconfirmed(NOTICE)).toBe(true)
    expect(readPullUnconfirmed()).toEqual(NOTICE)
    expect(Object.keys(localStorage)).toEqual([KEY])
    clearPullUnconfirmed()
    expect(readPullUnconfirmed()).toBeNull()
    expect(localStorage.getItem(KEY)).toBeNull()
  })

  it('never touches the profile control plane', () => {
    const control = JSON.stringify({ version: 1, state: { masterHostId: 'h9', attachGeneration: 9 } })
    localStorage.setItem(STORAGE_KEYS.PROFILE, control)
    writePullUnconfirmed(NOTICE)
    clearPullUnconfirmed()
    expect(localStorage.getItem(STORAGE_KEYS.PROFILE)).toBe(control)
  })

  it.each([
    ['a malformed profile id', { ...NOTICE, profileId: 'nope' }],
    ['an empty host id', { ...NOTICE, hostId: '' }],
    ['a non-finite time', { ...NOTICE, at: Number.NaN }],
  ])('%s: refused by the writer, dropped by the reader', (_label, bad) => {
    expect(writePullUnconfirmed(bad)).toBe(false)
    expect(localStorage.getItem(KEY)).toBeNull()
    localStorage.setItem(KEY, JSON.stringify(bad))
    expect(readPullUnconfirmed()).toBeNull()
  })

  it('junk in storage reads as null; extra fields are dropped', () => {
    localStorage.setItem(KEY, '{not json')
    expect(readPullUnconfirmed()).toBeNull()
    localStorage.setItem(KEY, JSON.stringify({ ...NOTICE, extra: 1 }))
    expect(readPullUnconfirmed()).toEqual(NOTICE)
  })

  it('storage that throws: nothing throws, the reader says null, the writer false', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota')
    })
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(readPullUnconfirmed()).toBeNull()
    expect(writePullUnconfirmed(NOTICE)).toBe(false)
    expect(() => clearPullUnconfirmed()).not.toThrow()
  })

  it('subscribers hear this window\'s writes and another window\'s (the `storage` event); the snapshot keeps its identity until the value moves', () => {
    const fn = vi.fn()
    const stop = subscribePullUnconfirmed(fn)
    writePullUnconfirmed(NOTICE)
    expect(fn).toHaveBeenCalledTimes(1)
    const first = pullUnconfirmedSnapshot()
    expect(first).toEqual(NOTICE)
    expect(pullUnconfirmedSnapshot()).toBe(first)
    // another window clears it
    localStorage.removeItem(KEY)
    window.dispatchEvent(new StorageEvent('storage', { key: KEY }))
    expect(fn).toHaveBeenCalledTimes(2)
    expect(pullUnconfirmedSnapshot()).toBeNull()
    // someone else's key: not ours
    window.dispatchEvent(new StorageEvent('storage', { key: STORAGE_KEYS.PROFILE }))
    expect(fn).toHaveBeenCalledTimes(2)
    stop()
    clearPullUnconfirmed()
    writePullUnconfirmed(NOTICE)
    window.dispatchEvent(new StorageEvent('storage', { key: KEY }))
    expect(fn).toHaveBeenCalledTimes(2)
  })
})

import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerLocale,
  getLocale,
  getAllLocales,
  unregisterLocale,
  clearLocaleRegistry,
} from './locale-registry'
import type { LocaleDef } from './locale-registry'

const enLocale: LocaleDef = {
  id: 'en',
  name: 'English',
  translations: { 'common.save': 'Save' },
  builtin: true,
}

const customLocale: LocaleDef = {
  id: 'custom-1',
  name: 'My Locale',
  translations: { 'common.save': 'SAVE!' },
  builtin: false,
}

describe('locale-registry', () => {
  beforeEach(() => clearLocaleRegistry())

  it('registers and retrieves a locale', () => {
    registerLocale(enLocale)
    expect(getLocale('en')).toEqual(enLocale)
  })

  it('returns undefined for unregistered id', () => {
    expect(getLocale('nope')).toBeUndefined()
  })

  it('getAllLocales returns all registered', () => {
    registerLocale(enLocale)
    registerLocale(customLocale)
    expect(getAllLocales()).toHaveLength(2)
  })

  it('is idempotent — re-registering same id overwrites', () => {
    registerLocale(enLocale)
    const updated = { ...enLocale, name: 'EN Updated' }
    registerLocale(updated)
    expect(getLocale('en')?.name).toBe('EN Updated')
    expect(getAllLocales()).toHaveLength(1)
  })

  it('unregisterLocale removes non-builtin', () => {
    registerLocale(customLocale)
    unregisterLocale('custom-1')
    expect(getLocale('custom-1')).toBeUndefined()
  })

  it('unregisterLocale refuses to remove builtin', () => {
    registerLocale(enLocale)
    unregisterLocale('en')
    expect(getLocale('en')).toEqual(enLocale)
  })

  it('clearLocaleRegistry empties everything', () => {
    registerLocale(enLocale)
    registerLocale(customLocale)
    clearLocaleRegistry()
    expect(getAllLocales()).toHaveLength(0)
  })
})

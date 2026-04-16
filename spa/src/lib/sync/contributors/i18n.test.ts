// =============================================================================
// Sync Architecture — I18nContributor Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createI18nContributor } from './i18n'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { FullPayload } from '../types'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resetStore() {
  useI18nStore.setState({
    activeLocaleId: 'en',
    customLocales: {},
  })
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('createI18nContributor', () => {
  let contributor: ReturnType<typeof createI18nContributor>

  beforeEach(() => {
    resetStore()
    contributor = createI18nContributor()
  })

  // -------------------------------------------------------------------------
  // Identity & strategy
  // -------------------------------------------------------------------------

  it('has id "i18n"', () => {
    expect(contributor.id).toBe('i18n')
  })

  it('has strategy "full"', () => {
    expect(contributor.strategy).toBe('full')
  })

  // -------------------------------------------------------------------------
  // getVersion
  // -------------------------------------------------------------------------

  it('getVersion returns 1', () => {
    expect(contributor.getVersion()).toBe(1)
  })

  // -------------------------------------------------------------------------
  // serialize
  // -------------------------------------------------------------------------

  it('serialize returns FullPayload with version 1', () => {
    const payload = contributor.serialize() as FullPayload
    expect(payload.version).toBe(1)
    expect(payload.data).toBeDefined()
  })

  it('serialize only includes expected data fields (no functions)', () => {
    const payload = contributor.serialize() as FullPayload
    const keys = Object.keys(payload.data)

    expect(keys).toContain('activeLocaleId')
    expect(keys).toContain('customLocales')

    // Must NOT contain the `t` function or action functions
    expect(keys).not.toContain('t')
    expect(keys).not.toContain('setLocale')
    expect(keys).not.toContain('importLocale')
    expect(keys).not.toContain('updateCustomLocale')
    expect(keys).not.toContain('deleteCustomLocale')

    // All values must be non-function
    for (const key of keys) {
      expect(typeof payload.data[key]).not.toBe('function')
    }
  })

  it('serialize reflects current store state', () => {
    useI18nStore.setState({ activeLocaleId: 'zh-TW' })
    const payload = contributor.serialize() as FullPayload
    expect(payload.data.activeLocaleId).toBe('zh-TW')
  })

  it('serialize includes customLocales', () => {
    useI18nStore.setState({
      customLocales: {
        'custom-1': { id: 'custom-1', name: 'Custom', translations: { hello: '哈囉' }, builtin: false },
      },
    })
    const payload = contributor.serialize() as FullPayload
    const customLocales = payload.data.customLocales as Record<string, unknown>
    expect(customLocales['custom-1']).toBeDefined()
  })

  // -------------------------------------------------------------------------
  // deserialize — full-replace
  // -------------------------------------------------------------------------

  it('deserialize with full-replace overwrites store state', () => {
    const incoming: FullPayload = {
      version: 1,
      data: {
        activeLocaleId: 'zh-TW',
        customLocales: {
          'c-1': { id: 'c-1', name: 'Test Locale', translations: { hello: '嗨' }, builtin: false },
        },
      },
    }

    contributor.deserialize(incoming, { type: 'full-replace' })

    const state = useI18nStore.getState()
    expect(state.activeLocaleId).toBe('zh-TW')
    expect(state.customLocales['c-1']).toBeDefined()
  })

  // -------------------------------------------------------------------------
  // deserialize — field-merge
  // -------------------------------------------------------------------------

  it('deserialize with field-merge only applies resolved remote fields', () => {
    useI18nStore.setState({
      activeLocaleId: 'en',
      customLocales: {},
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        activeLocaleId: 'ja',
        customLocales: {
          'c-remote': { id: 'c-remote', name: 'Japanese Custom', translations: { hello: 'こんにちは' }, builtin: false },
        },
      },
    }

    // Only apply activeLocaleId from remote; customLocales stays local
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: {
        activeLocaleId: 'remote',
        customLocales: 'local',
      },
    })

    const state = useI18nStore.getState()
    expect(state.activeLocaleId).toBe('ja')
    expect(state.customLocales['c-remote']).toBeUndefined()
  })

  it('deserialize with field-merge ignores fields not present in resolved', () => {
    useI18nStore.setState({
      activeLocaleId: 'en',
      customLocales: {},
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        activeLocaleId: 'fr',
        customLocales: {},
      },
    }

    // activeLocaleId=remote; customLocales not mentioned
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { activeLocaleId: 'remote' },
    })

    const state = useI18nStore.getState()
    expect(state.activeLocaleId).toBe('fr')
    // customLocales untouched (empty stays empty)
    expect(Object.keys(state.customLocales)).toHaveLength(0)
  })
})

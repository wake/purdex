import { describe, it, expect, beforeEach } from 'vitest'
import { useI18nStore } from './useI18nStore'
import { registerLocale, clearLocaleRegistry } from '../lib/locale-registry'
import type { LocaleDef } from '../lib/locale-registry'

const en: LocaleDef = {
  id: 'en', name: 'English',
  translations: { 'common.save': 'Save', 'common.cancel': 'Cancel', 'greeting': 'Hello {{name}}' },
  builtin: true,
}
const zhTW: LocaleDef = {
  id: 'zh-TW', name: '繁體中文',
  translations: { 'common.save': '儲存', 'common.cancel': '取消', 'greeting': '你好 {{name}}' },
  builtin: true,
}

describe('useI18nStore', () => {
  beforeEach(() => {
    clearLocaleRegistry()
    registerLocale(en)
    registerLocale(zhTW)
    useI18nStore.setState({ activeLocaleId: 'en', customLocales: {} })
    // Rebuild t after resetting state
    useI18nStore.getState().setLocale('en')
  })

  describe('t()', () => {
    it('returns translation for active locale', () => {
      const { t } = useI18nStore.getState()
      expect(t('common.save')).toBe('Save')
    })
    it('falls back to en when key missing in active locale', () => {
      // Add a key only in en
      registerLocale({ ...en, translations: { ...en.translations, 'only_en': 'Only English' } })
      useI18nStore.getState().setLocale('zh-TW')
      const { t } = useI18nStore.getState()
      expect(t('only_en')).toBe('Only English')
    })
    it('returns key itself when not found anywhere', () => {
      const { t } = useI18nStore.getState()
      expect(t('nonexistent.key')).toBe('nonexistent.key')
    })
    it('interpolates {{params}}', () => {
      const { t } = useI18nStore.getState()
      expect(t('greeting', { name: 'World' })).toBe('Hello World')
    })
    it('replaces missing param with empty string', () => {
      const { t } = useI18nStore.getState()
      expect(t('greeting')).toBe('Hello ')
    })
  })

  describe('setLocale()', () => {
    it('changes active locale', () => {
      useI18nStore.getState().setLocale('zh-TW')
      expect(useI18nStore.getState().activeLocaleId).toBe('zh-TW')
      expect(useI18nStore.getState().t('common.save')).toBe('儲存')
    })
    it('ignores unregistered locale id', () => {
      useI18nStore.getState().setLocale('nope')
      expect(useI18nStore.getState().activeLocaleId).toBe('en')
    })
    it('updates document.documentElement.lang', () => {
      useI18nStore.getState().setLocale('zh-TW')
      expect(document.documentElement.lang).toBe('zh-TW')
    })
  })

  describe('custom locale CRUD', () => {
    it('importLocale adds custom locale', () => {
      const id = useI18nStore.getState().importLocale({
        name: 'My Custom', translations: { 'common.save': 'SAVE!' },
      })
      expect(typeof id).toBe('string')
      expect(useI18nStore.getState().customLocales[id]).toBeDefined()
      expect(useI18nStore.getState().customLocales[id].name).toBe('My Custom')
    })
    it('importLocale deduplicates names', () => {
      useI18nStore.getState().importLocale({ name: 'Custom', translations: { a: 'b' } })
      const id2 = useI18nStore.getState().importLocale({ name: 'Custom', translations: { a: 'c' } })
      expect(useI18nStore.getState().customLocales[id2].name).toBe('Custom (2)')
    })
    it('updateCustomLocale patches locale', () => {
      const id = useI18nStore.getState().importLocale({ name: 'X', translations: { a: 'b' } })
      useI18nStore.getState().updateCustomLocale(id, { name: 'Y' })
      expect(useI18nStore.getState().customLocales[id].name).toBe('Y')
    })
    it('deleteCustomLocale removes and falls back if active', () => {
      const id = useI18nStore.getState().importLocale({ name: 'X', translations: { a: 'b' } })
      useI18nStore.getState().setLocale(id)
      useI18nStore.getState().deleteCustomLocale(id)
      expect(useI18nStore.getState().customLocales[id]).toBeUndefined()
      expect(useI18nStore.getState().activeLocaleId).toBe('en')
    })
  })
})

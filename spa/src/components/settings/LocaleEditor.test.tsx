import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { LocaleEditor } from './LocaleEditor'
import { useI18nStore } from '../../stores/useI18nStore'
import { registerLocale, clearLocaleRegistry } from '../../lib/locale-registry'
import type { LocaleDef } from '../../lib/locale-registry'

const en: LocaleDef = {
  id: 'en', name: 'English',
  translations: { 'common.save': 'Save', 'common.cancel': 'Cancel', 'common.reset': 'Reset',
    'locale.editor.title': 'Locale Editor', 'locale.editor.close': 'Close',
    'locale.editor.name_label': 'Name', 'locale.editor.name_aria': 'Locale name',
    'locale.editor.search': 'Search', 'locale.editor.save': 'Save locale',
    'locale.editor.filter.all': 'All ({{count}})', 'locale.editor.filter.modified': 'Modified ({{count}})',
    'locale.editor.filter.missing': 'Missing ({{count}})' },
  builtin: true,
}

describe('LocaleEditor', () => {
  beforeEach(() => {
    clearLocaleRegistry()
    registerLocale(en)
    useI18nStore.setState({ activeLocaleId: 'en', customLocales: {} })
    useI18nStore.getState().setLocale('en')
  })

  it('save forks builtin locale into new custom entry', () => {
    const onClose = vi.fn()
    render(<LocaleEditor baseLocaleId="en" onClose={onClose} />)

    // Change name
    const nameInput = screen.getByLabelText('Locale name')
    fireEvent.change(nameInput, { target: { value: 'My English' } })

    // Save
    fireEvent.click(screen.getByLabelText('Save locale'))

    // Should have created a custom locale
    const state = useI18nStore.getState()
    const customIds = Object.keys(state.customLocales)
    expect(customIds.length).toBe(1)
    expect(customIds[0]).not.toBe('en')
    expect(state.customLocales[customIds[0]].name).toBe('My English')
    expect(onClose).toHaveBeenCalled()
  })

  it('save updates existing custom locale in-place', () => {
    // Pre-create a custom locale
    const customId = useI18nStore.getState().importLocale({
      name: 'My Locale',
      translations: { ...en.translations },
    })
    registerLocale(useI18nStore.getState().customLocales[customId])

    const onClose = vi.fn()
    render(<LocaleEditor baseLocaleId={customId} onClose={onClose} />)

    // Change name
    const nameInput = screen.getByLabelText('Locale name')
    fireEvent.change(nameInput, { target: { value: 'Updated Locale' } })

    // Save
    fireEvent.click(screen.getByLabelText('Save locale'))

    // Should have updated in-place, not created a new entry
    const state = useI18nStore.getState()
    const customIds = Object.keys(state.customLocales)
    expect(customIds.length).toBe(1)
    expect(customIds[0]).toBe(customId)
    expect(state.customLocales[customId].name).toBe('Updated Locale')
    expect(onClose).toHaveBeenCalled()
  })

  it('name initializes without (Custom) suffix when editing custom', () => {
    // Pre-create a custom locale
    const customId = useI18nStore.getState().importLocale({
      name: 'My Locale',
      translations: { ...en.translations },
    })
    registerLocale(useI18nStore.getState().customLocales[customId])

    render(<LocaleEditor baseLocaleId={customId} onClose={() => {}} />)

    const nameInput = screen.getByLabelText('Locale name')
    expect(nameInput).toHaveValue('My Locale')
  })
})

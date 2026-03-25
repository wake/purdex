import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { generateId } from '../lib/id'
import { registerLocale, unregisterLocale, getLocale, getAllLocales } from '../lib/locale-registry'
import type { LocaleDef } from '../lib/locale-registry'
import { detectLocale } from '../lib/detect-locale'
import type { LocaleImportPayload } from '../lib/locale-import'

interface I18nState {
  activeLocaleId: string
  customLocales: Record<string, LocaleDef>
  t: (key: string, params?: Record<string, string | number>) => string

  setLocale: (id: string) => void
  importLocale: (payload: LocaleImportPayload) => string
  updateCustomLocale: (id: string, patch: Partial<Pick<LocaleDef, 'name' | 'translations'>>) => void
  deleteCustomLocale: (id: string) => void
}

function applyLocaleToDom(id: string) {
  document.documentElement.lang = id
}

function generateUniqueId(existing: Set<string>): string {
  for (let i = 0; i < 100; i++) {
    const id = generateId()
    if (!existing.has(id)) return id
  }
  return generateId()
}

function deduplicateName(name: string, existingNames: Set<string>): string {
  if (!existingNames.has(name)) return name
  let i = 2
  while (existingNames.has(`${name} (${i})`)) i++
  return `${name} (${i})`
}

function makeT(activeLocaleId: string, customLocales: Record<string, LocaleDef>) {
  return (key: string, params?: Record<string, string | number>): string => {
    const active = getLocale(activeLocaleId) ?? customLocales[activeLocaleId]
    let value: string | undefined = active?.translations[key]
    if (value === undefined) {
      const en = getLocale('en')
      value = en?.translations[key]
    }
    if (value === undefined) return key
    value = value.replace(/\{\{(\w+)\}\}/g, (_, k) => String(params?.[k] ?? ''))
    return value
  }
}

export const useI18nStore = create<I18nState>()(
  persist(
    (set, get) => ({
      activeLocaleId: 'en',
      customLocales: {},
      t: makeT('en', {}),

      setLocale: (id) => {
        if (!getLocale(id) && !get().customLocales[id]) return
        set({ activeLocaleId: id, t: makeT(id, get().customLocales) })
        applyLocaleToDom(id)
      },

      importLocale: (payload) => {
        const builtinIds = new Set(getAllLocales().map((l) => l.id))
        const customIds = new Set(Object.keys(get().customLocales))
        const allIds = new Set([...builtinIds, ...customIds])
        const id = generateUniqueId(allIds)
        const existingNames = new Set([
          ...getAllLocales().map((l) => l.name),
          ...Object.values(get().customLocales).map((l) => l.name),
        ])
        const name = deduplicateName(payload.name, existingNames)
        const def: LocaleDef = { id, name, translations: payload.translations, builtin: false }
        registerLocale(def)
        const newCustom = { ...get().customLocales, [id]: def }
        set({ customLocales: newCustom, t: makeT(get().activeLocaleId, newCustom) })
        return id
      },

      updateCustomLocale: (id, patch) => {
        const existing = get().customLocales[id]
        if (!existing) return
        const updated: LocaleDef = {
          ...existing,
          ...(patch.name !== undefined ? { name: patch.name } : {}),
          ...(patch.translations !== undefined ? { translations: patch.translations } : {}),
        }
        registerLocale(updated)
        const newCustom = { ...get().customLocales, [id]: updated }
        set({ customLocales: newCustom, t: makeT(get().activeLocaleId, newCustom) })
      },

      deleteCustomLocale: (id) => {
        unregisterLocale(id)
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { [id]: _removed, ...rest } = get().customLocales
        const newActiveId = get().activeLocaleId === id ? 'en' : get().activeLocaleId
        if (get().activeLocaleId === id) applyLocaleToDom('en')
        set({ customLocales: rest, activeLocaleId: newActiveId, t: makeT(newActiveId, rest) })
      },
    }),
    {
      name: 'tbox-i18n',
      partialize: (state) => ({
        activeLocaleId: state.activeLocaleId,
        customLocales: state.customLocales,
      }),
      onRehydrateStorage: () => (state) => {
        if (!state) return
        for (const def of Object.values(state.customLocales)) {
          registerLocale(def)
        }
        const localeId = (getLocale(state.activeLocaleId) || state.customLocales[state.activeLocaleId])
          ? state.activeLocaleId
          : detectLocale(navigator.languages, getAllLocales().map((l) => l.id))
        applyLocaleToDom(localeId)
        if (localeId !== state.activeLocaleId) {
          useI18nStore.setState({ activeLocaleId: localeId, t: makeT(localeId, state.customLocales) })
        } else {
          useI18nStore.setState({ t: makeT(localeId, state.customLocales) })
        }
      },
    },
  ),
)

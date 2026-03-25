import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { generateId } from '../lib/id'
import { registerTheme, unregisterTheme, getTheme, getAllThemes } from '../lib/theme-registry'
import type { ThemeDefinition } from '../lib/theme-registry'
import type { ThemeTokens } from '../lib/theme-tokens'

export interface ThemeImportPayload {
  name: string
  tokens: Partial<ThemeTokens>
}

interface ThemeState {
  activeThemeId: string
  customThemes: Record<string, ThemeDefinition>

  setActiveTheme: (id: string) => void
  createCustomTheme: (name: string, baseId: string, overrides: Partial<ThemeTokens>) => string
  updateCustomTheme: (id: string, patch: Partial<Pick<ThemeDefinition, 'name' | 'tokens'>>) => void
  deleteCustomTheme: (id: string) => void
  importTheme: (payload: ThemeImportPayload) => string
}

function applyThemeToDom(id: string) {
  document.documentElement.dataset.theme = id
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

export const useThemeStore = create<ThemeState>()(
  persist(
    (set, get) => ({
      activeThemeId: 'dark',
      customThemes: {},

      setActiveTheme: (id) => {
        if (!getTheme(id)) return
        set({ activeThemeId: id })
        applyThemeToDom(id)
      },

      createCustomTheme: (name, baseId, overrides) => {
        const base = getTheme(baseId)
        if (!base) throw new Error(`Base theme "${baseId}" not found`)

        const builtinIds = new Set(getAllThemes().map((t) => t.id))
        const customIds = new Set(Object.keys(get().customThemes))
        const allIds = new Set([...builtinIds, ...customIds])
        const id = generateUniqueId(allIds)

        const tokens: ThemeTokens = { ...base.tokens, ...overrides }
        const def: ThemeDefinition = { id, name, tokens, builtin: false }
        registerTheme(def)
        set((s) => ({ customThemes: { ...s.customThemes, [id]: def } }))
        return id
      },

      updateCustomTheme: (id, patch) => {
        const state = get()
        const existing = state.customThemes[id]
        if (!existing) return

        const updated: ThemeDefinition = {
          ...existing,
          ...(patch.name !== undefined ? { name: patch.name } : {}),
          ...(patch.tokens !== undefined ? { tokens: patch.tokens } : {}),
        }
        registerTheme(updated)
        set((s) => ({ customThemes: { ...s.customThemes, [id]: updated } }))
      },

      deleteCustomTheme: (id) => {
        unregisterTheme(id)
        set((s) => {
          // eslint-disable-next-line @typescript-eslint/no-unused-vars
          const { [id]: _removed, ...rest } = s.customThemes
          const newActiveId = s.activeThemeId === id ? 'dark' : s.activeThemeId
          if (s.activeThemeId === id) applyThemeToDom('dark')
          return { customThemes: rest, activeThemeId: newActiveId }
        })
      },

      importTheme: (payload) => {
        if (!getTheme('dark')) throw new Error('Dark theme not registered')
        const existingNames = new Set(
          [...getAllThemes().map((t) => t.name), ...Object.values(get().customThemes).map((t) => t.name)]
        )
        const name = deduplicateName(payload.name, existingNames)
        return get().createCustomTheme(name, 'dark', payload.tokens)
      },
    }),
    {
      name: 'tbox-themes',
      partialize: (state) => ({
        activeThemeId: state.activeThemeId,
        customThemes: state.customThemes,
      }),
      onRehydrateStorage: () => (state) => {
        if (!state) return
        for (const def of Object.values(state.customThemes)) {
          registerTheme(def)
        }
        const themeId = getTheme(state.activeThemeId) ? state.activeThemeId : 'dark'
        applyThemeToDom(themeId)
        if (themeId !== state.activeThemeId) {
          useThemeStore.setState({ activeThemeId: themeId })
        }
      },
    },
  ),
)

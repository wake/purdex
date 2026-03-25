# Theme System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 將 SPA 硬編碼色彩抽取為語義化 CSS token，支援 4 個預設主題（Dark/Light/Nord/Dracula）+ 自訂主題（fork + 全色彩編輯器 + 匯入匯出）。

**Architecture:** Tailwind v4 `@theme` 映射 CSS Variables 為 utility class。`[data-theme]` 在 `<html>` 切換主題。Theme Registry（Map-based）+ 獨立 Zustand Theme Store（persist localStorage）。自訂主題 runtime 注入 `<style>`。

**Tech Stack:** React 19 / Tailwind CSS 4 / Zustand 5 / Vitest / CSS Custom Properties

**Spec:** `docs/superpowers/specs/2026-03-25-theme-system-design.md`

---

## File Structure

### New Files

| File | Responsibility |
|------|---------------|
| `spa/src/lib/theme-tokens.ts` | `ThemeTokenKey` union type, `THEME_TOKEN_KEYS` array, `TOKEN_METADATA` (label + group per token) |
| `spa/src/lib/theme-registry.ts` | Map-based registry: `registerTheme`, `getTheme`, `getAllThemes`, `unregisterTheme`, `clearThemeRegistry` |
| `spa/src/lib/register-themes.ts` | `registerBuiltinThemes()` — registers 4 preset themes via `registerTheme()` |
| `spa/src/stores/useThemeStore.ts` | Zustand + persist: `activeThemeId`, `customThemes`, CRUD actions, `data-theme` side effect |
| `spa/src/styles/themes.css` | `@theme` Tailwind mapping + `[data-theme="dark"]` / light / nord / dracula CSS variable blocks |
| `spa/src/components/ThemeInjector.tsx` | Side-effect component: injects `<style>` for active custom theme |
| `spa/src/components/settings/ThemeEditor.tsx` | Full color editor: name input, 6 token groups, color pickers, save/cancel/reset |
| `spa/src/components/settings/ThemeImportModal.tsx` | Import modal: JSON paste / file upload / URL fetch + validation |
| `spa/src/lib/theme-tokens.test.ts` | Tests for token metadata completeness |
| `spa/src/lib/theme-registry.test.ts` | Tests for registry CRUD + builtin protection |
| `spa/src/stores/useThemeStore.test.ts` | Tests for store actions + side effects |
| `spa/src/components/settings/ThemeEditor.test.tsx` | Tests for editor flow |
| `spa/src/components/settings/ThemeImportModal.test.tsx` | Tests for import validation |

### Modified Files

| File | Change |
|------|--------|
| `spa/src/index.css` | Add `@import "./styles/themes.css"`, body bg → `var(--surface-primary)` |
| `spa/src/main.tsx` | Add `registerBuiltinThemes()` call |
| `spa/src/App.tsx` | Mount `<ThemeInjector />`, migrate colors |
| `spa/src/components/settings/AppearanceSection.tsx` | Replace disabled UI with working theme selector + customize + import buttons |
| `spa/src/hooks/useTerminal.ts` | Read terminal theme from CSS variables |
| ~20 component files | Migrate `bg-[#xxx]` / `text-gray-*` → semantic token classes |
| ~4 test files | Update hardcoded color assertions |

---

## Task 1: Theme Token Types

**Files:**
- Create: `spa/src/lib/theme-tokens.ts`
- Create: `spa/src/lib/theme-tokens.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/theme-tokens.test.ts
import { describe, it, expect } from 'vitest'
import { THEME_TOKEN_KEYS, TOKEN_METADATA, type ThemeTokenKey } from './theme-tokens'

describe('theme-tokens', () => {
  it('exports 23 token keys', () => {
    expect(THEME_TOKEN_KEYS).toHaveLength(23)
  })

  it('every key has metadata with label and group', () => {
    for (const key of THEME_TOKEN_KEYS) {
      const meta = TOKEN_METADATA[key]
      expect(meta, `missing metadata for ${key}`).toBeDefined()
      expect(meta.label).toBeTruthy()
      expect(meta.group).toBeTruthy()
    }
  })

  it('groups are one of the 6 defined groups', () => {
    const validGroups = ['surface', 'text', 'border', 'accent', 'terminal', 'status']
    for (const key of THEME_TOKEN_KEYS) {
      expect(validGroups).toContain(TOKEN_METADATA[key].group)
    }
  })

  it('ThemeTokenKey type matches THEME_TOKEN_KEYS', () => {
    // Type-level check: this compiles only if types align
    const keys: ThemeTokenKey[] = [...THEME_TOKEN_KEYS]
    expect(keys.length).toBe(23)
  })

  it('tokensToCss converts tokens to CSS variable declarations', () => {
    const { tokensToCss } = await import('./theme-tokens')
    const tokens = Object.fromEntries(THEME_TOKEN_KEYS.map((k) => [k, '#000'])) as ThemeTokens
    tokens.accent = '#ff0000'
    const css = tokensToCss(tokens)
    expect(css).toContain('--accent: #ff0000;')
    expect(css).toContain('--surface-primary: #000;')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/theme-tokens.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Write implementation**

```ts
// spa/src/lib/theme-tokens.ts
export const THEME_TOKEN_KEYS = [
  // Surface
  'surface-primary', 'surface-secondary', 'surface-tertiary',
  'surface-elevated', 'surface-hover', 'surface-active', 'surface-input',
  // Text
  'text-primary', 'text-secondary', 'text-muted', 'text-inverse',
  // Border
  'border-default', 'border-active', 'border-subtle',
  // Accent
  'accent', 'accent-hover', 'accent-muted',
  // Terminal
  'terminal-bg', 'terminal-fg', 'terminal-cursor',
  // Status
  'status-error', 'status-warning', 'status-success',
] as const

export type ThemeTokenKey = (typeof THEME_TOKEN_KEYS)[number]
export type ThemeTokens = Record<ThemeTokenKey, string>

interface TokenMeta {
  label: string
  group: 'surface' | 'text' | 'border' | 'accent' | 'terminal' | 'status'
}

export const TOKEN_METADATA: Record<ThemeTokenKey, TokenMeta> = {
  'surface-primary':   { label: 'Primary Background',    group: 'surface' },
  'surface-secondary': { label: 'Secondary Background',  group: 'surface' },
  'surface-tertiary':  { label: 'Tertiary Background',   group: 'surface' },
  'surface-elevated':  { label: 'Elevated Surface',      group: 'surface' },
  'surface-hover':     { label: 'Hover State',           group: 'surface' },
  'surface-active':    { label: 'Active/Selected',       group: 'surface' },
  'surface-input':     { label: 'Input Background',      group: 'surface' },
  'text-primary':      { label: 'Primary Text',          group: 'text' },
  'text-secondary':    { label: 'Secondary Text',        group: 'text' },
  'text-muted':        { label: 'Muted Text',            group: 'text' },
  'text-inverse':      { label: 'Inverse Text',          group: 'text' },
  'border-default':    { label: 'Default Border',        group: 'border' },
  'border-active':     { label: 'Active Border',         group: 'border' },
  'border-subtle':     { label: 'Subtle Border',         group: 'border' },
  'accent':            { label: 'Accent',                group: 'accent' },
  'accent-hover':      { label: 'Accent Hover',          group: 'accent' },
  'accent-muted':      { label: 'Accent Muted',          group: 'accent' },
  'terminal-bg':       { label: 'Terminal Background',    group: 'terminal' },
  'terminal-fg':       { label: 'Terminal Foreground',    group: 'terminal' },
  'terminal-cursor':   { label: 'Terminal Cursor',        group: 'terminal' },
  'status-error':      { label: 'Error',                  group: 'status' },
  'status-warning':    { label: 'Warning',                group: 'status' },
  'status-success':    { label: 'Success',                group: 'status' },
}

/** Convert ThemeTokens to CSS variable declarations */
export function tokensToCss(tokens: ThemeTokens): string {
  return Object.entries(tokens)
    .map(([key, value]) => `--${key}: ${value};`)
    .join(' ')
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/theme-tokens.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/theme-tokens.ts spa/src/lib/theme-tokens.test.ts
git commit -m "feat(theme): add ThemeTokenKey types and token metadata"
```

---

## Task 2: Theme Registry

**Files:**
- Create: `spa/src/lib/theme-registry.ts`
- Create: `spa/src/lib/theme-registry.test.ts`

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/lib/theme-registry.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerTheme, getTheme, getAllThemes, unregisterTheme, clearThemeRegistry,
} from './theme-registry'
import type { ThemeTokens } from './theme-tokens'
import { THEME_TOKEN_KEYS } from './theme-tokens'

function makeTokens(base = '#000000'): ThemeTokens {
  return Object.fromEntries(THEME_TOKEN_KEYS.map((k) => [k, base])) as ThemeTokens
}

describe('theme-registry', () => {
  beforeEach(() => clearThemeRegistry())

  it('registers and retrieves a theme', () => {
    registerTheme({ id: 'test', name: 'Test', tokens: makeTokens(), builtin: false })
    expect(getTheme('test')).toBeDefined()
    expect(getTheme('test')!.name).toBe('Test')
  })

  it('returns undefined for unregistered theme', () => {
    expect(getTheme('nope')).toBeUndefined()
  })

  it('getAllThemes returns all registered themes', () => {
    registerTheme({ id: 'a', name: 'A', tokens: makeTokens(), builtin: false })
    registerTheme({ id: 'b', name: 'B', tokens: makeTokens(), builtin: true })
    expect(getAllThemes()).toHaveLength(2)
  })

  it('registerTheme is idempotent (overwrites)', () => {
    registerTheme({ id: 'x', name: 'V1', tokens: makeTokens(), builtin: false })
    registerTheme({ id: 'x', name: 'V2', tokens: makeTokens(), builtin: false })
    expect(getTheme('x')!.name).toBe('V2')
    expect(getAllThemes()).toHaveLength(1)
  })

  it('unregisterTheme removes non-builtin theme', () => {
    registerTheme({ id: 'custom', name: 'C', tokens: makeTokens(), builtin: false })
    unregisterTheme('custom')
    expect(getTheme('custom')).toBeUndefined()
  })

  it('unregisterTheme refuses to remove builtin theme', () => {
    registerTheme({ id: 'dark', name: 'Dark', tokens: makeTokens(), builtin: true })
    unregisterTheme('dark')
    expect(getTheme('dark')).toBeDefined()
  })

  it('clearThemeRegistry removes all themes', () => {
    registerTheme({ id: 'a', name: 'A', tokens: makeTokens(), builtin: true })
    registerTheme({ id: 'b', name: 'B', tokens: makeTokens(), builtin: false })
    clearThemeRegistry()
    expect(getAllThemes()).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/theme-registry.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Write implementation**

```ts
// spa/src/lib/theme-registry.ts
import type { ThemeTokens } from './theme-tokens'

export interface ThemeDefinition {
  id: string
  name: string
  tokens: ThemeTokens
  builtin: boolean
}

const registry = new Map<string, ThemeDefinition>()

export function registerTheme(def: ThemeDefinition): void {
  registry.set(def.id, def)
}

export function getTheme(id: string): ThemeDefinition | undefined {
  return registry.get(id)
}

export function getAllThemes(): ThemeDefinition[] {
  return [...registry.values()]
}

export function unregisterTheme(id: string): void {
  const theme = registry.get(id)
  if (theme && !theme.builtin) registry.delete(id)
}

export function clearThemeRegistry(): void {
  registry.clear()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/theme-registry.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/theme-registry.ts spa/src/lib/theme-registry.test.ts
git commit -m "feat(theme): add theme registry with builtin protection"
```

---

## Task 3: Theme Store

**Files:**
- Create: `spa/src/stores/useThemeStore.ts`
- Create: `spa/src/stores/useThemeStore.test.ts`

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/stores/useThemeStore.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useThemeStore } from './useThemeStore'
import { clearThemeRegistry, getTheme, registerTheme, getAllThemes } from '../lib/theme-registry'
import { THEME_TOKEN_KEYS, type ThemeTokens } from '../lib/theme-tokens'

function makeTokens(base = '#000000'): ThemeTokens {
  return Object.fromEntries(THEME_TOKEN_KEYS.map((k) => [k, base])) as ThemeTokens
}

describe('useThemeStore', () => {
  beforeEach(() => {
    clearThemeRegistry()
    // Register a builtin so store actions can reference it
    registerTheme({ id: 'dark', name: 'Dark', tokens: makeTokens('#111'), builtin: true })
    // Reset store state
    useThemeStore.setState({ activeThemeId: 'dark', customThemes: {} })
  })

  it('defaults to dark theme', () => {
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('setActiveTheme updates activeThemeId and sets data-theme', () => {
    registerTheme({ id: 'light', name: 'Light', tokens: makeTokens('#fff'), builtin: true })
    useThemeStore.getState().setActiveTheme('light')
    expect(useThemeStore.getState().activeThemeId).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('setActiveTheme ignores unknown theme id', () => {
    useThemeStore.getState().setActiveTheme('nonexistent')
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('createCustomTheme creates and registers theme', () => {
    const id = useThemeStore.getState().createCustomTheme('My Theme', 'dark', { accent: '#ff0000' })
    expect(id).toMatch(/^[0-9a-z]{6}$/)
    expect(useThemeStore.getState().customThemes[id]).toBeDefined()
    expect(useThemeStore.getState().customThemes[id].tokens.accent).toBe('#ff0000')
    expect(useThemeStore.getState().customThemes[id].tokens['surface-primary']).toBe('#111') // from base
    expect(getTheme(id)).toBeDefined()
    expect(getTheme(id)!.builtin).toBe(false)
  })

  it('createCustomTheme id does not collide with builtin or existing custom', () => {
    // Can't fully test rejection sampling but ensure ID is valid
    const id1 = useThemeStore.getState().createCustomTheme('A', 'dark', {})
    const id2 = useThemeStore.getState().createCustomTheme('B', 'dark', {})
    expect(id1).not.toBe(id2)
    expect(id1).not.toBe('dark')
  })

  it('updateCustomTheme patches theme', () => {
    const id = useThemeStore.getState().createCustomTheme('Old', 'dark', {})
    useThemeStore.getState().updateCustomTheme(id, { name: 'New', tokens: { ...makeTokens('#111'), accent: '#00ff00' } })
    expect(useThemeStore.getState().customThemes[id].name).toBe('New')
    expect(useThemeStore.getState().customThemes[id].tokens.accent).toBe('#00ff00')
  })

  it('updateCustomTheme ignores unknown id', () => {
    useThemeStore.getState().updateCustomTheme('nope', { name: 'X' })
    // No error thrown
  })

  it('deleteCustomTheme removes theme and falls back to dark if active', () => {
    const id = useThemeStore.getState().createCustomTheme('Tmp', 'dark', {})
    useThemeStore.getState().setActiveTheme(id)
    useThemeStore.getState().deleteCustomTheme(id)
    expect(useThemeStore.getState().customThemes[id]).toBeUndefined()
    expect(getTheme(id)).toBeUndefined()
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('deleteCustomTheme does not fall back if not active', () => {
    const id = useThemeStore.getState().createCustomTheme('Tmp', 'dark', {})
    // activeThemeId is still 'dark'
    useThemeStore.getState().deleteCustomTheme(id)
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('importTheme validates and creates theme', () => {
    const id = useThemeStore.getState().importTheme({
      name: 'Imported',
      tokens: { accent: '#abcdef' }, // partial — should merge with dark fallback
    })
    expect(id).toMatch(/^[0-9a-z]{6}$/)
    const theme = useThemeStore.getState().customThemes[id]
    expect(theme.name).toBe('Imported')
    expect(theme.tokens.accent).toBe('#abcdef')
    expect(theme.tokens['surface-primary']).toBe('#111') // fallback from dark
  })

  it('importTheme deduplicates name', () => {
    useThemeStore.getState().importTheme({ name: 'Dup', tokens: {} })
    const id2 = useThemeStore.getState().importTheme({ name: 'Dup', tokens: {} })
    expect(useThemeStore.getState().customThemes[id2].name).toBe('Dup (2)')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useThemeStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Write implementation**

```ts
// spa/src/stores/useThemeStore.ts
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
  return generateId() // fallback, astronomically unlikely collision
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
        if (!getTheme(id)) return // ignore unknown
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
          const { [id]: _, ...rest } = s.customThemes
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

        // createCustomTheme handles the merge: { ...dark.tokens, ...overrides }
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
        // Re-register custom themes into registry
        for (const def of Object.values(state.customThemes)) {
          registerTheme(def)
        }
        // Apply active theme to DOM
        applyThemeToDom(state.activeThemeId)
      },
    },
  ),
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/stores/useThemeStore.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useThemeStore.ts spa/src/stores/useThemeStore.test.ts
git commit -m "feat(theme): add theme store with CRUD and persistence"
```

---

## Task 4: CSS Architecture — themes.css + index.css

**Files:**
- Create: `spa/src/styles/themes.css`
- Modify: `spa/src/index.css`

- [ ] **Step 1: Create themes.css with Dark theme and @theme mapping**

```css
/* spa/src/styles/themes.css */

/* === Tailwind token mapping === */
@theme {
  --color-surface-primary: var(--surface-primary);
  --color-surface-secondary: var(--surface-secondary);
  --color-surface-tertiary: var(--surface-tertiary);
  --color-surface-elevated: var(--surface-elevated);
  --color-surface-hover: var(--surface-hover);
  --color-surface-active: var(--surface-active);
  --color-surface-input: var(--surface-input);
  --color-text-primary: var(--text-primary);
  --color-text-secondary: var(--text-secondary);
  --color-text-muted: var(--text-muted);
  --color-text-inverse: var(--text-inverse);
  --color-border-default: var(--border-default);
  --color-border-active: var(--border-active);
  --color-border-subtle: var(--border-subtle);
  --color-accent: var(--accent);
  --color-accent-hover: var(--accent-hover);
  --color-accent-muted: var(--accent-muted);
  --color-terminal-bg: var(--terminal-bg);
  --color-terminal-fg: var(--terminal-fg);
  --color-terminal-cursor: var(--terminal-cursor);
  --color-status-error: var(--status-error);
  --color-status-warning: var(--status-warning);
  --color-status-success: var(--status-success);
}

/* === Dark (default, matches current hardcoded values) === */
[data-theme="dark"] {
  --surface-primary: #0a0a1a;
  --surface-secondary: #12122a;
  --surface-tertiary: #08081a;
  --surface-elevated: #1e1e3e;
  --surface-hover: #1a1a32;
  --surface-active: #272444;
  --surface-input: #2a2a2a;
  --text-primary: #e0e0e0;
  --text-secondary: #9ca3af;
  --text-muted: #6b7280;
  --text-inverse: #0a0a1a;
  --border-default: #404040;
  --border-active: #7a6aaa;
  --border-subtle: #2a2a2a;
  --accent: #7a6aaa;
  --accent-hover: #8a7aba;
  --accent-muted: rgba(122, 106, 170, 0.2);
  --terminal-bg: #0a0a1a;
  --terminal-fg: #e0e0e0;
  --terminal-cursor: #e0e0e0;
  --status-error: #4a3038;
  --status-warning: #4a4028;
  --status-success: #2a4a3a;
}
```

Note: Light, Nord, Dracula themes will be added in Tasks 11-13.

- [ ] **Step 2: Update index.css**

Change `spa/src/index.css` to:
```css
@import "tailwindcss";
@import "./styles/themes.css";
@plugin "@tailwindcss/typography";

body {
  background-color: var(--surface-primary);
}

.scrollbar-hide {
  -ms-overflow-style: none;
  scrollbar-width: none;
}
.scrollbar-hide::-webkit-scrollbar {
  display: none;
}
```

- [ ] **Step 3: Verify build succeeds**

Run: `cd spa && pnpm run build`
Expected: Build succeeds (no color changes yet, Dark values match existing).

- [ ] **Step 4: Commit**

```bash
git add spa/src/styles/themes.css spa/src/index.css
git commit -m "feat(theme): add CSS variable system with Dark theme"
```

---

## Task 5: ThemeInjector + register-themes + main.tsx Wiring

**Files:**
- Create: `spa/src/components/ThemeInjector.tsx`
- Create: `spa/src/lib/register-themes.ts`
- Modify: `spa/src/main.tsx`
- Modify: `spa/src/App.tsx` (mount ThemeInjector only — color migration in Task 6)

- [ ] **Step 1: Create ThemeInjector**

```tsx
// spa/src/components/ThemeInjector.tsx
import { useEffect } from 'react'
import { useThemeStore } from '../stores/useThemeStore'
import { tokensToCss } from '../lib/theme-tokens'

export function ThemeInjector() {
  const activeThemeId = useThemeStore((s) => s.activeThemeId)
  const customThemes = useThemeStore((s) => s.customThemes)
  const custom = customThemes[activeThemeId]

  useEffect(() => {
    if (!custom) return
    const style = document.createElement('style')
    style.dataset.themeId = custom.id
    style.textContent = `[data-theme="${custom.id}"] { ${tokensToCss(custom.tokens)} }`
    document.head.appendChild(style)
    return () => { style.remove() }
  }, [custom])

  return null
}
```

- [ ] **Step 2: Create register-themes.ts with Dark theme data**

```ts
// spa/src/lib/register-themes.ts
import { registerTheme } from './theme-registry'
import type { ThemeTokens } from './theme-tokens'

const darkTokens: ThemeTokens = {
  'surface-primary': '#0a0a1a',
  'surface-secondary': '#12122a',
  'surface-tertiary': '#08081a',
  'surface-elevated': '#1e1e3e',
  'surface-hover': '#1a1a32',
  'surface-active': '#272444',
  'surface-input': '#2a2a2a',
  'text-primary': '#e0e0e0',
  'text-secondary': '#9ca3af',
  'text-muted': '#6b7280',
  'text-inverse': '#0a0a1a',
  'border-default': '#404040',
  'border-active': '#7a6aaa',
  'border-subtle': '#2a2a2a',
  'accent': '#7a6aaa',
  'accent-hover': '#8a7aba',
  'accent-muted': 'rgba(122, 106, 170, 0.2)',
  'terminal-bg': '#0a0a1a',
  'terminal-fg': '#e0e0e0',
  'terminal-cursor': '#e0e0e0',
  'status-error': '#4a3038',
  'status-warning': '#4a4028',
  'status-success': '#2a4a3a',
}

export function registerBuiltinThemes(): void {
  registerTheme({ id: 'dark', name: 'Dark', tokens: darkTokens, builtin: true })
  // Light, Nord, Dracula added in Tasks 11-13
}
```

- [ ] **Step 3: Wire up main.tsx**

Add `registerBuiltinThemes()` call in `spa/src/main.tsx`:
```ts
import { registerBuiltinThemes } from './lib/register-themes'
// ... existing imports ...

registerBuiltinThemes()
registerBuiltinPanes()
```

`registerBuiltinThemes()` should be called before `registerBuiltinPanes()` so that themes are registered before any consumer might reference them.

- [ ] **Step 4: Mount ThemeInjector in App.tsx**

Add inside the root `<Router>` in `spa/src/App.tsx`:
```tsx
import { ThemeInjector } from './components/ThemeInjector'
// ... in return:
<Router>
  <ThemeInjector />
  <div className="h-screen flex bg-[#0a0a1a] text-gray-200">
    {/* ... existing */}
  </div>
</Router>
```

- [ ] **Step 5: Verify build and add data-theme="dark" to html**

The store's `onRehydrateStorage` will set `document.documentElement.dataset.theme = 'dark'` on load. Verify:

Run: `cd spa && pnpm run build`
Expected: Build succeeds.

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/ThemeInjector.tsx spa/src/lib/register-themes.ts spa/src/main.tsx spa/src/App.tsx
git commit -m "feat(theme): wire ThemeInjector + register-themes + main.tsx"
```

---

## Task 6: Color Migration — Core Layout

Migrate hardcoded colors to semantic tokens in core layout components. After this task, the app should look identical (Dark values match current).

**Files:**
- Modify: `spa/src/App.tsx`
- Modify: `spa/src/components/TabBar.tsx`
- Modify: `spa/src/components/ActivityBar.tsx`
- Modify: `spa/src/components/StatusBar.tsx`
- Modify: `spa/src/components/TerminalView.tsx`

- [ ] **Step 1: Migrate App.tsx**

Replace `bg-[#0a0a1a]` → `bg-surface-primary`, `text-gray-200` → `text-text-primary`.

- [ ] **Step 2: Migrate TabBar.tsx**

Replace `bg-[#12122a]` → `bg-surface-secondary`, `text-gray-*` → semantic tokens.

- [ ] **Step 3: Migrate ActivityBar.tsx**

Replace `bg-[#08081a]` → `bg-surface-tertiary`, related colors.

- [ ] **Step 4: Migrate StatusBar.tsx**

Replace `bg-[#12122a]` → `bg-surface-secondary`, dropdown colors → `bg-surface-elevated`.

- [ ] **Step 5: Migrate TerminalView.tsx**

Replace `#0a0a1a` → `bg-terminal-bg`.

- [ ] **Step 6: Run lint + build**

Run: `cd spa && pnpm run lint && pnpm run build`
Expected: Clean.

- [ ] **Step 7: Commit**

```bash
git add spa/src/App.tsx spa/src/components/TabBar.tsx spa/src/components/ActivityBar.tsx spa/src/components/StatusBar.tsx spa/src/components/TerminalView.tsx
git commit -m "refactor(theme): migrate core layout to semantic tokens"
```

---

## Task 7: Color Migration — Tab Components

**Files:**
- Modify: `spa/src/components/SortableTab.tsx`
- Modify: `spa/src/components/TabBar.tsx`
- Modify: `spa/src/components/TabContextMenu.tsx`

- [ ] **Step 1: Migrate SortableTab.tsx colors**

`#12122a` → `bg-surface-secondary`, `#272444` → `bg-surface-active`, `#1a1a32` → `bg-surface-hover`, `text-gray-*` → semantic text tokens.

- [ ] **Step 2: Migrate TabBar.tsx colors**

Background and text colors → semantic tokens.

- [ ] **Step 3: Migrate TabContextMenu.tsx**

`#1e1e2e` → `bg-surface-elevated`, hover/text colors.

- [ ] **Step 4: Run lint + build**

Run: `cd spa && pnpm run lint && pnpm run build`

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SortableTab.tsx spa/src/components/TabBar.tsx spa/src/components/TabContextMenu.tsx
git commit -m "refactor(theme): migrate tab components to semantic tokens"
```

---

## Task 8: Color Migration — Settings Components

**Files:**
- Modify: `spa/src/components/settings/AppearanceSection.tsx`
- Modify: `spa/src/components/settings/TerminalSection.tsx`
- Modify: `spa/src/components/settings/SettingsSidebar.tsx`
- Modify: `spa/src/components/settings/SettingItem.tsx`
- Modify: `spa/src/components/settings/SegmentControl.tsx`
- Modify: `spa/src/components/settings/ToggleSwitch.tsx`

- [ ] **Step 1: Migrate settings components**

Pattern:
- `bg-[#1e1e3e]` → `bg-surface-elevated`
- `bg-[#2a2a2a]` → `bg-surface-input`
- `border-[#7a6aaa]` → `border-border-active`
- `border-[#404040]` → `border-border-default`
- `bg-[#7a6aaa]` → `bg-accent`
- `text-gray-200` → `text-text-primary`
- `text-gray-400` → `text-text-secondary`
- `text-gray-500` → `text-text-muted`
- `text-gray-300` → `text-text-primary` or `text-text-secondary` based on context

- [ ] **Step 2: Run lint + tests + build**

Run: `cd spa && pnpm run lint && npx vitest run && pnpm run build`

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/settings/
git commit -m "refactor(theme): migrate settings components to semantic tokens"
```

---

## Task 9: Color Migration — Session + Stream Components

**Files:**
- Modify: `spa/src/components/SessionPicker.tsx`
- Modify: `spa/src/components/SessionPaneContent.tsx`
- Modify: `spa/src/components/SessionPanel.tsx`
- Modify: `spa/src/components/ConversationView.tsx`
- Modify: `spa/src/components/MessageBubble.tsx`
- Modify: `spa/src/components/ToolCallBlock.tsx`
- Modify: `spa/src/components/ToolResultBlock.tsx`
- Modify: `spa/src/components/ThinkingBlock.tsx`
- Modify: `spa/src/components/ThinkingIndicator.tsx`
- Modify: `spa/src/components/StreamInput.tsx`
- Modify: `spa/src/components/TopBar.tsx`
- Modify: `spa/src/components/FileAttachment.tsx`
- Modify: `spa/src/components/HandoffButton.tsx`
- Modify: `spa/src/components/PermissionPrompt.tsx`
- Modify: `spa/src/components/AskUserQuestion.tsx` (if exists)
- Modify: `spa/src/components/SessionStatusBadge.tsx` (if exists)
- Modify: `spa/src/components/DashboardPage.tsx`
- Modify: `spa/src/components/HistoryPage.tsx`
- Modify: `spa/src/components/NewTabPage.tsx`
- Modify: `spa/src/components/PaneLayoutRenderer.tsx`

- [ ] **Step 1: Migrate session components**

SessionPicker: `#1e1e3e` → `bg-surface-elevated`

- [ ] **Step 2: Migrate stream/conversation components**

Follow spec Section 11 migration decision principles:
1. Map to closest semantic token
2. Add component token if shared by multiple components
3. Keep + `/* TODO: theme token */` if single-use and ambiguous

- [ ] **Step 3: Migrate remaining components**

DashboardPage, HistoryPage, NewTabPage, PaneLayoutRenderer — any hardcoded colors.

- [ ] **Step 4: Run lint + build**

Run: `cd spa && pnpm run lint && pnpm run build`

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/
git commit -m "refactor(theme): migrate session and stream components to semantic tokens"
```

---

## Task 10: Migration — Test Files

**Files:**
- Modify: All test files found by grep in Step 1 (known: `ToggleSwitch.test.tsx`, `SegmentControl.test.tsx`, `TabBar.test.tsx`, `TopBar.test.tsx`, `ConversationView.test.tsx`, `SessionPanel.test.tsx`, `ToolResultBlock.test.tsx`, `MessageBubble.test.tsx`, `SessionStatusBadge.test.tsx`)

- [ ] **Step 1: Find all test files with hardcoded color assertions**

Run: `cd spa && grep -r 'bg-\[#\|text-\[#\|border-\[#\|text-gray-\|bg-gray-\|border-gray-' src/ --include='*.test.*' -l`

- [ ] **Step 2: Update assertions to use new token classes**

Example: `toContain('bg-[#7a6aaa]')` → `toContain('bg-accent')`

- [ ] **Step 3: Run all tests**

Run: `cd spa && npx vitest run`
Expected: All tests pass.

- [ ] **Step 4: Commit**

Stage only the test files found in Step 1:
```bash
git add spa/src/**/*.test.ts spa/src/**/*.test.tsx
git commit -m "test(theme): update color assertions to semantic tokens"
```

---

## Task 11: xterm.js Integration

**Files:**
- Modify: `spa/src/hooks/useTerminal.ts`

- [ ] **Step 1: Replace hardcoded theme in useTerminal.ts**

Change the Terminal constructor theme from:
```ts
theme: { background: '#0a0a1a', foreground: '#e0e0e0' },
```
to reading from CSS variables:
```ts
function getTerminalTheme() {
  const style = getComputedStyle(document.documentElement)
  return {
    background: style.getPropertyValue('--terminal-bg').trim() || '#0a0a1a',
    foreground: style.getPropertyValue('--terminal-fg').trim() || '#e0e0e0',
    cursor: style.getPropertyValue('--terminal-cursor').trim() || '#e0e0e0',
  }
}

// In Terminal constructor:
theme: getTerminalTheme(),
```

Also subscribe to theme changes and update `term.options.theme` when it changes. Add a `useEffect` that subscribes using Zustand's plain `.subscribe()` (no `subscribeWithSelector` middleware needed):

```ts
useEffect(() => {
  let prevThemeId = useThemeStore.getState().activeThemeId
  const unsub = useThemeStore.subscribe((state) => {
    if (state.activeThemeId === prevThemeId) return
    prevThemeId = state.activeThemeId
    if (!termRef.current) return
    // CSS variables update after data-theme change, read on next frame
    requestAnimationFrame(() => {
      if (termRef.current) {
        termRef.current.options.theme = getTerminalTheme()
      }
    })
  })
  return unsub
}, [])
```

- [ ] **Step 2: Run build**

Run: `cd spa && pnpm run build`
Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add spa/src/hooks/useTerminal.ts
git commit -m "feat(theme): read xterm theme from CSS variables"
```

---

## Task 12: Light Theme

**Files:**
- Modify: `spa/src/styles/themes.css`
- Modify: `spa/src/lib/register-themes.ts`

- [ ] **Step 1: Define Light theme token values**

Research and define a clean light theme. Add to `themes.css`:

```css
[data-theme="light"] {
  --surface-primary: #f5f5f5;
  --surface-secondary: #e8e8e8;
  --surface-tertiary: #f0f0f0;
  --surface-elevated: #ffffff;
  --surface-hover: #e0e0e0;
  --surface-active: #d4d0e8;
  --surface-input: #ffffff;
  --text-primary: #1a1a2e;
  --text-secondary: #4a4a5a;
  --text-muted: #8a8a9a;
  --text-inverse: #f5f5f5;
  --border-default: #d0d0d0;
  --border-active: #6a5a9a;
  --border-subtle: #e0e0e0;
  --accent: #6a5a9a;
  --accent-hover: #5a4a8a;
  --accent-muted: rgba(106, 90, 154, 0.15);
  --terminal-bg: #f5f5f5;
  --terminal-fg: #1a1a2e;
  --terminal-cursor: #1a1a2e;
  --status-error: #fce4e4;
  --status-warning: #fef3cd;
  --status-success: #d4edda;
}
```

- [ ] **Step 2: Register Light theme in register-themes.ts**

Add `lightTokens` object and `registerTheme({ id: 'light', name: 'Light', tokens: lightTokens, builtin: true })`.

- [ ] **Step 3: Verify build**

Run: `cd spa && pnpm run build`

- [ ] **Step 4: Commit**

```bash
git add spa/src/styles/themes.css spa/src/lib/register-themes.ts
git commit -m "feat(theme): add Light theme"
```

---

## Task 13: Nord Theme

**Files:**
- Modify: `spa/src/styles/themes.css`
- Modify: `spa/src/lib/register-themes.ts`

- [ ] **Step 1: Define Nord theme token values**

Based on Nord palette (nordtheme.com):

```css
[data-theme="nord"] {
  --surface-primary: #2e3440;
  --surface-secondary: #3b4252;
  --surface-tertiary: #2e3440;
  --surface-elevated: #434c5e;
  --surface-hover: #434c5e;
  --surface-active: #4c566a;
  --surface-input: #3b4252;
  --text-primary: #eceff4;
  --text-secondary: #d8dee9;
  --text-muted: #7b88a1;
  --text-inverse: #2e3440;
  --border-default: #4c566a;
  --border-active: #88c0d0;
  --border-subtle: #3b4252;
  --accent: #88c0d0;
  --accent-hover: #8fbcbb;
  --accent-muted: rgba(136, 192, 208, 0.2);
  --terminal-bg: #2e3440;
  --terminal-fg: #d8dee9;
  --terminal-cursor: #d8dee9;
  --status-error: #bf616a33;
  --status-warning: #ebcb8b33;
  --status-success: #a3be8c33;
}
```

- [ ] **Step 2: Register Nord theme**

- [ ] **Step 3: Verify build + Commit**

```bash
git add spa/src/styles/themes.css spa/src/lib/register-themes.ts
git commit -m "feat(theme): add Nord theme"
```

---

## Task 14: Dracula Theme

**Files:**
- Modify: `spa/src/styles/themes.css`
- Modify: `spa/src/lib/register-themes.ts`

- [ ] **Step 1: Define Dracula theme token values**

Based on Dracula palette (draculatheme.com):

```css
[data-theme="dracula"] {
  --surface-primary: #282a36;
  --surface-secondary: #21222c;
  --surface-tertiary: #191a21;
  --surface-elevated: #44475a;
  --surface-hover: #44475a;
  --surface-active: #6272a4;
  --surface-input: #21222c;
  --text-primary: #f8f8f2;
  --text-secondary: #bfbfbf;
  --text-muted: #6272a4;
  --text-inverse: #282a36;
  --border-default: #44475a;
  --border-active: #bd93f9;
  --border-subtle: #21222c;
  --accent: #bd93f9;
  --accent-hover: #caa9fa;
  --accent-muted: rgba(189, 147, 249, 0.2);
  --terminal-bg: #282a36;
  --terminal-fg: #f8f8f2;
  --terminal-cursor: #f8f8f2;
  --status-error: #ff555533;
  --status-warning: #f1fa8c33;
  --status-success: #50fa7b33;
}
```

- [ ] **Step 2: Register Dracula theme**

- [ ] **Step 3: Verify build + Commit**

```bash
git add spa/src/styles/themes.css spa/src/lib/register-themes.ts
git commit -m "feat(theme): add Dracula theme"
```

---

## Task 15: AppearanceSection — Theme Selector

**Files:**
- Modify: `spa/src/components/settings/AppearanceSection.tsx`

- [ ] **Step 1: Write test for theme selector**

```tsx
// Add to existing test file or create new
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { AppearanceSection } from './AppearanceSection'
import { useThemeStore } from '../../stores/useThemeStore'
import { clearThemeRegistry, registerTheme } from '../../lib/theme-registry'
import { THEME_TOKEN_KEYS, type ThemeTokens } from '../../lib/theme-tokens'

function makeTokens(base = '#000'): ThemeTokens {
  return Object.fromEntries(THEME_TOKEN_KEYS.map((k) => [k, base])) as ThemeTokens
}

describe('AppearanceSection', () => {
  beforeEach(() => {
    clearThemeRegistry()
    registerTheme({ id: 'dark', name: 'Dark', tokens: makeTokens(), builtin: true })
    registerTheme({ id: 'light', name: 'Light', tokens: makeTokens('#fff'), builtin: true })
    useThemeStore.setState({ activeThemeId: 'dark', customThemes: {} })
  })

  it('renders theme selector with current theme', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Dark')).toBeInTheDocument()
  })

  it('switches theme on selection', () => {
    render(<AppearanceSection />)
    fireEvent.click(screen.getByText('Light'))
    expect(useThemeStore.getState().activeThemeId).toBe('light')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

- [ ] **Step 3: Rewrite AppearanceSection with working theme selector**

Replace the disabled buttons with a dropdown that lists all themes from `getAllThemes()`, grouped by builtin/custom. Add "Customize" button that opens ThemeEditor. Add "Import" button that opens ThemeImportModal.

Wire to `useThemeStore.setActiveTheme()`.

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run`

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/AppearanceSection.tsx spa/src/components/settings/AppearanceSection.test.tsx
git commit -m "feat(theme): enable theme selector in AppearanceSection"
```

---

## Task 16: ThemeEditor Component

**Files:**
- Create: `spa/src/components/settings/ThemeEditor.tsx`
- Create: `spa/src/components/settings/ThemeEditor.test.tsx`

- [ ] **Step 1: Write failing tests**

Test the core flows:
1. Editor shows current theme's token values
2. Changing a color picker updates the preview (temp style element exists)
3. Save creates custom theme in store
4. Cancel removes temp style and restores original theme
5. Reset restores base theme values in editor

- [ ] **Step 2: Run test to verify it fails**

- [ ] **Step 3: Implement ThemeEditor**

Structure:
```tsx
export function ThemeEditor({ baseThemeId, onClose }: Props) {
  // State: editingTokens (local), themeName (local), tempStyleRef
  // On mount: fork base theme tokens into local state
  // On token change: update local state + inject temp <style>
  // Save: createCustomTheme() → setActiveTheme() → onClose()
  // Cancel: remove temp style → restore data-theme → onClose()
  // Reset: restore tokens from base theme
}
```

Token groups rendered using `TOKEN_METADATA` grouped by `group`, each group is a collapsible accordion section. Each token row: label + color swatch + `<input type="color">` + hex text input.

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run`

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/ThemeEditor.tsx spa/src/components/settings/ThemeEditor.test.tsx
git commit -m "feat(theme): add ThemeEditor with live preview"
```

---

## Task 17: ThemeImportModal Component

**Files:**
- Create: `spa/src/components/settings/ThemeImportModal.tsx`
- Create: `spa/src/components/settings/ThemeImportModal.test.tsx`

- [ ] **Step 1: Write failing tests**

Test:
1. JSON paste: valid JSON creates theme
2. JSON paste: invalid JSON shows error
3. JSON paste: missing `name` shows error
4. JSON paste: partial tokens merged with Dark fallback
5. File upload: reads file and imports
6. URL fetch: success creates theme
7. URL fetch: CORS error shows helpful message
8. Duplicate name gets suffix

- [ ] **Step 2: Run test to verify it fails**

- [ ] **Step 3: Implement ThemeImportModal**

Structure:
```tsx
export function ThemeImportModal({ onClose, onImported }: Props) {
  // Tabs: "Paste JSON" | "File" | "URL"
  // Paste: <textarea> + "Import" button
  // File: <input type="file" accept=".json"> or drag-drop
  // URL: <input type="url"> + "Fetch" button
  // Validation: parseAndValidate() → error message or importTheme()
  // Success: onImported(themeId) → close
}
```

Validation function:
```ts
function parseAndValidate(raw: unknown): ThemeImportPayload | string {
  if (!raw || typeof raw !== 'object') return 'Invalid JSON'
  const obj = raw as Record<string, unknown>
  if (typeof obj.name !== 'string' || !obj.name.trim()) return 'Missing "name" field'
  if (!obj.tokens || typeof obj.tokens !== 'object') return 'Missing "tokens" field'
  const validKeys = new Set(THEME_TOKEN_KEYS as readonly string[])
  const tokens: Partial<ThemeTokens> = {}
  let hasValid = false
  for (const [k, v] of Object.entries(obj.tokens as Record<string, unknown>)) {
    if (validKeys.has(k) && typeof v === 'string') {
      tokens[k as ThemeTokenKey] = v
      hasValid = true
    }
  }
  if (!hasValid) return 'No valid token keys found'
  return { name: obj.name.trim(), tokens }
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run`

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/ThemeImportModal.tsx spa/src/components/settings/ThemeImportModal.test.tsx
git commit -m "feat(theme): add ThemeImportModal with JSON/file/URL support"
```

---

## Task 18: Theme Export + Final Integration

**Files:**
- Modify: `spa/src/components/settings/AppearanceSection.tsx` (add export button for custom themes)

- [ ] **Step 1: Add export function**

```ts
function exportTheme(theme: ThemeDefinition) {
  const data = JSON.stringify({ name: theme.name, tokens: theme.tokens }, null, 2)
  const blob = new Blob([data], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${theme.name}.json`
  a.click()
  URL.revokeObjectURL(url)
}
```

- [ ] **Step 2: Wire export button in custom theme list**

Custom themes in the selector dropdown get an export icon button.

- [ ] **Step 3: Run full test suite + lint + build**

Run: `cd spa && pnpm run lint && npx vitest run && pnpm run build`
Expected: All pass.

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/settings/AppearanceSection.tsx
git commit -m "feat(theme): add theme export and final integration"
```

---

## Task 19: Final Verification

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All tests pass.

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors.

- [ ] **Step 3: Run build**

Run: `cd spa && pnpm run build`
Expected: Clean build.

- [ ] **Step 4: Manual smoke test**

Open `http://100.64.0.2:5174` and verify:
1. Default Dark theme looks identical to before
2. Switch to Light — all UI elements readable
3. Switch to Nord — cold blue/green palette
4. Switch to Dracula — purple palette
5. Customize: fork Dark, change accent, save, verify it applies
6. Import: paste a JSON theme, verify it appears
7. Export: download a custom theme JSON
8. Delete custom theme: verify fallback to Dark
9. Terminal: verify bg/fg/cursor change with theme
10. Refresh: theme persists

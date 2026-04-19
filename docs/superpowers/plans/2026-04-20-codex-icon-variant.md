# Codex Icon Variant Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give Codex CLI tabs an icon-variant choice (OpenAI logo vs Codex logo), mirroring the existing CC `bot`/`star` pattern, persisted per user, defaulting to OpenAI.

**Architecture:** SPA-only. Extend `useAgentStore` with `codexIconVariant` ('openai' | 'codex'), bump persist version 4 → 5. Extend `getAgentIcon`'s `GetAgentIconOptions` with `codexVariant`. `useTabDisplay` reads the new field and forwards it. Add a Settings → Terminal row below the existing CC icon row. Import monochrome `codex.svg` from `@lobehub/icons-static-svg` (uses `currentColor`, inherits tab theme).

**Tech Stack:** Zustand 5 + persist + `syncManager`, React 19, Vite 8 + `vite-plugin-svgr`, Phosphor Icons, `@lobehub/icons-static-svg`, Vitest + @testing-library/react, Tailwind 4.

**Spec:** `docs/superpowers/specs/2026-04-20-codex-icon-variant-design.md`

---

## File Structure

**Modify:**
- `spa/src/stores/useAgentStore.ts` — new type, state field, action, partialize, version bump
- `spa/src/lib/agent-icons.tsx` — `CODEX_VARIANTS`, `codexVariant` option, branch change
- `spa/src/lib/agent-icons.test.tsx` — updated codex-branch tests
- `spa/src/hooks/useTabDisplay.ts` — read `codexIconVariant`, pass in options
- `spa/src/hooks/useTabDisplay.test.ts` — add `codexIconVariant: 'openai'` to fixture
- `spa/src/features/workspace/components/InlineTab.test.tsx` — add `codexIconVariant: 'openai'` to fixture
- `spa/src/components/settings/TerminalSection.tsx` — new Codex icon `SettingItem` row
- `spa/src/components/settings/TerminalSection.test.tsx` — codex button click test + fixture update
- `spa/src/locales/en.json` — 5 new `settings.terminal.codex_icon.*` keys
- `spa/src/locales/zh-TW.json` — 5 new `settings.terminal.codex_icon.*` keys
- `CHANGELOG.md` — entry under the current alpha heading

**Create:** _none_

---

## Task 1: Add `codexIconVariant` to `useAgentStore`

**Files:**
- Modify: `spa/src/stores/useAgentStore.ts`

Define the type, default state, setter, persist partialize, and bump persist version.

- [ ] **Step 1: Add the type export**

In `spa/src/stores/useAgentStore.ts`, find the existing line `export type CcIconVariant = 'bot' | 'star'` and add directly below:

```ts
export type CodexIconVariant = 'openai' | 'codex'
```

- [ ] **Step 2: Add the state field to the `AgentState` interface**

Find `ccIconVariant: CcIconVariant` in the `AgentState` interface (around line 69) and add directly below:

```ts
  codexIconVariant: CodexIconVariant
```

- [ ] **Step 3: Add the action signature to the `AgentState` interface**

Find `setCcIconVariant: (variant: CcIconVariant) => void` (around line 78) and add directly below:

```ts
  setCodexIconVariant: (variant: CodexIconVariant) => void
```

- [ ] **Step 4: Add the default state**

Find `ccIconVariant: 'bot' as CcIconVariant,` (around line 97) and add directly below:

```ts
      codexIconVariant: 'openai' as CodexIconVariant,
```

- [ ] **Step 5: Add the setter implementation**

Find `setCcIconVariant: (variant) => set({ ccIconVariant: variant }),` (around line 191) and add directly below:

```ts
      setCodexIconVariant: (variant) => set({ codexIconVariant: variant }),
```

- [ ] **Step 6: Bump persist version and add to `partialize`**

Find the persist config (around lines 242-250) and change `version: 4` to `version: 5`, and add `codexIconVariant: state.codexIconVariant,` to the `partialize` return object. The final block should read:

```ts
    {
      name: STORAGE_KEYS.AGENT,
      storage: purdexStorage,
      version: 5,
      partialize: (state) => ({
        tabIndicatorStyle: state.tabIndicatorStyle,
        ccIconVariant: state.ccIconVariant,
        codexIconVariant: state.codexIconVariant,
        showOscTitle: state.showOscTitle,
      }),
    },
```

- [ ] **Step 7: Run existing tests to confirm no regressions**

Run: `cd spa && npx vitest run src/stores/useAgentStore.test.ts`
Expected: all green (no new tests yet; this confirms the store still compiles and behaves).

- [ ] **Step 8: Typecheck**

Run: `cd spa && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 9: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-icon-variant
git add spa/src/stores/useAgentStore.ts
git commit -m "feat(agent-store): add codexIconVariant with openai default

Introduce CodexIconVariant type and codexIconVariant state + setter;
include in partialize; bump persist version 4 -> 5.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Extend the icon registry (`agent-icons.tsx`)

**Files:**
- Modify: `spa/src/lib/agent-icons.tsx`
- Test: `spa/src/lib/agent-icons.test.tsx`

Add the `codex.svg` import, the `CODEX_VARIANTS` map, extend `GetAgentIconOptions`, and make the codex branch of `getAgentIcon` respect the new option.

- [ ] **Step 1: Update the test file to describe the new behavior (failing test)**

Replace the entire contents of `spa/src/lib/agent-icons.test.tsx` with:

```tsx
import { describe, it, expect } from 'vitest'
import { getAgentIcon, CC_ICON_VARIANTS, CODEX_ICON_VARIANTS } from './agent-icons'

describe('getAgentIcon', () => {
  it('returns bot variant for cc when ccVariant=bot', () => {
    expect(getAgentIcon('cc', { ccVariant: 'bot', codexVariant: 'openai' })).toBe(CC_ICON_VARIANTS.bot)
  })

  it('returns star variant for cc when ccVariant=star', () => {
    expect(getAgentIcon('cc', { ccVariant: 'star', codexVariant: 'openai' })).toBe(CC_ICON_VARIANTS.star)
  })

  it('returns distinct components for bot vs star', () => {
    expect(CC_ICON_VARIANTS.bot).not.toBe(CC_ICON_VARIANTS.star)
  })

  it('returns openai variant for codex when codexVariant=openai', () => {
    expect(getAgentIcon('codex', { ccVariant: 'bot', codexVariant: 'openai' })).toBe(CODEX_ICON_VARIANTS.openai)
  })

  it('returns codex variant for codex when codexVariant=codex', () => {
    expect(getAgentIcon('codex', { ccVariant: 'bot', codexVariant: 'codex' })).toBe(CODEX_ICON_VARIANTS.codex)
  })

  it('returns distinct components for openai vs codex', () => {
    expect(CODEX_ICON_VARIANTS.openai).not.toBe(CODEX_ICON_VARIANTS.codex)
  })

  it('codex branch ignores ccVariant and respects codexVariant', () => {
    const withBot = getAgentIcon('codex', { ccVariant: 'bot', codexVariant: 'codex' })
    const withStar = getAgentIcon('codex', { ccVariant: 'star', codexVariant: 'codex' })
    expect(withBot).toBe(CODEX_ICON_VARIANTS.codex)
    expect(withStar).toBe(CODEX_ICON_VARIANTS.codex)
  })

  it('returns undefined for unknown agent types', () => {
    expect(getAgentIcon('gemini', { ccVariant: 'bot', codexVariant: 'openai' })).toBeUndefined()
    expect(getAgentIcon('', { ccVariant: 'bot', codexVariant: 'openai' })).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/agent-icons.test.tsx`
Expected: FAIL — `CODEX_ICON_VARIANTS` does not exist, and `GetAgentIconOptions` rejects `codexVariant`.

- [ ] **Step 3: Replace `spa/src/lib/agent-icons.tsx` with the new implementation**

Overwrite the file contents with:

```tsx
/// <reference types="vite-plugin-svgr/client" />
import type { ComponentType, SVGProps } from 'react'
import { OpenAiLogo } from '@phosphor-icons/react'
import ClaudeCodeBotSvg from '@lobehub/icons-static-svg/icons/claudecode.svg?react'
import ClaudeStarSvg from '@lobehub/icons-static-svg/icons/claude.svg?react'
import CodexLobeSvg from '@lobehub/icons-static-svg/icons/codex.svg?react'
import type { CcIconVariant, CodexIconVariant } from '../stores/useAgentStore'

type SvgComponent = ComponentType<SVGProps<SVGSVGElement>>

export type AgentIconComponent = ComponentType<{ size: number; className?: string }>

function wrapSvg(Svg: SvgComponent): AgentIconComponent {
  return function AgentBrandIcon({ size, className }) {
    return <Svg width={size} height={size} className={className} />
  }
}

function CodexOpenAiIcon({ size, className }: { size: number; className?: string }) {
  return <OpenAiLogo size={size} className={className} />
}

const CC_VARIANTS: Record<CcIconVariant, AgentIconComponent> = {
  bot: wrapSvg(ClaudeCodeBotSvg),
  star: wrapSvg(ClaudeStarSvg),
}

const CODEX_VARIANTS: Record<CodexIconVariant, AgentIconComponent> = {
  openai: CodexOpenAiIcon,
  codex: wrapSvg(CodexLobeSvg),
}

export interface GetAgentIconOptions {
  ccVariant: CcIconVariant
  codexVariant: CodexIconVariant
}

// This file is a component registry — every export resolves to a component.
// eslint-disable-next-line react-refresh/only-export-components
export function getAgentIcon(agentType: string, options: GetAgentIconOptions): AgentIconComponent | undefined {
  if (agentType === 'cc') return CC_VARIANTS[options.ccVariant]
  if (agentType === 'codex') return CODEX_VARIANTS[options.codexVariant]
  return undefined
}

/** Icon components for each cc variant — exposed so Settings can render a live preview. */
export const CC_ICON_VARIANTS = CC_VARIANTS

/** Icon components for each codex variant — exposed so Settings can render a live preview. */
export const CODEX_ICON_VARIANTS = CODEX_VARIANTS
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd spa && npx vitest run src/lib/agent-icons.test.tsx`
Expected: PASS — 8 tests.

- [ ] **Step 5: Verify expected typecheck failure**

Run: `cd spa && npx tsc --noEmit`
Expected: **one error** at `src/hooks/useTabDisplay.ts` — `getAgentIcon` is called without `codexVariant`. This is expected; the consumer is updated in Task 3. Do NOT fix it here.

If you see errors anywhere else, stop and investigate before committing.

- [ ] **Step 6: Commit (repo will be red on typecheck until Task 3 lands)**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-icon-variant
git add spa/src/lib/agent-icons.tsx spa/src/lib/agent-icons.test.tsx
git commit -m "feat(agent-icons): add codex openai/codex variants

Mirror the CC bot/star variant pattern for codex sessions: wrap the
existing OpenAiLogo path as CodexOpenAiIcon, add the monochrome
lobehub codex.svg as the second variant, and make getAgentIcon's
codex branch switch on options.codexVariant.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Thread `codexVariant` through `useTabDisplay`

**Files:**
- Modify: `spa/src/hooks/useTabDisplay.ts`
- Test: `spa/src/hooks/useTabDisplay.test.ts`

Read the new field from the store and pass it into `getAgentIcon`.

- [ ] **Step 1: Update the test fixture — add `codexIconVariant: 'openai'` to the `beforeEach` seed**

In `spa/src/hooks/useTabDisplay.test.ts`, find the `useAgentStore.setState({...})` block in `beforeEach` (around lines 31-40) and add `codexIconVariant: 'openai',` directly after the `ccIconVariant: 'bot',` line. The block becomes:

```ts
  useAgentStore.setState({
    unread: {},
    statuses: {},
    subagents: {},
    agentTypes: {},
    oscTitles: {},
    tabIndicatorStyle: 'badge',
    ccIconVariant: 'bot',
    codexIconVariant: 'openai',
    showOscTitle: false,
  })
```

- [ ] **Step 2: Run the tests to verify they still pass (store still rehydrates the field from defaults)**

Run: `cd spa && npx vitest run src/hooks/useTabDisplay.test.ts`
Expected: all green (test still passes because the hook still uses `ccVariant: ccIconVariant` only).

Note: this step just makes the fixture explicit before the source change; the real failure comes from the next step.

- [ ] **Step 3: Update `useTabDisplay.ts` to read and forward `codexIconVariant`**

In `spa/src/hooks/useTabDisplay.ts`, find the `ccIconVariant` selector (line 53) and add a sibling selector directly below:

```ts
  const codexIconVariant = useAgentStore((s) => s.codexIconVariant)
```

Then find the `getAgentIcon(...)` call (line 65) and update its options object:

```ts
  const agentIcon = !isTerminated && agentType ? getAgentIcon(agentType, { ccVariant: ccIconVariant, codexVariant: codexIconVariant }) : undefined
```

- [ ] **Step 4: Run useTabDisplay tests**

Run: `cd spa && npx vitest run src/hooks/useTabDisplay.test.ts`
Expected: all green.

- [ ] **Step 5: Typecheck**

Run: `cd spa && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-icon-variant
git add spa/src/hooks/useTabDisplay.ts spa/src/hooks/useTabDisplay.test.ts
git commit -m "feat(tab-display): forward codexIconVariant to getAgentIcon

Read codexIconVariant from useAgentStore and include it in the
GetAgentIconOptions passed to getAgentIcon so codex tabs render
the user-selected variant.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Update `InlineTab` test fixture

**Files:**
- Modify: `spa/src/features/workspace/components/InlineTab.test.tsx`

Keep test isolation explicit.

- [ ] **Step 1: Add `codexIconVariant: 'openai'` to the `beforeEach` seed**

In `spa/src/features/workspace/components/InlineTab.test.tsx`, find the `useAgentStore.setState({...})` block in `beforeEach` (around lines 40-49) and add `codexIconVariant: 'openai',` directly after the `ccIconVariant: 'bot',` line. The block becomes:

```ts
  useAgentStore.setState({
    statuses: {},
    unread: {},
    subagents: {},
    agentTypes: {},
    oscTitles: {},
    showOscTitle: false,
    tabIndicatorStyle: 'badge',
    ccIconVariant: 'bot',
    codexIconVariant: 'openai',
  })
```

- [ ] **Step 2: Run the tests**

Run: `cd spa && npx vitest run src/features/workspace/components/InlineTab.test.tsx`
Expected: all green.

- [ ] **Step 3: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-icon-variant
git add spa/src/features/workspace/components/InlineTab.test.tsx
git commit -m "test(inline-tab): seed codexIconVariant in beforeEach

Keep test isolation explicit now that the agent store has a new
persisted field.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Add i18n keys

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

Five new keys per locale. Wording parallels the CC icon row.

- [ ] **Step 1: Add keys to `spa/src/locales/en.json`**

Find the `"settings.terminal.cc_icon.hidden_hint"` entry and add these five lines directly below it:

```json
  "settings.terminal.codex_icon.label": "Codex icon",
  "settings.terminal.codex_icon.desc": "Which icon Codex sessions use on the tab",
  "settings.terminal.codex_icon.openai": "OpenAI",
  "settings.terminal.codex_icon.codex": "Codex",
  "settings.terminal.codex_icon.hidden_hint": "Current tab indicator style hides the icon; this setting has no visible effect until you switch mode.",
```

- [ ] **Step 2: Add keys to `spa/src/locales/zh-TW.json`**

Find the `"settings.terminal.cc_icon.hidden_hint"` entry and add these five lines directly below it:

```json
  "settings.terminal.codex_icon.label": "Codex 圖示",
  "settings.terminal.codex_icon.desc": "Codex session 在分頁標籤上使用的圖示",
  "settings.terminal.codex_icon.openai": "OpenAI",
  "settings.terminal.codex_icon.codex": "Codex",
  "settings.terminal.codex_icon.hidden_hint": "目前分頁標籤顯示方式不會顯示圖示，切換到其他模式後才會生效。",
```

- [ ] **Step 3: Run the locale completeness test**

Run: `cd spa && npx vitest run src/locales/locale-completeness.test.ts`
Expected: all green — en and zh-TW have identical key sets and no empty values.

- [ ] **Step 4: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-icon-variant
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "i18n: add codex_icon setting strings

Five new settings.terminal.codex_icon.* keys in en + zh-TW for the
new Codex icon variant choice.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Add the Codex icon row to `TerminalSection`

**Files:**
- Modify: `spa/src/components/settings/TerminalSection.tsx`
- Test: `spa/src/components/settings/TerminalSection.test.tsx`

Add a new `SettingItem` row directly below the existing CC icon row, sharing the button-group + live-preview pattern.

- [ ] **Step 1: Update test fixture — add `codexIconVariant: 'openai'` to `beforeEach`**

In `spa/src/components/settings/TerminalSection.test.tsx`, line 16 reads:

```ts
    useAgentStore.setState({ tabIndicatorStyle: 'badge', ccIconVariant: 'bot', showOscTitle: false })
```

Change it to:

```ts
    useAgentStore.setState({ tabIndicatorStyle: 'badge', ccIconVariant: 'bot', codexIconVariant: 'openai', showOscTitle: false })
```

- [ ] **Step 2: Add failing test for the Codex button**

Directly after the existing `it('updates ccIconVariant when a cc icon button is clicked', ...)` block (around lines 111-117), add:

```tsx
  it('updates codexIconVariant when a codex icon button is clicked', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByRole('button', { name: /^Codex$/ }))
    expect(useAgentStore.getState().codexIconVariant).toBe('codex')
    fireEvent.click(screen.getByRole('button', { name: /^OpenAI$/ }))
    expect(useAgentStore.getState().codexIconVariant).toBe('openai')
  })
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd spa && npx vitest run src/components/settings/TerminalSection.test.tsx -t "codexIconVariant"`
Expected: FAIL — no button with accessible name `Codex` or `OpenAI` is rendered yet.

- [ ] **Step 4: Update the imports in `TerminalSection.tsx`**

At the top of `spa/src/components/settings/TerminalSection.tsx`, change:

```ts
import { useAgentStore, type TabIndicatorStyle, type CcIconVariant } from '../../stores/useAgentStore'
import { CC_ICON_VARIANTS } from '../../lib/agent-icons'
```

to:

```ts
import { useAgentStore, type TabIndicatorStyle, type CcIconVariant, type CodexIconVariant } from '../../stores/useAgentStore'
import { CC_ICON_VARIANTS, CODEX_ICON_VARIANTS } from '../../lib/agent-icons'
```

- [ ] **Step 5: Read `codexIconVariant` and `setCodexIconVariant` from the store**

Find the two existing lines (around 27-28):

```ts
  const ccIconVariant = useAgentStore((s) => s.ccIconVariant)
  const setCcIconVariant = useAgentStore((s) => s.setCcIconVariant)
```

Add directly below:

```ts
  const codexIconVariant = useAgentStore((s) => s.codexIconVariant)
  const setCodexIconVariant = useAgentStore((s) => s.setCodexIconVariant)
```

- [ ] **Step 6: Add the Codex icon options array**

Find the `CC_ICON_OPTIONS` declaration (around lines 46-49) and add directly below:

```ts
  const CODEX_ICON_OPTIONS: { value: CodexIconVariant; label: string }[] = [
    { value: 'openai', label: t('settings.terminal.codex_icon.openai') },
    { value: 'codex', label: t('settings.terminal.codex_icon.codex') },
  ]
```

- [ ] **Step 7: Add the Codex `SettingItem` row in JSX**

Find the closing `</SettingItem>` of the CC icon row (the one that contains `settings.terminal.cc_icon.label`, around line 147) and add directly below it:

```tsx
      <SettingItem label={t('settings.terminal.codex_icon.label')} description={t('settings.terminal.codex_icon.desc')}>
        <div className="flex flex-col gap-2 items-end">
          <div className="flex gap-2">
            {CODEX_ICON_OPTIONS.map((opt) => {
              const VariantIcon = CODEX_ICON_VARIANTS[opt.value]
              const isActive = opt.value === codexIconVariant
              return (
                <button
                  key={opt.value}
                  type="button"
                  onClick={() => { if (!isActive) setCodexIconVariant(opt.value) }}
                  className={`flex items-center gap-2 px-3 py-1.5 rounded-md border text-xs transition-colors cursor-pointer ${
                    isActive
                      ? 'bg-surface-elevated border-border-active text-text-primary'
                      : 'bg-transparent border-border-default text-text-muted hover:text-text-primary hover:border-text-muted'
                  }`}
                  aria-pressed={isActive}
                >
                  <VariantIcon size={16} />
                  <span>{opt.label}</span>
                </button>
              )
            })}
          </div>
          {tabIndicatorStyle === 'dot' && (
            <p className="text-xs text-text-muted text-right max-w-xs">
              {t('settings.terminal.codex_icon.hidden_hint')}
            </p>
          )}
        </div>
      </SettingItem>
```

- [ ] **Step 8: Run the new test**

Run: `cd spa && npx vitest run src/components/settings/TerminalSection.test.tsx -t "codexIconVariant"`
Expected: PASS.

- [ ] **Step 9: Run the full Settings test file**

Run: `cd spa && npx vitest run src/components/settings/TerminalSection.test.tsx`
Expected: all green.

- [ ] **Step 10: Typecheck + lint**

Run: `cd spa && npx tsc --noEmit && pnpm run lint`
Expected: no errors.

- [ ] **Step 11: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-icon-variant
git add spa/src/components/settings/TerminalSection.tsx spa/src/components/settings/TerminalSection.test.tsx
git commit -m "feat(settings): add Codex icon row to Terminal section

Mirror the existing CC icon button group: two buttons (OpenAI /
Codex) with live preview, aria-pressed state, and the same
hidden-hint banner when tabIndicatorStyle is dot-only.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Full test run + final sanity

**Files:** _none modified_

Make sure nothing upstream regressed.

- [ ] **Step 1: Full vitest run**

Run: `cd spa && npx vitest run`
Expected: all green (suite count should be prior total + 4 new tests from Task 2 and + 1 from Task 6 = +5 overall).

- [ ] **Step 2: Lint**

Run: `cd spa && pnpm run lint`
Expected: zero errors, zero warnings.

- [ ] **Step 3: Type check**

Run: `cd spa && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: If any test or check fails, fix it before proceeding. No commit if nothing changed.**

---

## Task 8: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

Per project convention (CLAUDE.md) each PR-worthy change gets a CHANGELOG note.

- [ ] **Step 1: Inspect the top of CHANGELOG.md for the current unreleased / alpha heading**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-icon-variant && head -30 CHANGELOG.md`

Identify the topmost version heading (e.g. `## [Unreleased]` or `## [1.0.0-alpha.189]`). Add the new entry under the appropriate `### Added` (or similar) subsection there. If no `### Added` subsection exists under that heading, create one.

- [ ] **Step 2: Add the entry**

Under the appropriate heading, add:

```markdown
- **spa**: Codex sessions now have an icon-variant choice (`OpenAI` / `Codex`) in Settings → Terminal, mirroring the existing Claude Code `bot`/`star` row. Defaults to `OpenAI` to preserve current behaviour; persisted per user (`useAgentStore.codexIconVariant`, persist version 4 → 5).
```

- [ ] **Step 3: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-icon-variant
git add CHANGELOG.md
git commit -m "docs(changelog): note codex icon variant setting

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Acceptance checklist (final)

- [ ] All vitest files pass — including `agent-icons.test.tsx`, `useTabDisplay.test.ts`, `InlineTab.test.tsx`, `TerminalSection.test.tsx`, `locale-completeness.test.ts`
- [ ] `cd spa && pnpm run lint` clean
- [ ] `cd spa && npx tsc --noEmit` clean
- [ ] Manual check (after `pnpm run build` and launching SPA):
  - Fresh profile: Codex tabs render the Phosphor OpenAI logo (unchanged default)
  - Settings → Terminal → Codex icon: click "Codex" → tab icon changes to monochrome Codex logo live
  - Toggle indicator style to "Dot only": hidden-hint paragraph appears for both CC and Codex rows
  - Toggle back: hints disappear, icons resume
- [ ] `CHANGELOG.md` entry present at the top heading

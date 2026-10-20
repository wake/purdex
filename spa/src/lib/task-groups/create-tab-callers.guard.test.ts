// @vitest-environment node
//
// Guard (task groups spec §5, TG-3b-2): `createTab` is the one place a tab is minted and it puts each tab in the window's fresh set; the fresh tab joins the selected task group only when `addTab` or `detachPane` (the tab store's
// two inserting actions) takes it in. A production caller that mints a tab and hands it anywhere else would leave a fresh tab that never gets placed, or one placed later than its insertion. So the callers are a listed set, each
// handing its tab to the tab store; a new caller has to be added here, and so gets reviewed.
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see pool/fixtures-sync.guard.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readdirSync, readFileSync, statSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { join, relative } from 'node:path'

// @ts-expect-error __dirname is untyped here.
const SRC: string = join(__dirname, '..', '..')

/** Production file → how many times it calls `createTab(`; and what it hands the tab to. */
const CALLERS: Record<string, { calls: number; handsTo: RegExp }> = {
  'lib/notification-click.ts': { calls: 1, handsTo: /\baddTab\(/ },
  'lib/open-browser-tab.ts': { calls: 1, handsTo: /\baddTab\(/ },
  'hooks/useShortcuts.ts': { calls: 1, handsTo: /\baddTab\(/ },
  'features/workspace/hooks.ts': { calls: 2, handsTo: /\baddTab\(/ },
  'components/HistoryPage.tsx': { calls: 1, handsTo: /\baddTab\(/ },
  // the tab store itself: `openSingletonTab` (then `addTab`) and `detachPane` (inserts in its own `set`, after `placeFreshTab`)
  'stores/useTabStore.ts': { calls: 2, handsTo: /\bplaceFreshTab\(/ },
}
/** The definition. */
const DEFINITION = 'types/tab.ts'

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir) as string[]) {
    const full = join(dir, name)
    if (statSync(full).isDirectory()) {
      if (name === 'node_modules') continue
      out.push(...sourceFiles(full))
    } else if (/\.(ts|tsx)$/.test(name) && !/\.test\.|\.test-helpers\./.test(name)) {
      out.push(full)
    }
  }
  return out
}

describe('createTab callers', () => {
  const found: Record<string, number> = {}
  const text: Record<string, string> = {}
  for (const file of sourceFiles(SRC)) {
    const rel = relative(SRC, file)
    const src = readFileSync(file, 'utf8') as string
    const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '') // comments may talk about createTab
    const calls = (code.match(/\bcreateTab\(/g) ?? []).length
    // any mention counts, not only a call: an alias (`createTab as mint`) or a namespace use still has to name `createTab` somewhere, and that file must be in the list
    if (/\bcreateTab\b/.test(code) && rel !== DEFINITION) { found[rel] = calls; text[rel] = src }
  }

  it('the production callers are exactly the listed set, each with its listed number of calls', () => {
    expect(Object.fromEntries(Object.entries(found).sort())).toEqual(Object.fromEntries(Object.entries(CALLERS).map(([f, c]) => [f, c.calls]).sort()))
  })

  it('no file aliases createTab', () => {
    for (const [file, src] of Object.entries(text)) expect(src, file).not.toMatch(/\bcreateTab\s+as\b|\bcreateTab\s*[,}]\s*as\b/)
  })

  it('each caller hands its tab to the tab store (addTab, or detachPane\'s own placement)', () => {
    for (const [file, { handsTo }] of Object.entries(CALLERS)) {
      expect(text[file], file).toMatch(handsTo)
      // a file with several mints hands at least as many tabs on (a store file counts its own two actions separately, below)
      if (file !== 'stores/useTabStore.ts') expect((text[file].match(new RegExp(handsTo.source, 'g')) ?? []).length, file).toBeGreaterThanOrEqual(CALLERS[file].calls)
    }
  })

  it('addTab and detachPane both take a tab out of the fresh set and place it', () => {
    const store = text['stores/useTabStore.ts']
    expect(store).toMatch(/takeFreshTab\(incoming\) \? placeFreshTab\(incoming\)/)
    expect(store).toMatch(/takeFreshTab\(minted\)[\s\S]*placeFreshTab\(minted\)/)
  })
})

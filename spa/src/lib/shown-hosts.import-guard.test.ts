// spa/src/lib/shown-hosts.import-guard.test.ts — the static half of "hidden ≠ absent" and "hiding never closes" (host
// ownership plan H2d-5 T5). A scan of the import declarations of every NON-test source file under `src/`:
//
// 1. `stores/useShownHostsStore` is imported only by the files listed below — the reader module, the host store (its
//    delete cascade), the re-resolve pass, the `settings` builder / applier, the re-show recovery and the local-profiles
//    store (only `sanitizeShownIds`, for a local workbench's own list — per-workbench shown hosts A1). Everything else
//    goes through `lib/shown-hosts` (the one predicate).
// 2. `lib/shown-hosts` is imported only by the H2d-2 / H2d-3 / H2d-4 production files listed below (the Hosts page, the
//    openers and landings, the pane gate, the StatusBar, the per-pane sweeps, the re-show recovery) and the re-resolve
//    pass. The list is the TRUE set on main after H2d-4b, derived from the code (it is wider than the plan's: H2d-3
//    added `HistoryPage.tsx` and `useShortcuts.ts` — reopen-closed lands on the Hosts page — and H2d-4 added
//    `HostHiddenPane.tsx`). A new importer turns this test red: a new consumer of the shown list must be a decision,
//    reviewed against §0.21's "not filtered" table, not an accident.
// 3. None of those files references a tab-closing API (`closeTab` / `closeTabInWorkspace` imported, or called as a
//    member `.closeTab(` / `.closeTabInWorkspace(` — `useTabStore.getState().closeTab`, the workspace store's) that it
//    did not reference at `6858fb1f` (H2d-1 T4, before any reader existed). At that commit only `useShortcuts.ts`
//    imported `closeTab` (its close-tab shortcut); every other file referenced none.
import { describe, expect, it } from 'vitest'

// Every non-test source file, raw. Keys are `/src/…` paths.
const SOURCES = import.meta.glob<string>(['/src/**/*.{ts,tsx}', '!/src/**/*.test.{ts,tsx}', '!/src/**/*.d.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
})

const STORE = 'src/stores/useShownHostsStore'
const READER = 'src/lib/shown-hosts'

/** Importers of `stores/useShownHostsStore` (non-test), verbatim. */
const STORE_IMPORTERS = [
  'src/lib/host-reresolve.ts',
  'src/lib/profile/apply-to-stores.ts',
  'src/lib/profile/collector.ts',
  'src/lib/rebuild/host-reshow.ts',
  'src/lib/shown-hosts.ts',
  'src/stores/useHostStore.ts',
  'src/stores/useLocalProfilesStore.ts',
]

/** Importers of `lib/shown-hosts` (non-test), verbatim. */
const READER_IMPORTERS = [
  'src/components/HandoffConfirmDialog.tsx',
  'src/components/HistoryPage.tsx',
  'src/components/HostHiddenPane.tsx',
  'src/components/NewTabPage.tsx',
  'src/components/PaneLayoutRenderer.tsx',
  'src/components/SessionPickerList.tsx',
  'src/components/StatusBar.tsx',
  'src/components/executions/ExecutionsView.tsx',
  'src/components/hosts/HostSidebar.tsx',
  'src/components/hosts/OverviewSection.tsx',
  'src/components/hosts/SessionsSection.tsx',
  'src/components/hosts/nex/NexExecutionsTable.tsx',
  'src/hooks/useNotificationDispatcher.ts',
  'src/hooks/useRouteSync.ts',
  'src/hooks/useShortcuts.ts',
  'src/lib/deeplink/deeplinkResolver.ts',
  'src/lib/host-reresolve.ts',
  'src/lib/nex/handoff.ts',
  'src/lib/rebuild/cwd-probe.ts',
  'src/lib/rebuild/host-reshow.ts',
  'src/lib/rebuild/reconcile-host.ts',
  'src/lib/rebuild/revive.ts',
]

/** Tab-closing references per file at `6858fb1f` (files absent here referenced none, or did not exist). */
const CLOSERS_AT_BASE: Record<string, string[]> = {
  'src/hooks/useShortcuts.ts': ['import closeTab'],
}
const CLOSING_NAMES = new Set(['closeTab', 'closeTabInWorkspace'])

/** Block comments and whole-line `//` comments removed — a comment naming a module or an API is not a use. */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

interface ImportDecl { specifier: string; names: string[] }

function importsOf(src: string): ImportDecl[] {
  const code = stripComments(src)
  const out: ImportDecl[] = []
  const namesOf = (clause: string) => clause.replace(/^type\s+/, '').replace(/[{}*]/g, ' ').split(',')
    .map((n) => n.trim().replace(/^type\s+/, '').split(/\s+as\s+/)[0].trim()).filter(Boolean)
  for (const m of code.matchAll(/\bimport\s+([^;'"]*?)\s*from\s*['"]([^'"]+)['"]/g)) out.push({ specifier: m[2], names: namesOf(m[1]) })
  for (const m of code.matchAll(/\bexport\s+(?:type\s+)?(\*(?:\s+as\s+\w+)?|\{[^}]*\})\s*from\s*['"]([^'"]+)['"]/g)) out.push({ specifier: m[2], names: namesOf(m[1]) })
  for (const m of code.matchAll(/\bimport\s*['"]([^'"]+)['"]/g)) out.push({ specifier: m[1], names: [] })
  for (const m of code.matchAll(/\bimport\(\s*['"]([^'"]+)['"]\s*\)/g)) out.push({ specifier: m[1], names: [] })
  return out
}

/** `/src/a/b.ts` + `../c/d` → `src/a/c/d` (extension-less); a bare package specifier → null. */
function resolve(fromKey: string, specifier: string): string | null {
  if (!specifier.startsWith('.')) return null
  const parts = fromKey.replace(/^\//, '').split('/').slice(0, -1)
  for (const seg of specifier.split('/')) {
    if (seg === '.' || seg === '') continue
    if (seg === '..') parts.pop()
    else parts.push(seg)
  }
  return parts.join('/').replace(/\.(tsx?|jsx?)$/, '').replace(/\/index$/, '')
}

function importersOf(target: string): string[] {
  const found: string[] = []
  for (const [key, src] of Object.entries(SOURCES)) {
    if (importsOf(src).some((d) => resolve(key, d.specifier) === target)) found.push(key.replace(/^\//, ''))
  }
  return found.sort()
}

function closersOf(file: string): string[] {
  const src = SOURCES[`/${file}`]
  const refs = new Set<string>()
  for (const d of importsOf(src)) for (const n of d.names) if (CLOSING_NAMES.has(n)) refs.add(`import ${n}`)
  for (const m of stripComments(src).matchAll(/\.(closeTab|closeTabInWorkspace)\s*\(/g)) refs.add(`call .${m[1]}`)
  return [...refs].sort()
}

describe('shown hosts — import guard (H2d-5 T5)', () => {
  it('the scan sees the source tree (premise)', () => {
    const keys = Object.keys(SOURCES)
    expect(keys.length).toBeGreaterThan(300)
    expect(keys).toContain(`/${READER}.ts`)
    expect(keys).toContain(`/${STORE}.ts`)
    expect(keys.some((k) => k.includes('.test.'))).toBe(false)
    // the parser handles the forms in use: a multi-line clause, a type-only import, a dynamic import
    expect(importsOf("import {\n  a,\n  type B as C,\n} from './x'\nimport type { D } from \"../y\"\nconst e = await import('./z')"))
      .toEqual([{ specifier: './x', names: ['a', 'B'] }, { specifier: '../y', names: ['D'] }, { specifier: './z', names: [] }])
  })

  it('`stores/useShownHostsStore` is imported only by the listed files', () => {
    expect(importersOf(STORE)).toEqual(STORE_IMPORTERS)
  })

  it('`lib/shown-hosts` is imported only by the listed files', () => {
    expect(importersOf(READER)).toEqual(READER_IMPORTERS)
  })

  it('no importer references a tab-closing API it did not reference at 6858fb1f', () => {
    const files = [...new Set([...STORE_IMPORTERS, ...READER_IMPORTERS])].sort()
    const now = Object.fromEntries(files.map((f) => [f, closersOf(f)]).filter(([, refs]) => refs.length > 0))
    expect(now).toEqual(CLOSERS_AT_BASE)
  })
})

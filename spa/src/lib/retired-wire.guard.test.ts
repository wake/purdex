// @vitest-environment node
//
// Guard (monitoring tabs plan, MON-12; spec A11 / W9): the wire the daemon is retiring (`fleet.v1`, `gate.decisions.v1`, the watch events routes) is no longer read by the SPA. Each MON-12 PR adds what it deletes to the three lists below:
// the files that must stay gone, the routes / capabilities / event names no code may mention, and the identifiers no code may use. Comments (not strings) are stripped before matching, so a note that says why something is gone does not turn this red.
// Known limit (88's ruling, MON-12a-3): this is a text match against someone putting the old wire back by accident, not against someone building it from pieces on purpose (`'watch' + '.events'`, a route held in a variable and joined later). The common splits are caught; the rest is for review.
import { describe, it, expect } from 'vitest'
import ts from 'typescript'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see pool/fixtures-sync.guard.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { dirname, join, relative } from 'node:path'

// @ts-expect-error __dirname is untyped here.
const SRC: string = join(__dirname, '..')
// @ts-expect-error __filename is untyped here.
const SELF: string = relative(SRC, __filename)

/** Files that must not come back. */
const RETIRED_FILES = [
  'components/watch/QueueSection.tsx',
  'components/watch/QueueNow.tsx',
  'components/watch/QueueLane.tsx',
  'components/watch/QueueHistory.tsx',
  'lib/watch/history-read.ts',
  'lib/watch/history-view.ts',
  'lib/watch/decisions-types.ts',
  'lib/watch/events-store.ts',
  'lib/watch/events-view.ts',
  'lib/watch/events-types.ts',
  'lib/fleet/fleet-support.ts',
  'lib/fleet/fleet-ws.ts',
  'lib/pool/pool-look-migration.ts',
  // The Hosts page's probe-host editor, the pool host page and their rows (MON-9b-2).
  'components/hosts/ProbeHostPage.tsx', 'components/hosts/PoolHostPage.tsx', 'components/hosts/ProbeHostEditor.tsx', 'components/hosts/ProbeHostForm.tsx', 'components/hosts/ProbeCheck.tsx',
  'components/hosts/probe-rows.tsx', 'components/hosts/use-probe-rows.ts', 'components/hosts/useProbeEditor.ts', 'components/hosts/PoolStatusDot.tsx', 'components/hosts/pool-status-word.ts',
  'lib/pool/use-pool-host-rows.ts', 'lib/pool/fleet-bridge.ts',
  // The fleet's settings half and its reads (MON-12b-2): host-config's probe-hosts and fleet rows, the ssh aliases, the probe check, the fleet read and its re-read.
  'lib/fleet/config-api.ts', 'lib/fleet/config-draft.ts', 'lib/fleet/config-store.ts', 'lib/fleet/config-validate.ts', 'lib/fleet/probe-check.ts',
  'lib/fleet/use-fleet-daemons.ts', 'lib/fleet/use-fleet-host-views.ts', 'lib/fleet/reread.ts', 'lib/fleet/api.ts',
]

/** Routes, capabilities and event names no non-test source may contain (matched as plain text, after comments are stripped). */
const RETIRED_CODE = [
  '/api/gate/',
  'gate.decisions.v1',
  // The daemon's `watch.events` push, as a whole string: the locale keys (`watch.events.level.push`) and the pane kind `watch-events` are not it.
  "'watch.events'", '"watch.events"', '`watch.events`',
]

/**
 * The fleet's wire (MON-12): its routes, its capability, its event, the host-config rows it wrote and the opt-in that asked for it. A non-test source naming one of these is the wire coming back.
 */
const RETIRED_FLEET_CODE = [
  '/api/fleet', 'fleet.v1', 'fleet.changed', 'hostconfig/probe-hosts', 'hostconfig/fleet',
  "searchParams.set('fleet'", 'searchParams.set("fleet"', 'searchParams.set(`fleet`',
]

/**
 * Every way into the network that `lib/fleet` has (`hostFetch` is called from `api.ts`, `config-api.ts`, `probe-check.ts`; `grep -n hostFetch spa/src/lib/fleet/*.ts` again when this list is changed), and what MON-12b-1 deleted.
 * No non-test source may use them.
 */
const RETIRED_FLEET_NAMES = [
  'fetchFleet', 'readFleetOf', 'readAllFleets', 'fetchFleetConfig', 'fetchSshAliases', 'putProbeHosts', 'putFleetRow', 'runProbeCheck', 'useFleetConfigStore',
  'startFleetSupport', 'handleFleetEvent', 'bootPoolLooks',
]

/** Identifiers no non-test source may use. */
const RETIRED_NAMES = [
  'fetchGateDecisions',
  'selectGateDecisionsV1',
  'useGateHistory',
  'parseGateDecisions',
  'startWatchEvents',
  'useWatchEventsStore',
  'fetchWatchEvents',
  'fetchWatchUnhandled',
  'WATCH_EVENTS_EVENT',
]

/** Blanks the comments (found by the TypeScript parser, so a `//` inside a string or template literal is left alone) and keeps everything else, including the line breaks. */
export function stripComments(text: string): string {
  const sf = ts.createSourceFile('x.tsx', text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const ranges: Array<[number, number]> = []
  const jsxText: Array<[number, number]> = []
  const visit = (n: ts.Node): void => {
    // The parser asks for trivia next to JSX text too, where `/* x */` is rendered text, not a comment.
    if (n.kind === ts.SyntaxKind.JsxText) jsxText.push([n.getFullStart(), n.getEnd()])
    for (const r of ts.getLeadingCommentRanges(text, n.getFullStart()) ?? []) ranges.push([r.pos, r.end])
    for (const r of ts.getTrailingCommentRanges(text, n.getEnd()) ?? []) ranges.push([r.pos, r.end])
    // getChildren (not forEachChild) also yields the punctuation tokens, so a comment inside an empty `{ }`, `[ ]` or `( )` is found too.
    for (const c of n.getChildren(sf)) visit(c)
  }
  visit(sf)
  let out = text
  for (const [pos, end] of ranges.filter(([p, e]) => !jsxText.some(([s, f]) => p >= s && e <= f))) out = out.slice(0, pos) + out.slice(pos, end).replace(/[^\n]/g, ' ') + out.slice(end)
  return out
}

export function hitsIn(text: string): string[] {
  // Parsing is the slow part: a file that does not contain a retired term anywhere, comments included, has nothing to find.
  if (![...RETIRED_CODE, ...RETIRED_FLEET_CODE, ...RETIRED_NAMES, ...RETIRED_FLEET_NAMES].some((s) => text.includes(s)) && !/['"`]watch\.['"`]\s*\+/.test(text)) return []
  const code = stripComments(text)
  const hits: string[] = []
  for (const s of [...RETIRED_CODE, ...RETIRED_FLEET_CODE]) if (code.includes(s)) hits.push(s)
  for (const n of [...RETIRED_NAMES, ...RETIRED_FLEET_NAMES]) if (new RegExp(`\\b${n}\\b`).test(code)) hits.push(n)
  // The event name put together from pieces ('watch.' + 'events'). A guard against drift, not against someone set on getting round it.
  if (/['"`]watch\.['"`]\s*\+/.test(code)) hits.push("'watch.' + …")
  return hits
}

/** The names of what read the fleet store and its views. Looked for in every file under src, tests included (comments stripped). */
const FLEET_READERS = /\b(useFleetStore|useFleetHostViews|readFleetView|useFleetConfigStore)\b/

/**
 * The relative module specifiers of `file` that resolve into `lib/fleet`: any relative string literal (`from`, `import()`, `require`, `vi.mock`, `vi.importActual`…), resolved against the file's own directory,
 * so `../fleet/QueueStrip` from `components/watch/` (the sidebar's `components/fleet`) is not mistaken for `lib/fleet`. Comments are stripped first.
 */
export function fleetImportsIn(file: string, text: string, src: string): string[] {
  if (!text.includes('fleet')) return []
  const root = join(src, 'lib', 'fleet')
  const out: string[] = []
  for (const m of stripComments(text).matchAll(/['"`](\.{1,2}\/[^'"`]*)['"`]/g)) {
    const target = join(dirname(file), m[1])
    if (target === root || target.startsWith(root + '/')) out.push(m[1])
  }
  return out
}

/** The watch routes the SPA may still call (W9): the status, the wake, and the guardian's start key. */
const WATCH_ROUTES_KEPT = ['status', 'wake', 'start']

/** The `/api/watch/<x>` routes named in code (comments stripped) that are not one of the kept three. */
export function retiredWatchRoutesIn(text: string): string[] {
  if (!text.includes('/api/watch/')) return []
  const out: string[] = []
  for (const m of stripComments(text).matchAll(/\/api\/watch\/([A-Za-z0-9_-]*)(\$\{|['"`]\s*\+)?/g)) {
    // A route that is built (`/api/watch/${x}`, '/api/watch/' + x) cannot be told from a retired one: not allowed.
    if (m[2] !== undefined) out.push(`/api/watch/${m[1]}<built>`)
    else if (!WATCH_ROUTES_KEPT.includes(m[1])) out.push(`/api/watch/${m[1]}`)
  }
  return out
}

function sources(dir: string, withTests = false, out: string[] = []): string[] {
  for (const name of readdirSync(dir) as string[]) {
    if (name === 'node_modules') continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) sources(full, withTests, out)
    else if (/\.(ts|tsx)$/.test(name) && (withTests || !/\.test\.(ts|tsx)$/.test(name))) out.push(full)
  }
  return out
}

describe('the retired wire stays retired', () => {
  it('the retired files are not there', () => {
    for (const f of RETIRED_FILES) expect(existsSync(join(SRC, f)), f).toBe(false)
  })

  it('lib/fleet is not there at all', () => {
    expect(existsSync(join(SRC, 'lib', 'fleet'))).toBe(false)
  })

  // The whole of src, tests too: a test that imports or names what is gone would be the first step of bringing it back.
  it('nothing under src imports lib/fleet or names a reader of the fleet store', () => {
    const hits: string[] = []
    for (const file of sources(SRC, true)) {
      const rel = relative(SRC, file)
      if (rel === SELF) continue
      const text = readFileSync(file, 'utf8')
      for (const spec of fleetImportsIn(file, text, SRC)) hits.push(`${rel}: imports ${spec}`)
      if (FLEET_READERS.test(stripComments(text))) hits.push(`${rel}: names a fleet reader`)
    }
    expect(hits).toEqual([])
  }, 60_000)

  it('no source file names a retired route, capability or identifier', () => {
    const hits: string[] = []
    for (const file of sources(SRC)) {
      const rel = relative(SRC, file)
      if (rel === SELF) continue
      for (const h of hitsIn(readFileSync(file, 'utf8'))) hits.push(`${rel}: ${h}`)
    }
    expect(hits).toEqual([])
  }, 30_000)

  it('the only /api/watch/ routes in code are the status, the wake and the start', () => {
    const hits: string[] = []
    for (const file of sources(SRC)) {
      const rel = relative(SRC, file)
      if (rel === SELF) continue
      for (const h of retiredWatchRoutesIn(readFileSync(file, 'utf8'))) hits.push(`${rel}: ${h}`)
    }
    expect(hits).toEqual([])
  }, 30_000)

  it('the detector catches what it is for', () => {
    expect(hitsIn("const t = 'watch.events'")).toEqual(["'watch.events'"])
    expect(hitsIn('type X = "watch.events" | "a"')).toEqual(['"watch.events"'])
    // the locale keys, the pane kind and a comment are not the event
    expect(hitsIn("t('watch.events.level.push'); const k = 'watch-events'; // 'watch.events'")).toEqual([])
    expect(retiredWatchRoutesIn("hostFetch(id, '/api/watch/events?limit=500')")).toEqual(['/api/watch/events'])
    expect(retiredWatchRoutesIn("hostFetch(id, '/api/watch/unhandled')")).toEqual(['/api/watch/unhandled'])
    expect(retiredWatchRoutesIn("hostFetch(id, '/api/watch/status'); post('/api/watch/wake'); post('/api/watch/start')")).toEqual([])
    expect(retiredWatchRoutesIn("// GET /api/watch/events\nconst a = 1")).toEqual([])
    // built from pieces: not told apart from a retired one, so not allowed (codex attack)
    expect(retiredWatchRoutesIn("const p = '/api/watch/' + kind")).toEqual(['/api/watch/<built>'])
    expect(retiredWatchRoutesIn('const p = `/api/watch/${kind}`')).toEqual(['/api/watch/<built>'])
    // the fleet's wire and the names that reach it
    expect(hitsIn("hostFetch(id, '/api/fleet')")).toEqual(['/api/fleet'])
    expect(hitsIn("hostFetch(id, '/api/fleet/ssh-aliases')")).toEqual(['/api/fleet'])
    expect(hitsIn("caps.includes('fleet.v1')")).toEqual(['fleet.v1'])
    expect(hitsIn("if (event.type === 'fleet.changed') return")).toEqual(['fleet.changed'])
    expect(hitsIn("put(id, 'hostconfig/probe-hosts')")).toEqual(['hostconfig/probe-hosts'])
    expect(hitsIn("u.searchParams.set('fleet', 'v1')")).toEqual(["searchParams.set('fleet'"])
    expect(hitsIn("u.searchParams.set('agent', 'v2')")).toEqual([])
    expect(hitsIn('readAllFleets(); void startFleetSupport()')).toEqual(['readAllFleets', 'startFleetSupport'])
    // imports are judged by where the specifier lands, not by how it is spelled
    const f = (file: string, text: string) => fleetImportsIn(join(SRC, file), text, SRC)
    expect(f('lib/pool/x.ts', "import type { FleetFast } from '../fleet/types'")).toEqual(['../fleet/types'])
    expect(f('components/fleet/A.tsx', "import { x } from '../../lib/fleet/store'")).toEqual(['../../lib/fleet/store'])
    expect(f('lib/pool/x.ts', "const m = await import('../fleet')")).toEqual(['../fleet'])
    expect(f('lib/pool/x.ts', "import '../fleet/store'")).toEqual(['../fleet/store'])
    expect(f('lib/pool/x.test.ts', "vi.mock('../fleet/reread', () => ({}))")).toEqual(['../fleet/reread'])
    expect(f('lib/pool/x.ts', "export { x } from '../fleet/store'")).toEqual(['../fleet/store'])
    // `../fleet/…` from a component is the sidebar's components/fleet, and a name that only looks alike is not a path into lib/fleet
    expect(f('components/watch/QueueFlow.tsx', "import { QueueSegments } from '../fleet/QueueStrip'")).toEqual([])
    expect(f('components/watch/host/LookSettings.tsx', "import { HostCell } from '../../fleet/HostCell'")).toEqual([])
    expect(f('lib/pool/x.ts', "import { x } from './fleet-bridge'; import { y } from '../fleetish/z'")).toEqual([])
    expect(f('lib/pool/x.ts', "// import { x } from '../fleet/store'\nconst a = 1")).toEqual([])
    expect(FLEET_READERS.test('const s = useFleetStore.getState()')).toBe(true)
    expect(FLEET_READERS.test('const s = useFleetStoreLater')).toBe(false)
    expect(hitsIn('// the fleet.v1 capability and fetchFleet are gone\nconst a = 1')).toEqual([])
    expect(hitsIn('const readAllFleetsLater = 1')).toEqual([])
    expect(hitsIn("const type = 'watch.' + 'events'")).toEqual(["'watch.' + …"])
    expect(hitsIn("const k = 'watch.events.level.' + level")).toEqual([])
    expect(hitsIn("hostFetch(id, `/api/gate/decisions?x`)")).toEqual(['/api/gate/'])
    expect(hitsIn("caps.includes('gate.decisions.v1')")).toEqual(['gate.decisions.v1'])
    expect(hitsIn('export const selectGateDecisionsV1 = () => true')).toEqual(['selectGateDecisionsV1'])
    // a comment that says why it is gone, and a different identifier that merely contains the words
    expect(hitsIn('// was GET /api/gate/decisions (gate.decisions.v1), fetchGateDecisions')).toEqual([])
    expect(hitsIn('/* selectGateDecisionsV1 */ const a = 1')).toEqual([])
    expect(hitsIn('const fetchGateDecisionsLater = 1')).toEqual([])
    // a `//` inside a string is not a comment: the code after it on the same line still counts
    expect(hitsIn("const u = 'https://h/api/gate/decisions'")).toEqual(['/api/gate/'])
    expect(hitsIn("const base = 'https://host'; fetchGateDecisions(base)")).toEqual(['fetchGateDecisions'])
    expect(hitsIn('const u = `https://h/${x}`; // selectGateDecisionsV1')).toEqual([])
    expect(hitsIn("const x = `${`https://host`}`; fetchGateDecisions()")).toEqual(['fetchGateDecisions'])
    expect(hitsIn("const re = /'/; const u = 'https://h'; fetchGateDecisions()")).toEqual(['fetchGateDecisions'])
    expect(hitsIn('const a = <div>https://h</div>; fetchGateDecisions()')).toEqual(['fetchGateDecisions'])
    // `/* … */` in JSX text is rendered text, but `{/* … */}` is a comment
    expect(hitsIn('const a = <div>/* fetchGateDecisions */</div>')).toEqual(['fetchGateDecisions'])
    expect(hitsIn('const a = <div>{/* fetchGateDecisions */}</div>')).toEqual([])
    // a comment alone in an empty container
    expect(hitsIn('const x = { /* fetchGateDecisions */ }')).toEqual([])
    expect(hitsIn('function f() { // fetchGateDecisions\n}')).toEqual([])
    expect(hitsIn('const a = [/* selectGateDecisionsV1 */]; f(/* parseGateDecisions */)')).toEqual([])
  })
})

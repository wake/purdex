// @vitest-environment node
//
// Guard (monitoring tabs plan, MON-12; spec A11 / W9): the wire the daemon is retiring (`fleet.v1`, `gate.decisions.v1`, the watch events routes) is no longer read by the SPA. Each MON-12 PR adds what it deletes to the three lists below:
// the files that must stay gone, the routes / capabilities / event names no code may mention, and the identifiers no code may use. Comments are stripped before matching, so a note that says why something is gone does not turn this red.
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see pool/fixtures-sync.guard.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { join, relative } from 'node:path'

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
]

/** Routes, capabilities and event names no non-test source may contain (matched as plain text, after comments are stripped). */
const RETIRED_CODE = [
  '/api/gate/',
  'gate.decisions.v1',
]

/** Identifiers no non-test source may use. */
const RETIRED_NAMES = [
  'fetchGateDecisions',
  'selectGateDecisionsV1',
  'useGateHistory',
  'parseGateDecisions',
]

/** Drops `/* … *\/` and `// …` (a `//` inside a string is cut too: that can only miss a hit, never invent one). */
export function stripComments(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '')
}

export function hitsIn(text: string): string[] {
  const code = stripComments(text)
  const hits: string[] = []
  for (const s of RETIRED_CODE) if (code.includes(s)) hits.push(s)
  for (const n of RETIRED_NAMES) if (new RegExp(`\\b${n}\\b`).test(code)) hits.push(n)
  return hits
}

function sources(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir) as string[]) {
    if (name === 'node_modules') continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) sources(full, out)
    else if (/\.(ts|tsx)$/.test(name) && !/\.test\.(ts|tsx)$/.test(name)) out.push(full)
  }
  return out
}

describe('the retired wire stays retired', () => {
  it('the retired files are not there', () => {
    for (const f of RETIRED_FILES) expect(existsSync(join(SRC, f)), f).toBe(false)
  })

  it('no source file names a retired route, capability or identifier', () => {
    const hits: string[] = []
    for (const file of sources(SRC)) {
      const rel = relative(SRC, file)
      if (rel === SELF) continue
      for (const h of hitsIn(readFileSync(file, 'utf8'))) hits.push(`${rel}: ${h}`)
    }
    expect(hits).toEqual([])
  })

  it('the detector catches what it is for', () => {
    expect(hitsIn("hostFetch(id, `/api/gate/decisions?x`)")).toEqual(['/api/gate/'])
    expect(hitsIn("caps.includes('gate.decisions.v1')")).toEqual(['gate.decisions.v1'])
    expect(hitsIn('export const selectGateDecisionsV1 = () => true')).toEqual(['selectGateDecisionsV1'])
    // a comment that says why it is gone, and a different identifier that merely contains the words
    expect(hitsIn('// was GET /api/gate/decisions (gate.decisions.v1), fetchGateDecisions')).toEqual([])
    expect(hitsIn('/* selectGateDecisionsV1 */ const a = 1')).toEqual([])
    expect(hitsIn('const fetchGateDecisionsLater = 1')).toEqual([])
  })
})

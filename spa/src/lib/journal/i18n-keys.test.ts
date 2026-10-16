// Every `journal.*` text key the journal's sources use exists in BOTH locale files (J-2c review). `locale-completeness` only
// compares en with zh-TW, so a key missing from both (a row showing the raw key name) slips through; this reads the sources.
import { describe, it, expect } from 'vitest'
import { ENTRY_KINDS, TASK_STATES } from './types'
import { FILTER_IDS } from './filters'
import en from '../../locales/en.json'
import zh from '../../locales/zh-TW.json'

// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see kind-tokens.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync, readdirSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
// @ts-expect-error __dirname is untyped here.
const SRC: string = resolve(__dirname, '../..')

/** Non-test sources under the journal's three homes: components/journal, lib/journal, stores/useJournalStore*. */
function sources(): string[] {
  const files: string[] = []
  const walk = (dir: string) => {
    for (const f of readdirSync(dir, { withFileTypes: true }) as { name: string; isDirectory(): boolean }[]) {
      const p = resolve(dir, f.name)
      if (f.isDirectory()) { if (f.name !== '__tests__') walk(p) } else files.push(p)
    }
  }
  walk(resolve(SRC, 'components/journal'))
  walk(resolve(SRC, 'lib/journal'))
  for (const f of readdirSync(resolve(SRC, 'stores')) as string[]) if (/^useJournalStore.*\.ts$/.test(f)) files.push(resolve(SRC, 'stores', f))
  return files.filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f) && !/fixtures?\.ts$|-fixture\.ts$/.test(f))
}

// `journal.changed` / `journal.v1` are the daemon's event and capability names, not text keys.
const NOT_TEXT = new Set(['journal.changed', 'journal.v1'])
const DYNAMIC: Record<string, readonly string[]> = { 'journal.kind.': ENTRY_KINDS, 'journal.filter.': FILTER_IDS, 'journal.tasks.state.': TASK_STATES }

/** Every key literal ('journal.x.y' or `journal.x.${id}`, which expands over the ids that fill it). */
function usedKeys(): { key: string; file: string }[] {
  const out: { key: string; file: string }[] = []
  for (const file of sources()) {
    const code: string = readFileSync(file, 'utf8')
    for (const m of code.matchAll(/(['"`])(journal\.[\w.]*(?:\$\{[^}]*\}[\w.]*)?)\1/g)) {
      const lit = m[2]
      if (NOT_TEXT.has(lit)) continue
      const dyn = lit.match(/^(journal\.[\w.]*)\$\{[^}]*\}$/)
      if (!dyn) { out.push({ key: lit, file }); continue }
      const ids = DYNAMIC[dyn[1]]
      if (!ids) { out.push({ key: `${lit} (unknown dynamic key: teach DYNAMIC about it)`, file }); continue }
      for (const id of ids) out.push({ key: dyn[1] + id, file })
    }
  }
  return out
}

describe('journal i18n keys', () => {
  const keys = usedKeys()
  it('finds the keys it is meant to check (not an empty scan)', () => {
    const names = keys.map((k) => k.key)
    expect(names).toContain('journal.band.answered')
    expect(names).toContain('journal.kind.confirm')
    expect(names).toContain('journal.stream.newEvents')
    expect(names).toContain('journal.pin.failed')
  })
  it.each([['en', en], ['zh-TW', zh]] as const)('%s has every key the journal sources use', (_name, locale) => {
    const dict = locale as Record<string, string>
    const missing = keys.filter((k) => typeof dict[k.key] !== 'string').map((k) => `${k.key}  (${k.file.slice(SRC.length + 1)})`)
    expect(missing).toEqual([])
  })
})

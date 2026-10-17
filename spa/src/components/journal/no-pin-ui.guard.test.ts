// Guard (JV-3, spec U7 / §3.5): entry pinning is gone from the SPA. Nothing may call the pin route, no journal source may keep a pin
// action, and no journal text may say 釘選. (`pinned` on the wire is still parsed, for the old-daemon band.)
import { describe, it, expect } from 'vitest'
import en from '../../locales/en.json'
import zh from '../../locales/zh-TW.json'
// @ts-expect-error node:fs is untyped under tsconfig.app.json; it works at runtime.
import { readFileSync, readdirSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
// @ts-expect-error __dirname is untyped here.
const SRC: string = resolve(__dirname, '../..')

function sources(): string[] {
  const out: string[] = []
  const walk = (dir: string) => {
    for (const f of readdirSync(dir, { withFileTypes: true }) as { name: string; isDirectory(): boolean }[]) {
      const p = resolve(dir, f.name)
      if (f.isDirectory()) { if (f.name !== 'node_modules') walk(p) } else if (/\.(ts|tsx)$/.test(f.name) && !/\.test\.tsx?$/.test(f.name)) out.push(p)
    }
  }
  walk(SRC)
  return out
}

describe('no pin UI left in the journal', () => {
  // Built from parts so that this file does not match its own search.
  const route = ['/api/journal', 'pin'].join('/')
  it('no source calls the pin route', () => {
    const hits = sources().filter((f) => (readFileSync(f, 'utf8') as string).includes(route))
    expect(hits).toEqual([])
  })
  it('the journal sources have no pin action (setPin / setPinned / onTogglePin / journal-pin-toggle)', () => {
    const re = /\b(setPin|setPinned|onTogglePin)\b|journal-pin-toggle/
    const hits = sources().filter((f) => /\/(components\/journal|lib\/journal|stores\/useJournalStore)/.test(f) && re.test(readFileSync(f, 'utf8') as string))
    expect(hits).toEqual([])
  })
  it.each([['zh-TW', zh], ['en', en]])('no journal.* text in %s mentions pinning', (_n, dict) => {
    const bad = Object.entries(dict as Record<string, string>).filter(([k, v]) => k.startsWith('journal.') && /釘選|\bpin(ned)?\b/i.test(v))
    expect(bad).toEqual([])
  })
})

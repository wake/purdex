// The journal's green is the text-strength token (J-2c review of the light screenshots). `status-success` is a pale background tint in the light
// theme (#d4edda): as a text, icon or dot colour it all but disappears, so the journal uses `status-success-text` (>= 4.5:1 on the card).
import { describe, it, expect } from 'vitest'

// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see kind-tokens.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync, readdirSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
// @ts-expect-error __dirname is untyped here.
const SRC: string = resolve(__dirname, '../..')

function sources(dir: string): string[] {
  const files: string[] = []
  for (const f of readdirSync(dir, { withFileTypes: true }) as { name: string; isDirectory(): boolean }[]) {
    const p = resolve(dir, f.name)
    if (f.isDirectory()) files.push(...sources(p))
    else if (/\.tsx?$/.test(f.name) && !/\.test\.tsx?$/.test(f.name)) files.push(p)
  }
  return files
}

describe('the journal draws green with the text-strength token', () => {
  it('no journal source uses the plain status-success as a text, icon or dot colour', () => {
    const offenders: string[] = []
    for (const file of [...sources(resolve(SRC, 'components/journal')), ...sources(resolve(SRC, 'lib/journal'))]) {
      const text = readFileSync(file, 'utf8') as string
      for (const m of text.matchAll(/\b(?:text|bg|border)-status-success(?!-text)\b/g)) offenders.push(`${file.slice(SRC.length + 1)}: ${m[0]}`)
    }
    expect(offenders).toEqual([])
  })
})

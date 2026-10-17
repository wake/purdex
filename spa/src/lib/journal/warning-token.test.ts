// The journal draws its warning-coloured text with `journal-warning-text`, not `status-warning-text`. Nord's `status-warning-text` is the plain
// amber everywhere else in the app (watch alerts, dialogs, settings), where it must keep reading as a warning; the journal's chip and highlighted
// name need the lighter value (>= 4.5:1 on the chip), which only `--journal-warning-text` (themes.css, Nord) gives them.
import { describe, it, expect } from 'vitest'

// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see kind-tokens.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync, readdirSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
// @ts-expect-error __dirname is untyped here.
const SRC: string = resolve(__dirname, '../..')

function sources(dir: string, pattern: RegExp): string[] {
  const files: string[] = []
  for (const f of readdirSync(dir, { withFileTypes: true }) as { name: string; isDirectory(): boolean }[]) {
    const p = resolve(dir, f.name)
    if (f.isDirectory()) files.push(...sources(p, pattern))
    else if (pattern.test(f.name) && !/\.test\.tsx?$/.test(f.name)) files.push(p)
  }
  return files
}

const rel = (file: string): string => file.slice(SRC.length + 1)
const isJournal = (file: string): boolean => /^(?:components|lib)\/journal\//.test(rel(file))

describe('the journal draws warning text with its own token', () => {
  it('no journal source uses status-warning-text (as a text, background, border, ring, fill, stroke, divide or outline class, or as a var)', () => {
    const offenders: string[] = []
    const re = /\b(?:(?:text|bg|border|ring|fill|stroke|divide|outline)-status-warning-text|var\(--status-warning-text)/g
    for (const file of [...sources(resolve(SRC, 'components/journal'), /\.tsx?$/), ...sources(resolve(SRC, 'lib/journal'), /\.tsx?$/)]) {
      const text = readFileSync(file, 'utf8') as string
      for (const m of text.matchAll(re)) offenders.push(`${rel(file)}: ${m[0]}`)
    }
    expect(offenders).toEqual([])
  })

  it('themes.css maps the Tailwind colour to the variable with the status-warning-text fallback, and only Nord sets the variable', () => {
    const css = readFileSync(resolve(SRC, 'styles/themes.css'), 'utf8') as string
    expect(css).toContain('--color-journal-warning-text: var(--journal-warning-text, var(--status-warning-text, var(--status-warning)));')
    const setIn = [...css.matchAll(/\[data-theme="([\w-]+)"\]\s*\{([^}]*)\}/g)].filter((m) => /--journal-warning-text:/.test(m[2])).map((m) => m[1])
    expect(setIn).toEqual(['nord'])
  })

  it('journal-warning-text stays inside the journal (themes.css defines it)', () => {
    const offenders: string[] = []
    for (const file of sources(SRC, /\.(?:tsx?|css)$/)) {
      if (isJournal(file) || rel(file) === 'styles/themes.css') continue
      if ((readFileSync(file, 'utf8') as string).includes('journal-warning-text')) offenders.push(rel(file))
    }
    expect(offenders).toEqual([])
  })
})

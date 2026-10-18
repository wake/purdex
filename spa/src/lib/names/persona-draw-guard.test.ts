// DiceBear is large (core 461 KB, plus up to 373 KB per style). It must stay out of the main chunk, which means no
// static `import ... from '@dicebear/...'` anywhere in the SPA: only `import('@dicebear/...')` inside persona-draw.ts
// (plan P3). A static import anywhere would pull it into whichever chunk imports that file.
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) return sourceFiles(p)
    return /\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name) ? [p] : []
  })
}

describe('DiceBear stays in the lazy chunk', () => {
  const root = join(__dirname, '..', '..')
  const files = sourceFiles(root)

  it('is never imported statically', () => {
    const offenders = files.filter((f) => /^\s*(import|export)\s[^'"\n]*from\s+['"]@dicebear\//m.test(readFileSync(f, 'utf8')))
    expect(offenders).toEqual([])
  })

  it('is imported dynamically only by persona-draw.ts', () => {
    const users = files
      .filter((f) => /import\(\s*['"]@dicebear\//.test(readFileSync(f, 'utf8')))
      .map((f) => f.slice(root.length + 1))
    expect(users).toEqual(['lib/names/persona-draw.ts'])
  })

  it('and persona-draw.ts is imported by no file that the main chunk loads', () => {
    // Only the persona page (NA-1b-2b) may import it at runtime; until it exists, nobody does. A type-only import is
    // erased by the compiler and carries nothing, so it does not count (persona-credits.ts uses one).
    const runtimeImport = /^\s*(import|export)\s+(?!type\b)[^'"\n]*from\s+['"][^'"]*persona-draw['"]|import\(\s*['"][^'"]*persona-draw['"]/m
    const importers = files
      .filter((f) => !f.endsWith('persona-draw.ts') && runtimeImport.test(readFileSync(f, 'utf8')))
      .map((f) => f.slice(root.length + 1))
    for (const f of importers) expect(f).toMatch(/^components\/settings\/personas\//)
  })

  it('the importer check really does see a runtime import and skips a type-only one', () => {
    const runtimeImport = /^\s*(import|export)\s+(?!type\b)[^'"\n]*from\s+['"][^'"]*persona-draw['"]|import\(\s*['"][^'"]*persona-draw['"]/m
    expect(runtimeImport.test("import { drawRecipe } from './persona-draw'")).toBe(true)
    expect(runtimeImport.test("const m = await import('../../lib/names/persona-draw')")).toBe(true)
    expect(runtimeImport.test("import type { DicebearStyle } from './persona-draw'")).toBe(false)
  })
})

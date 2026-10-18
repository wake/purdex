// DiceBear is large (core 461 KB, plus up to 373 KB per style). It must stay out of the main chunk, which means no
// static `import ... from '@dicebear/...'` anywhere in the SPA: only `import('@dicebear/...')` inside persona-draw.ts
// (plan P3). A static import anywhere would pull it into whichever chunk imports that file.
import { describe, it, expect } from 'vitest'

// Every non-test source file, raw (the same way `lib/shown-hosts.import-guard.test.ts` reads them; `tsconfig.app.json`
// has no node types, so no `node:fs`). Keys are `/src/…` paths.
const SOURCES = import.meta.glob<string>(['/src/**/*.{ts,tsx}', '!/src/**/*.test.{ts,tsx}', '!/src/**/*.d.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
})

// `import x from '…'`, `import { x } from '…'`, `export … from '…'`, a bare side-effect `import '…'`, and `require('…')`.
const STATIC = /^\s*(?:(?:import|export)\s[^'"\n]*from\s+|import\s+|.*\brequire\(\s*)['"]@dicebear\//m
const DYNAMIC = /import\(\s*['"]@dicebear\//
// A runtime import of persona-draw; a type-only import is erased by the compiler and carries nothing.
const RUNTIME_DRAW =
  /^\s*(import|export)\s+(?!type\b)[^'"\n]*from\s+['"][^'"]*persona-draw['"]|import\(\s*['"][^'"]*persona-draw['"]/m

const paths = Object.keys(SOURCES).filter((p) => p !== '/src/lib/names/persona-draw-guard.test.ts')

describe('DiceBear stays in the lazy chunk', () => {
  it('reads the source tree', () => {
    expect(paths).toContain('/src/lib/names/persona-draw.ts')
    expect(paths.length).toBeGreaterThan(100)
  })

  it('is never imported statically', () => {
    expect(paths.filter((p) => STATIC.test(SOURCES[p]))).toEqual([])
  })

  it('the static check really does see every static form', () => {
    for (const line of [
      "import { Avatar } from '@dicebear/core'",
      "import core from '@dicebear/core'",
      "export * from '@dicebear/core'",
      "export { Avatar } from '@dicebear/core'",
      "import '@dicebear/core'",
      "const a = require('@dicebear/core')",
    ]) expect(STATIC.test(line)).toBe(true)
    expect(STATIC.test("const m = await import('@dicebear/core')")).toBe(false)
  })

  it('is imported dynamically only by persona-draw.ts', () => {
    expect(paths.filter((p) => DYNAMIC.test(SOURCES[p]))).toEqual(['/src/lib/names/persona-draw.ts'])
  })

  it('persona-draw.ts is imported at runtime by nothing outside the persona page', () => {
    // Until the page (NA-1b-2b) exists, nobody does.
    const importers = paths.filter((p) => p !== '/src/lib/names/persona-draw.ts' && RUNTIME_DRAW.test(SOURCES[p]))
    for (const p of importers) expect(p).toMatch(/^\/src\/components\/settings\/personas\//)
  })

  it('the importer check sees a runtime import and skips a type-only one', () => {
    expect(RUNTIME_DRAW.test("import { drawRecipe } from './persona-draw'")).toBe(true)
    expect(RUNTIME_DRAW.test("const m = await import('../../lib/names/persona-draw')")).toBe(true)
    expect(RUNTIME_DRAW.test("import type { DicebearStyle } from './persona-draw'")).toBe(false)
  })
})

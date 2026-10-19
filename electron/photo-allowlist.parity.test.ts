// electron/photo-allowlist.ts is a copy of spa/src/lib/names/photo-allowlist.ts: this tsconfig's rootDir is `electron/`, so
// it cannot import from `spa/`. The two must not drift, because the page checks the first URL and the main process checks
// it again and every redirect hop with the SAME rules (name pool v2 plan P2). This holds them identical.
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const here = __dirname
const read = (rel: string) => readFileSync(join(here, rel), 'utf8')

/** The code from the first constant on: the header comment may differ, the logic may not. */
function logic(src: string): string {
  const at = src.indexOf('const FETCH_HOSTS')
  expect(at).toBeGreaterThan(-1)
  return src.slice(at)
}

describe('photo allowlist copies', () => {
  it('electron/photo-allowlist.ts has exactly the logic of the SPA module', () => {
    expect(logic(read('photo-allowlist.ts'))).toBe(logic(read('../spa/src/lib/names/photo-allowlist.ts')))
  })
})

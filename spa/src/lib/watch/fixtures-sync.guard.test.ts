// Guard: the JSON samples in __fixtures__/ are byte copies of the daemon's wire testdata (internal/wwd/wire/testdata). When the Go side
// changes one and this side does not follow, this goes red instead of the parse tests quietly checking a stale shape. (The same guard for the
// pool is lib/pool/fixtures-sync.guard.test.ts; it reads only lib/pool/__fixtures__, so these copies need their own.)
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readdirSync, readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'

// @ts-expect-error __dirname is untyped here.
const HERE: string = resolve(__dirname, '__fixtures__')
// @ts-expect-error __dirname is untyped here.
const GO: string = resolve(__dirname, '../../../../internal/wwd/wire/testdata')

describe('watch fixtures follow the daemon testdata', () => {
  const copies = (readdirSync(HERE) as string[]).filter((f) => f.endsWith('.json'))
  const goFiles = (readdirSync(GO) as string[]).filter((f) => f.startsWith('watch_wake_settings_') && f.endsWith('.json'))

  it('there are copies to check', () => {
    expect(copies.length).toBeGreaterThan(0)
  })

  it.each(copies)('%s is identical to internal/wwd/wire/testdata/%s', (name: string) => {
    expect(readFileSync(resolve(HERE, name), 'utf8')).toBe(readFileSync(resolve(GO, name), 'utf8'))
  })

  // A copy that was never made would not fail the test above: every fixture the daemon has for the wake settings must have one here.
  it('every watch_wake_settings_* file of the daemon testdata has a copy', () => {
    expect(goFiles.length).toBeGreaterThan(0)
    expect([...copies].sort()).toEqual([...goFiles].sort())
  })
})

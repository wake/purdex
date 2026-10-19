// Guard: the JSON samples in __fixtures__/ are byte copies of the daemon's wire testdata (internal/wwd/wire/testdata). When the Go side
// changes one and this side does not follow, this goes red instead of the parse tests quietly checking a stale shape.
// Paths are from the working directory: vitest runs in spa/, and the jsdom environment gives import.meta.url no file scheme.
import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const HERE = resolve(process.cwd(), 'src/lib/pool/__fixtures__')
const GO = resolve(process.cwd(), '../internal/wwd/wire/testdata')

describe('pool fixtures follow the daemon testdata', () => {
  const copies = readdirSync(HERE).filter((f) => f.endsWith('.json'))

  it('there are copies to check', () => {
    expect(copies.length).toBeGreaterThan(0)
  })

  it.each(copies)('%s is identical to internal/wwd/wire/testdata/%s', (name) => {
    expect(readFileSync(resolve(HERE, name), 'utf8')).toBe(readFileSync(resolve(GO, name), 'utf8'))
  })
})

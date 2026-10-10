// The Mac's notification text rule is the phone push's (#2144): this runs the SAME fixture the Go test runs
// (internal/push/testdata/normalise.json -> TestNormaliseFixture), against the port.
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
import { normaliseNotificationText } from './notification-normalise'

interface Case { name: string; input: string; max: number; want: string }
// @ts-expect-error __dirname is untyped here.
const cases = JSON.parse(readFileSync(resolve(__dirname, '../../../internal/push/testdata/normalise.json'), 'utf8')) as Case[]

describe('normaliseNotificationText (the shared fixture)', () => {
  it('reads the whole fixture (it is shared with the Go test and must not shrink silently)', () => {
    expect(cases.length).toBeGreaterThanOrEqual(30)
  })

  it.each(cases)('$name', ({ input, max, want }) => {
    expect(normaliseNotificationText(input, max)).toBe(want)
  })
})

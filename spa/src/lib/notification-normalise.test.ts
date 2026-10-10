// The Mac's notification text rule is the phone push's (#2144): this runs the SAME fixture the Go test runs
// (internal/push/testdata/normalise.json -> TestNormaliseFixture), against the port.
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
import { normaliseNotificationText, plainNotificationText } from './notification-normalise'

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

// What a JSON fixture cannot carry: Go reads a lone surrogate as invalid UTF-8, which is U+FFFD.
describe('lone surrogates', () => {
  it.each(['a\ud800b', 'a\udc00b'])('%j becomes U+FFFD, as in Go', (s) => {
    expect(normaliseNotificationText(s, 240)).toBe('a�b')
    expect(plainNotificationText(s, 240)).toBe('a�b')
  })
  it('a proper surrogate pair is one character', () => {
    expect(normaliseNotificationText('😀', 240)).toBe('😀')
  })
})

describe('plainNotificationText (a title or a name: no Markdown rules)', () => {
  it('keeps what Markdown would take, cleans spaces and format characters, and cuts', () => {
    expect(plainNotificationText('my__session__x', 120)).toBe('my__session__x')
    expect(plainNotificationText('a‮b​c d', 120)).toBe('abc d')
    expect(plainNotificationText('abcdef', 5)).toBe('abcde…')
  })
})

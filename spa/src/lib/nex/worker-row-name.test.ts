// spa/src/lib/nex/worker-row-name.test.ts — #1771: the name of a row in the Workers lists.
import { describe, it, expect } from 'vitest'
import { workerRowName } from './worker-row-name'
import type { ExecutionSummary } from './types'

type Row = Pick<ExecutionSummary, 'brief' | 'cwd' | 'session_title'>
const titled = (text: string): Row['session_title'] => ({ text, source: 'ai' })
const r = (over: Partial<Row> = {}): Row => ({ brief: '', cwd: '/w/repo', ...over })

describe('workerRowName (#1771: brief → session_title (capability) → cwd basename → empty)', () => {
  it('a brief wins, even when a session_title exists and the capability is present', () => {
    expect(workerRowName(r({ brief: 'Fix the bug', session_title: titled('Zebrafinch') }), true)).toBe('Fix the bug')
  })

  it('the brief is shown as today: its first line, capped at 80 with an ellipsis, untrimmed', () => {
    expect(workerRowName(r({ brief: `${'a'.repeat(100)}\nsecond line` }), true)).toBe(`${'a'.repeat(79)}…`)
    expect(workerRowName(r({ brief: '  padded task  \nmore' }), true)).toBe('  padded task  ')
  })

  it('an empty brief (a handoff) + a session_title + the capability → the title', () => {
    expect(workerRowName(r({ session_title: titled('Zebrafinch') }), true)).toBe('Zebrafinch')
  })

  it('the same row without the capability → the cwd basename (fail closed: the title is never read)', () => {
    expect(workerRowName(r({ session_title: titled('Zebrafinch') }), false)).toBe('repo')
  })

  it('no session_title (or a blank one) with the capability → the cwd basename', () => {
    expect(workerRowName(r(), true)).toBe('repo')
    expect(workerRowName(r({ session_title: titled('   ') }), true)).toBe('repo')
  })

  it('a trailing slash on the cwd still names the last segment', () => {
    expect(workerRowName(r({ cwd: '/w/repo/' }), false)).toBe('repo')
  })

  it("cwd '/' or empty → '' (today's look)", () => {
    expect(workerRowName(r({ cwd: '/' }), false)).toBe('')
    expect(workerRowName(r({ cwd: '' }), false)).toBe('')
    expect(workerRowName(r({ cwd: '/', session_title: titled('  ') }), true)).toBe('')
  })

  it('a whitespace-only brief counts as empty', () => {
    expect(workerRowName(r({ brief: '   ', session_title: titled('Zebrafinch') }), true)).toBe('Zebrafinch')
    expect(workerRowName(r({ brief: ' \t ' }), false)).toBe('repo')
  })

  it('a brief whose first line is blank counts as empty (the first line is what the row shows)', () => {
    expect(workerRowName(r({ brief: '\nsecond line', session_title: titled('Zebrafinch') }), true)).toBe('Zebrafinch')
  })

  it('a multi-line title is one-lined and trimmed (the tab-title rule, `oneLine`)', () => {
    expect(workerRowName(r({ session_title: titled('  Zebrafinch \r\nsecond line') }), true)).toBe('Zebrafinch')
  })

  it('a long title is not capped (the row truncates by CSS)', () => {
    const long = 't'.repeat(120)
    expect(workerRowName(r({ session_title: titled(long) }), true)).toBe(long)
  })

  it('garbage fields from a seeded page never throw: a non-string brief / title / cwd counts as absent', () => {
    const garbage = { brief: 42, cwd: 7, session_title: { text: 9, source: 'ai' } } as unknown as Row
    expect(workerRowName(garbage, true)).toBe('')
    const titleNotObject = { brief: 42, cwd: '/w/repo', session_title: 'x' } as unknown as Row
    expect(workerRowName(titleNotObject, true)).toBe('repo')
    const nullTitle = { brief: '', cwd: '/w/repo', session_title: null } as unknown as Row
    expect(workerRowName(nullTitle, true)).toBe('repo')
  })
})

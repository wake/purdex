import { describe, it, expect } from 'vitest'
import { workerTitleOf } from './worker-summary'
import type { ExecutionSummary } from './types'

describe('workerTitleOf (spec §8.4; phase E: session_title gated by the host capability)', () => {
  type Summary = Pick<ExecutionSummary, 'brief' | 'cwd' | 'session_title'>
  const summary = (over: Partial<Summary> = {}): Summary => ({ brief: 'Fix the bug', cwd: '/w/repo', ...over })
  const titled = (text: string): Summary['session_title'] => ({ text, source: 'ai' })

  it('titleSupported: session_title wins over the brief', () => {
    expect(workerTitleOf({}, summary({ session_title: titled('Fix login') }), true)).toBe('Fix login - repo')
  })

  it('titleSupported: session_title wins over a pre-handoff title too', () => {
    expect(workerTitleOf({ fromTitle: 'Old terminal' }, summary({ session_title: titled('Fix login') }), true)).toBe('Fix login - repo')
  })

  it('not titleSupported: session_title is ignored, falls back to the brief', () => {
    expect(workerTitleOf({}, summary({ session_title: titled('Fix login') }), false)).toBe('Fix the bug - repo')
  })

  it('not titleSupported: falls back to the pre-handoff title when present', () => {
    expect(workerTitleOf({ fromTitle: 'Old terminal' }, summary({ session_title: titled('Fix login') }), false)).toBe('Old terminal - repo')
  })

  it('no summary at all: null regardless of titleSupported', () => {
    expect(workerTitleOf({}, null, true)).toBeNull()
    expect(workerTitleOf({}, undefined, false)).toBeNull()
  })

  it('a session_title containing markup-looking text passes through as plain data, never interpreted', () => {
    expect(workerTitleOf({}, summary({ session_title: titled('Fix <b>login</b> bug') }), true)).toBe('Fix <b>login</b> bug - repo')
  })
})

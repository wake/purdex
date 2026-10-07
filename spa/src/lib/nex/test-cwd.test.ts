import { describe, it, expect } from 'vitest'
import cases from '../../../../internal/conversations/testdata/test-cwd-cases.json'
import { isTestCwd } from './test-cwd'

describe('isTestCwd (shared case table with the daemon)', () => {
  it.each(cases as { cwd: string; want: boolean }[])('isTestCwd($cwd) = $want', ({ cwd, want }) => {
    expect(isTestCwd(cwd)).toBe(want)
  })
  it('undefined is not a test cwd', () => {
    expect(isTestCwd(undefined)).toBe(false)
  })
})

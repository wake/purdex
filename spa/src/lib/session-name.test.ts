import { describe, it, expect } from 'vitest'
import { isValidSessionName } from './session-name'

describe('isValidSessionName', () => {
  it.each([
    'my-session',
    'Session_01',
    'abc',
    'A',
    '0',
    'test-123_foo',
    'ALL-CAPS-NAME',
  ])('returns true for valid name: %s', (name) => {
    expect(isValidSessionName(name)).toBe(true)
  })

  it.each([
    ['space', 'hello world'],
    ['dot', 'my.session'],
    ['colon', 'tmux:session'],
    ['Chinese', '我的session'],
    ['slash', 'path/name'],
    ['at sign', 'user@host'],
    ['plus', 'a+b'],
    ['empty', ''],
  ])('returns false for invalid name (%s): %s', (_label, name) => {
    expect(isValidSessionName(name)).toBe(false)
  })
})

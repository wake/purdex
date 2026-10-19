import { describe, it, expect } from 'vitest'
import { compareWwdVersion, olderThanNewest } from './version'

describe('compareWwdVersion', () => {
  it('orders the numeric parts as numbers, not text', () => {
    expect(compareWwdVersion('1.0.0-alpha.755', '1.0.0-alpha.754')).toBeGreaterThan(0)
    expect(compareWwdVersion('1.0.0-alpha.99', '1.0.0-alpha.100')).toBeLessThan(0)
  })
  it('equal versions are 0', () => {
    expect(compareWwdVersion('1.2.3', '1.2.3')).toBe(0)
  })
  it('a higher major or minor wins whatever follows', () => {
    expect(compareWwdVersion('1.1.0', '1.0.9')).toBeGreaterThan(0)
    expect(compareWwdVersion('2.0.0', '1.99.99')).toBeGreaterThan(0)
  })
  it('a release is newer than its own prerelease', () => {
    expect(compareWwdVersion('1.0.0', '1.0.0-alpha.9')).toBeGreaterThan(0)
  })
  it('text that is not a version does not throw and compares as text', () => {
    expect(compareWwdVersion('dev', 'dev')).toBe(0)
    expect(() => compareWwdVersion('', '1.0.0')).not.toThrow()
  })
})

describe('olderThanNewest', () => {
  it('marks only the hosts behind the newest', () => {
    expect(olderThanNewest({ mlab: '1.0.0-alpha.755', air26: '1.0.0-alpha.755', air19: '1.0.0-alpha.754' })).toEqual(new Set(['air19']))
  })
  it('all the same: nothing is marked', () => {
    expect(olderThanNewest({ a: '1.0.0', b: '1.0.0' })).toEqual(new Set())
  })
  it('a host with no known version is not marked (it is not older, it is unknown)', () => {
    expect(olderThanNewest({ a: '1.0.0', b: '' })).toEqual(new Set())
  })
  it('one host alone is not behind anything', () => {
    expect(olderThanNewest({ a: '1.0.0' })).toEqual(new Set())
  })
})

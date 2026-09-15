import { describe, it, expect } from 'vitest'
import { nextProjectSessionName } from './launch-session-name'

describe('nextProjectSessionName', () => {
  it.each([
    ['no sessions', 'purdex', [], 'purdex-1'],
    ['bare slug counts', 'purdex', ['purdex'], 'purdex-2'],
    ['numbered ones count', 'purdex', ['purdex-1', 'purdex-2'], 'purdex-3'],
    ['others do not count', 'purdex', ['purdex-x', 'purdexy-1', 'dev', 'purdex-1a'], 'purdex-1'],
    ['a gap is not reused but the count is', 'purdex', ['purdex-5'], 'purdex-2'],
    ['increments past a taken candidate', 'purdex', ['purdex', 'purdex-2', 'purdex-3'], 'purdex-4'],
    ['collision chain', 'p', ['p-2', 'p-3', 'p-1'], 'p-4'],
  ])('%s', (_label, slug, live, want) => {
    expect(nextProjectSessionName(slug as string, live as string[])).toBe(want)
  })

  it('bump advances past a name the daemon just refused', () => {
    expect(nextProjectSessionName('purdex', [], 1)).toBe('purdex-2')
    expect(nextProjectSessionName('purdex', ['purdex-3'], 2)).toBe('purdex-4')
  })

  it('treats regex metacharacters in a slug literally', () => {
    // Slugs are [a-z0-9-] by validation; this guards the helper anyway.
    // Unescaped, `a.b` would also match `axb-2` and answer `a.b-3`.
    expect(nextProjectSessionName('a.b', ['a.b-1', 'axb-2'], 0)).toBe('a.b-2')
    expect(nextProjectSessionName('a-b', ['a-b-1', 'axb-2'], 0)).toBe('a-b-2')
  })
})

import { describe, it, expect, beforeEach } from 'vitest'
import { createRegistry } from './registry'
import type { LinkMatcher, LinkOpener, LinkToken } from './types'

describe('terminal-link registry', () => {
  let registry: ReturnType<typeof createRegistry>
  beforeEach(() => { registry = createRegistry() })

  it('registers and lists matchers', () => {
    const m: LinkMatcher = { id: 'm1', type: 'url', provide: () => [] }
    const dispose = registry.registerMatcher(m)
    expect(registry.getMatchers()).toEqual([m])
    dispose()
    expect(registry.getMatchers()).toEqual([])
  })

  it('dispatches to first opener whose canOpen returns true', () => {
    const calls: string[] = []
    const o1: LinkOpener = {
      id: 'o1', priority: 0,
      canOpen: (t) => t.type === 'url',
      open: () => { calls.push('o1') },
    }
    const o2: LinkOpener = {
      id: 'o2', priority: 10,
      canOpen: (t) => t.type === 'url',
      open: () => { calls.push('o2') },
    }
    registry.registerOpener(o1)
    registry.registerOpener(o2)
    const token: LinkToken = { type: 'url', text: 'https://x', range: { startCol: 0, endCol: 9 } }
    registry.dispatch(token, {}, new MouseEvent('click'))
    expect(calls).toEqual(['o2'])
  })

  it('dispatch returns false when no opener matches', () => {
    const token: LinkToken = { type: 'unknown', text: 'x', range: { startCol: 0, endCol: 1 } }
    expect(registry.dispatch(token, {}, new MouseEvent('click'))).toBe(false)
  })

  it('clear() empties matchers and openers', () => {
    registry.registerMatcher({ id: 'm', type: 't', provide: () => [] })
    registry.registerOpener({ id: 'o', canOpen: () => true, open: () => {} })
    registry.clear()
    expect(registry.getMatchers()).toEqual([])
    const token: LinkToken = { type: 't', text: 'x', range: { startCol: 0, endCol: 1 } }
    expect(registry.dispatch(token, {}, new MouseEvent('click'))).toBe(false)
  })
})

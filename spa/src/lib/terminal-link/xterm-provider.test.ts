import { describe, it, expect, vi } from 'vitest'
import type { Terminal } from '@xterm/xterm'
import { createRegistry } from './registry'
import { createXtermLinkProvider } from './xterm-provider'

function makeTerm(lineText: string, paddedText?: string): Terminal {
  return {
    buffer: {
      active: {
        getLine: (y: number) =>
          y === 0
            ? { translateToString: (trimRight?: boolean) => (trimRight ? lineText : (paddedText ?? lineText)) }
            : undefined,
      },
    },
  } as unknown as Terminal
}

describe('createXtermLinkProvider', () => {
  it('calls back with no links when no matcher produces results', () => {
    const registry = createRegistry()
    const provider = createXtermLinkProvider(registry, () => ({}), makeTerm('hello'))
    const cb = vi.fn()
    provider.provideLinks(1, cb)
    expect(cb).toHaveBeenCalledWith([])
  })

  it('builds ILink per matched token with 1-indexed range', () => {
    const registry = createRegistry()
    registry.registerMatcher({
      id: 'm', type: 'url',
      provide: () => [{ text: 'foo', range: { startCol: 2, endCol: 5 } }],
    })
    const provider = createXtermLinkProvider(registry, () => ({ hostId: 'h1' }), makeTerm('  foo'))
    const cb = vi.fn()
    provider.provideLinks(1, cb)
    expect(cb).toHaveBeenCalledTimes(1)
    const links = cb.mock.calls[0][0]
    expect(links).toHaveLength(1)
    expect(links[0].text).toBe('foo')
    expect(links[0].range).toEqual({
      start: { x: 3, y: 1 },
      end:   { x: 5, y: 1 },
    })
  })

  it('activate dispatches token to registry with ctx', () => {
    const registry = createRegistry()
    registry.registerMatcher({
      id: 'm', type: 'url',
      provide: () => [{ text: 'x', range: { startCol: 0, endCol: 1 } }],
    })
    const open = vi.fn()
    registry.registerOpener({ id: 'o', canOpen: () => true, open })
    const provider = createXtermLinkProvider(registry, () => ({ hostId: 'h2' }), makeTerm('x'))
    const cb = vi.fn()
    provider.provideLinks(1, cb)
    const link = cb.mock.calls[0][0][0]
    const event = new MouseEvent('click')
    link.activate(event, 'x')
    expect(open).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'url', text: 'x' }),
      { hostId: 'h2' },
      event,
    )
  })

  it('skips empty callback when terminal buffer line missing', () => {
    const registry = createRegistry()
    const term = { buffer: { active: { getLine: () => undefined } } } as unknown as Terminal
    const provider = createXtermLinkProvider(registry, () => ({}), term)
    const cb = vi.fn()
    provider.provideLinks(99, cb)
    expect(cb).toHaveBeenCalledWith([])
  })

  it('requests trimmed line text (trimRight=true) so matcher columns reflect content', () => {
    const registry = createRegistry()
    const calls: string[] = []
    registry.registerMatcher({
      id: 'm', type: 'url',
      provide: (line) => { calls.push(line); return [] },
    })
    // Untrimmed returns padded text; trimmed returns just 'foo'
    const provider = createXtermLinkProvider(registry, () => ({}), makeTerm('foo', 'foo             '))
    provider.provideLinks(1, vi.fn())
    expect(calls).toEqual(['foo'])
  })
})

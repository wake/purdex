import { describe, it, expect, vi } from 'vitest'
import type { Terminal } from '@xterm/xterm'
import { createRegistry } from './registry'
import { createXtermLinkProvider } from './xterm-provider'

function makeTerm(
  lineText: string,
  opts?: { padded?: string; cells?: Array<[string, number]> },
): Terminal {
  const cells = opts?.cells ?? lineText.split('').map((c) => [c, 1] as [string, number])
  const flat: Array<{ chars: string; width: number }> = []
  for (const [c, w] of cells) {
    flat.push({ chars: c, width: w })
    for (let i = 1; i < w; i++) flat.push({ chars: '', width: 0 })
  }
  return {
    buffer: {
      active: {
        getLine: (y: number) => y !== 0 ? undefined : {
          length: flat.length,
          translateToString: (trimRight?: boolean) =>
            trimRight ? lineText : (opts?.padded ?? lineText),
          getCell: (x: number) => {
            const cell = flat[x]
            return cell ? { getWidth: () => cell.width, getChars: () => cell.chars } : undefined
          },
        },
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
    const provider = createXtermLinkProvider(registry, () => ({}), makeTerm('foo', { padded: 'foo             ' }))
    provider.provideLinks(1, vi.fn())
    expect(calls).toEqual(['foo'])
  })

  it('converts JS offset to terminal column for CJK prefix', () => {
    // 完整：https://x
    // JS offsets:   完(0) 整(1) ：(2) h(3) t(4) t(5) p(6) s(7) :(8) /(9) /(10) x(11)
    // Cells (cols): 完(0-1) 整(2-3) ：(4-5) h(6) t(7) t(8) p(9) s(10) :(11) /(12) /(13) x(14)
    const lineText = '完整：https://x'
    const cells: Array<[string, number]> = [
      ['完', 2], ['整', 2], ['：', 2],
      ['h', 1], ['t', 1], ['t', 1], ['p', 1], ['s', 1], [':', 1], ['/', 1], ['/', 1], ['x', 1],
    ]
    const registry = createRegistry()
    registry.registerMatcher({
      id: 'm', type: 'url',
      // matcher returns JS offsets (mirroring matchAll's m.index)
      provide: () => [{ text: 'https://x', range: { startCol: 3, endCol: 12 } }],
    })
    const provider = createXtermLinkProvider(registry, () => ({}), makeTerm(lineText, { cells }))
    const cb = vi.fn()
    provider.provideLinks(1, cb)
    const link = cb.mock.calls[0][0][0]
    // terminal col: 'h' start col=6 → 1-indexed start x=7; 'x' end col=14 (exclusive endCol=15), 1-indexed inclusive end=15
    expect(link.range).toEqual({
      start: { x: 7, y: 1 },
      end:   { x: 15, y: 1 },
    })
  })
})

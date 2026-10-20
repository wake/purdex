// spa/src/components/watch/jobs/QueueRoutes.test.tsx — the SVG over the flow (monitoring tabs spec M29, M30): one route per active card, measured again on resize, on the fonts arriving, and when the cards change; static.
// jsdom has no layout: the rectangles come from a table the test fills, read by the elements' data attributes.
import { useRef } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { QueueRoutes, type ActiveCard, type CandidateSpec } from './QueueRoutes'

type Box = [left: number, top: number, width: number, height: number]
let boxes: Record<string, Box> = {}
const BASE: Record<string, Box> = {
  'flow': [0, 0, 1000, 400],
  'qtop': [0, 0, 1000, 100], 'qbot': [0, 200, 1000, 100],
  'pool:A': [0, 0, 400, 100], 'pool:B': [410, 0, 400, 100],
  'wk:W1': [0, 200, 480, 100], 'wk:W2': [490, 200, 480, 100],
  'j:mlab/a': [10, 230, 200, 28], 'j:mlab/b': [220, 230, 200, 28], 'j:mserv/c': [500, 230, 200, 28],
}
function boxOf(e: Element): Box {
  const k = e.hasAttribute('data-flow') ? 'flow' : e.hasAttribute('data-qtop') ? 'qtop' : e.hasAttribute('data-qbot') ? 'qbot'
    : e.hasAttribute('data-pool') ? `pool:${e.getAttribute('data-pool')}` : e.hasAttribute('data-wk') ? `wk:${e.getAttribute('data-wk')}` : e.hasAttribute('data-j') ? `j:${e.getAttribute('data-j')}` : ''
  return boxes[k] ?? [0, 0, 0, 0]
}

const disconnect = vi.fn()
let resizeCb: () => void = () => {}
class FakeRO { constructor(cb: () => void) { resizeCb = cb } observe() {} disconnect() { disconnect() } unobserve() {} }
let fontsDone: () => void = () => {}

const ALL: ActiveCard[] = [{ key: 'mlab/a', pool: 'A', worker: 'W1' }, { key: 'mlab/b', pool: 'A', worker: 'W1' }, { key: 'mserv/c', pool: 'B', worker: 'W2' }]

function Harness({ active, highlighted = new Set<string>(), candidate = null }: { active: ActiveCard[]; highlighted?: ReadonlySet<string>; candidate?: CandidateSpec | null }) {
  const ref = useRef<HTMLDivElement>(null)
  const has = (k: string) => active.some((a) => a.key === k)
  return (
    <div ref={ref} data-flow>
      <div data-qtop><div data-pool="A" /><div data-pool="B" /></div>
      <div data-qbot>
        <div data-wk="W1">{has('mlab/a') && <button data-j="mlab/a" />}{has('mlab/b') && <button data-j="mlab/b" />}</div>
        <div data-wk="W2">{has('mserv/c') && <button data-j="mserv/c" />}</div>
      </div>
      <QueueRoutes flowRef={ref} active={active} highlighted={highlighted} candidate={candidate} colorOf={(p) => (p === 'A' ? 'rgb(1, 2, 3)' : undefined)} />
    </div>
  )
}
const routeD = (k: string) => document.querySelector(`[data-r="${k}"] path`)?.getAttribute('d')

beforeEach(() => {
  cleanup(); disconnect.mockClear(); boxes = { ...BASE }
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    const [l, t, w, h] = boxOf(this)
    return { left: l, top: t, width: w, height: h, right: l + w, bottom: t + h, x: l, y: t, toJSON: () => ({}) } as DOMRect
  })
  vi.stubGlobal('ResizeObserver', FakeRO)
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 1 })
  vi.stubGlobal('cancelAnimationFrame', () => {})
  Object.defineProperty(document, 'fonts', { configurable: true, value: { ready: new Promise<void>((r) => { fontsDone = r }) } })
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('QueueRoutes', () => {
  it('one route per active card, from the pool\'s bottom edge to the worker cell\'s top edge, in the colour of the host that sent it', () => {
    render(<Harness active={ALL} />)
    expect(document.querySelectorAll('g[data-r]')).toHaveLength(3)
    const g = document.querySelector('[data-r="mlab/a"]') as SVGElement
    expect(g.style.color).toBe('rgb(1, 2, 3)')
    expect(document.querySelector('[data-r="mserv/c"]')!.getAttribute('class')).toContain('text-text-muted')
    expect(routeD('mlab/a')).toMatch(/^M\d/)
    expect(screen.getByTestId('queue-routes')).toHaveAttribute('viewBox', '0 0 1000 400')
  })

  it('the SVG does not clip: a wrapped row\'s gutter is just outside the rows', () => {
    render(<Harness active={ALL} />)
    expect(screen.getByTestId('queue-routes').getAttribute('class')).toContain('overflow-visible')
  })

  it('a card that is not in the DOM has no route', () => {
    render(<Harness active={[ALL[0]]} />)
    expect(document.querySelectorAll('g[data-r]')).toHaveLength(1)
  })

  it('a resize of the flow draws again', () => {
    render(<Harness active={ALL} />)
    const before = routeD('mlab/a')
    boxes['pool:A'] = [0, 0, 700, 100]
    boxes['j:mlab/a'] = [300, 230, 200, 28]
    act(() => resizeCb())
    expect(routeD('mlab/a')).not.toBe(before)
  })

  it('the fonts arriving draw again', async () => {
    render(<Harness active={ALL} />)
    const before = routeD('mlab/a')
    boxes['wk:W1'] = [0, 200, 480, 140]
    boxes['j:mlab/a'] = [40, 230, 200, 28]
    await act(async () => { fontsDone(); await Promise.resolve() })
    expect(routeD('mlab/a')).not.toBe(before)
  })

  it('cards coming and going change the routes; leaving disconnects the observer', () => {
    const { rerender, unmount } = render(<Harness active={[ALL[0]]} />)
    expect(document.querySelectorAll('g[data-r]')).toHaveLength(1)
    rerender(<Harness active={ALL} />)
    expect(document.querySelectorAll('g[data-r]')).toHaveLength(3)
    unmount()
    expect(disconnect).toHaveBeenCalled()
  })

  it('M28: nothing animates — no <animate>, no animate- or transition class', () => {
    render(<Harness active={ALL} highlighted={new Set(['mlab/a'])} candidate={{ pool: 'A', workers: ['W1', 'W2'] }} />)
    const html = screen.getByTestId('queue-routes').outerHTML
    expect(html).not.toMatch(/<animate/)
    expect(html).not.toMatch(/animate-|transition/)
  })

  it('a lit route is data-hl and drawn last; the others keep exactly what they had', () => {
    const { rerender } = render(<Harness active={ALL} />)
    const plain = Object.fromEntries(Array.from(document.querySelectorAll('g[data-r]')).map((g) => [g.getAttribute('data-r'), g.outerHTML]))
    rerender(<Harness active={ALL} highlighted={new Set(['mlab/a'])} />)
    const groups = Array.from(document.querySelectorAll('g[data-r]'))
    expect(groups.at(-1)!.getAttribute('data-r')).toBe('mlab/a')
    expect(groups.filter((g) => g.hasAttribute('data-hl')).map((g) => g.getAttribute('data-r'))).toEqual(['mlab/a'])
    for (const g of groups.filter((x) => x.getAttribute('data-r') !== 'mlab/a')) expect(g.outerHTML).toBe(plain[g.getAttribute('data-r')!])
  })

  it('candidates are dashed lines to the given worker cells only', () => {
    render(<Harness active={ALL} candidate={{ pool: 'A', workers: ['W2'] }} />)
    const paths = document.querySelectorAll('[data-testid="queue-candidates"] path')
    expect(paths).toHaveLength(1)
    expect(paths[0]).toHaveAttribute('data-c', 'W2')
    expect(paths[0].getAttribute('stroke-dasharray')).toBeTruthy()
  })
})

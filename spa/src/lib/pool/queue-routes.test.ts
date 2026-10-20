// spa/src/lib/pool/queue-routes.test.ts — the geometry of the task queue's routes (monitoring tabs spec M29, M30): every box is a literal; nothing here touches the DOM.
import { describe, it, expect } from 'vitest'
import { candidatesOf, linesOf, routeD, routesOf, type FlowLayout, type Rect } from './queue-routes'

const rect = (l: number, t: number, w: number, h: number): Rect => ({ l, t, r: l + w, b: t + h, w })

/** Points along a path made of M, L, Q and C segments, 8 px apart at most. */
function sample(d: string): Array<[number, number]> {
  const tok = d.match(/[MLQC]|-?\d+(?:\.\d+)?/g) ?? []
  const pts: Array<[number, number]> = []
  let i = 0
  let cur: [number, number] = [0, 0]
  const num = () => Number(tok[i++])
  while (i < tok.length) {
    const c = tok[i++]
    if (c === 'M') { cur = [num(), num()]; pts.push(cur); continue }
    const pieces = c === 'L' ? 1 : c === 'Q' ? 2 : 3
    const ctl: Array<[number, number]> = []
    for (let k = 0; k < pieces; k++) ctl.push([num(), num()])
    const steps = 40
    const [p0] = [cur]
    for (let s = 1; s <= steps; s++) {
      const u = s / steps
      const v = 1 - u
      let x: number
      let y: number
      if (c === 'L') { x = p0[0] + (ctl[0][0] - p0[0]) * u; y = p0[1] + (ctl[0][1] - p0[1]) * u }
      else if (c === 'Q') { x = v * v * p0[0] + 2 * v * u * ctl[0][0] + u * u * ctl[1][0]; y = v * v * p0[1] + 2 * v * u * ctl[0][1] + u * u * ctl[1][1] }
      else { x = v ** 3 * p0[0] + 3 * v * v * u * ctl[0][0] + 3 * v * u * u * ctl[1][0] + u ** 3 * ctl[2][0]; y = v ** 3 * p0[1] + 3 * v * v * u * ctl[0][1] + 3 * v * u * u * ctl[1][1] + u ** 3 * ctl[2][1] }
      pts.push([x, y])
    }
    cur = ctl[pieces - 1]
  }
  return pts
}
const inside = (p: [number, number], r: Rect) => p[0] > r.l + 0.01 && p[0] < r.r - 0.01 && p[1] > r.t + 0.01 && p[1] < r.b - 0.01

const poolsOneLine = { A: rect(0, 0, 300, 100), B: rect(310, 0, 300, 100), C: rect(620, 0, 300, 100) }
const workersOneLine = { W1: rect(0, 200, 450, 100), W2: rect(460, 200, 450, 100) }

/** Both rows on one line each. */
const flat = (cards: FlowLayout['cards']): FlowLayout => ({
  top: rect(0, 0, 920, 100), bot: rect(0, 200, 910, 100),
  topLines: linesOf(Object.values(poolsOneLine)), botLines: linesOf(Object.values(workersOneLine)),
  pools: poolsOneLine, workers: workersOneLine, cards,
})

/** The top row broke in two: A and B on the first line, C on the second; the bottom row broke too: W1 on the first line, W2 on the second. */
function wrapped(cards: FlowLayout['cards']): FlowLayout {
  const pools = { A: rect(0, 0, 300, 100), B: rect(310, 0, 300, 100), C: rect(0, 110, 300, 100) }
  const workers = { W1: rect(0, 240, 600, 100), W2: rect(0, 350, 600, 100) }
  return { top: rect(0, 0, 610, 210), bot: rect(0, 240, 600, 210), topLines: linesOf(Object.values(pools)), botLines: linesOf(Object.values(workers)), pools, workers, cards }
}

describe('linesOf', () => {
  it('cells whose tops are within 2 px are one line, the line\'s bottom is the lowest; lines come top to bottom', () => {
    expect(linesOf([rect(0, 110, 10, 50), rect(20, 0, 10, 40), rect(40, 1.5, 10, 60)])).toEqual([{ t: 0, b: 61.5 }, { t: 110, b: 160 }])
  })
})

describe('routesOf', () => {
  it('both rows on one line: one cubic curve from the pool\'s bottom edge to the worker cell\'s top edge', () => {
    const [r] = routesOf(flat([{ key: 'mlab/a', rect: rect(10, 210, 200, 28), pool: 'A', worker: 'W1' }]))
    expect(r.d.match(/C/g)).toHaveLength(1)
    expect(r.d).not.toContain('Q')
    expect(r.from).toEqual({ x: 150, y: 100 })
    expect(r.to).toEqual({ x: 28, y: 200 })
    expect(sample(r.d)[0]).toEqual([150, 100])
    const last = sample(r.d).at(-1)!
    expect(last[0]).toBeCloseTo(28, 3)
    expect(last[1]).toBeCloseTo(200, 3)
  })

  it('routes leaving one pool share its bottom edge evenly, in the order of their cards', () => {
    const rs = routesOf(flat([
      { key: 'x', rect: rect(250, 210, 200, 28), pool: 'A', worker: 'W1' },
      { key: 'y', rect: rect(10, 210, 200, 28), pool: 'A', worker: 'W1' },
    ]))
    const byKey = Object.fromEntries(rs.map((r) => [r.key, r.from.x]))
    expect(byKey.y).toBeCloseTo(100, 5)
    expect(byKey.x).toBeCloseTo(200, 5)
  })

  it('the ends reaching one worker cell are 34 px apart, starting 18 px in from the card', () => {
    const rs = routesOf(flat([
      { key: 'p', rect: rect(10, 210, 200, 28), pool: 'A', worker: 'W1' },
      { key: 'q', rect: rect(220, 210, 200, 28), pool: 'B', worker: 'W1' },
      { key: 'r', rect: rect(470, 210, 200, 28), pool: 'C', worker: 'W2' },
    ]))
    const to = Object.fromEntries(rs.map((r) => [r.key, r.to.x]))
    expect(to.p).toBe(28)
    expect(to.q).toBe(220 + 18 + 34)
    expect(to.r).toBe(470 + 18)
  })

  it('seven cards into one worker: every end stays above its own card, and a card that was not measured does not take a place', () => {
    const cards = [{ key: 'gone', rect: rect(0, 0, 1, 1), pool: 'nope', worker: 'W1' }, ...Array.from({ length: 7 }, (_, i) => ({ key: `k${i}`, rect: rect(10, 210 + i * 30, 200, 28), pool: 'A', worker: 'W1' }))]
    const rs = routesOf(flat(cards))
    expect(rs).toHaveLength(7)
    const k0 = rs.find((r) => r.key === 'k0')!
    expect(k0.to.x).toBe(10 + 18)
    for (const r of rs) {
      const c = cards.find((x) => x.key === r.key)!
      expect(r.to.x).toBeGreaterThan(c.rect.l)
      expect(r.to.x).toBeLessThanOrEqual(c.rect.r - 6)
    }
  })

  it('a card whose pool or worker was not measured has no route', () => {
    expect(routesOf(flat([{ key: 'z', rect: rect(0, 0, 1, 1), pool: 'gone', worker: 'W1' }]))).toEqual([])
  })

  it('wrapped rows: the route goes through the gaps and the gutter and never inside a cell', () => {
    const L = wrapped([
      { key: 'a', rect: rect(10, 250, 200, 28), pool: 'A', worker: 'W1' },
      { key: 'b', rect: rect(10, 360, 200, 28), pool: 'A', worker: 'W2' },
      { key: 'c', rect: rect(220, 250, 200, 28), pool: 'C', worker: 'W1' },
      { key: 'd', rect: rect(220, 360, 200, 28), pool: 'B', worker: 'W2' },
    ])
    const rs = routesOf(L)
    expect(rs).toHaveLength(4)
    const cells = [...Object.values(L.pools), ...Object.values(L.workers)]
    for (const r of rs) {
      // the pool on the last line meeting the worker on the first line is the one curve; the others go round
      expect(r.d.includes('Q'), r.key).toBe(r.key !== 'c')
      for (const p of sample(r.d)) for (const c of cells) expect(inside(p, c), `${r.key} at ${p}`).toBe(false)
    }
  })

  it('routes that share a gap or a gutter do not lie on top of each other', () => {
    const L = wrapped([
      { key: 'a', rect: rect(10, 250, 200, 28), pool: 'A', worker: 'W1' },
      { key: 'b', rect: rect(220, 250, 200, 28), pool: 'A', worker: 'W1' },
      { key: 'c', rect: rect(430, 250, 100, 28), pool: 'B', worker: 'W1' },
    ])
    const rs = routesOf(L)
    const gutterXs = rs.map((r) => new Set(sample(r.d).filter(([x]) => x < -1).map(([x]) => x.toFixed(1))))
    expect(rs).toHaveLength(3)
    const all = gutterXs.map((s) => [...s][0])
    expect(new Set(all).size).toBe(all.length)
  })
})

describe('routeD', () => {
  it('the first and the second route of a gap are at different heights (the lane offset)', () => {
    const L = wrapped([])
    const tally = { lane: 0, side: { l: 0, r: 0 } }
    const d1 = routeD(L, tally, { x: 100, t: 0, b: 100 }, { x: 50, t: 240 })
    const d2 = routeD(L, tally, { x: 120, t: 0, b: 100 }, { x: 70, t: 240 })
    const ys = (d: string) => sample(d).map(([, y]) => y.toFixed(1)).join()
    expect(ys(d1)).not.toBe(ys(d2))
  })
})

describe('candidatesOf', () => {
  it('draws to the middle of the top edge of each worker cell it is given, and only those', () => {
    const L = flat([])
    const cs = candidatesOf(L, 'A', ['W2'])
    expect(cs).toHaveLength(1)
    expect(cs[0].worker).toBe('W2')
    expect(cs[0].from).toEqual({ x: 150, y: 100 })
    expect(cs[0].to).toEqual({ x: 460 + 225, y: 200 })
    expect(candidatesOf(L, 'A', [])).toEqual([])
  })

  it('a pool or a worker that was not measured gives nothing', () => {
    expect(candidatesOf(flat([]), 'nope', ['W1'])).toEqual([])
    expect(candidatesOf(flat([]), 'A', ['nope'])).toEqual([])
  })
})

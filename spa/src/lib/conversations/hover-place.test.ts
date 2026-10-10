import { describe, it, expect } from 'vitest'
import { placeHoverCard } from './hover-place'

const VIEW = { w: 1000, h: 800 }
const anchor = (top: number, left = 500, w = 48, h = 48) => ({ left, top, width: w, height: h })
const CARD = { w: 376, h: 300 }

describe('placeHoverCard', () => {
  it('goes above the thumbnail when there is room, centred on it', () => {
    const p = placeHoverCard(anchor(600), CARD, VIEW)
    expect(p.top).toBe(600 - 8 - 300)
    expect(p.left).toBe(500 + 24 - 188)
    expect(p.side).toBe('above')
  })

  it('goes below when the thumbnail is too near the top for it', () => {
    const p = placeHoverCard(anchor(100), CARD, VIEW)
    expect(p.side).toBe('below')
    expect(p.top).toBe(100 + 48 + 8)
  })

  it('is clamped inside the viewport when neither side has room (the roomier side wins)', () => {
    const p = placeHoverCard(anchor(200, 500, 48, 48), { w: 376, h: 700 }, VIEW)
    expect(p.top).toBeGreaterThanOrEqual(8)
    expect(p.top + 700).toBeLessThanOrEqual(800 - 8)
    expect(p.side).toBe('below')
  })

  it('a card taller than the whole viewport starts at the top margin', () => {
    expect(placeHoverCard(anchor(300), { w: 376, h: 2000 }, VIEW).top).toBe(8)
  })

  it('is kept inside the viewport sideways', () => {
    expect(placeHoverCard(anchor(600, 0), CARD, VIEW).left).toBe(8)
    expect(placeHoverCard(anchor(600, 990), CARD, VIEW).left).toBe(1000 - 376 - 8)
  })

  it('exactly enough room above still goes above; one pixel less goes below', () => {
    expect(placeHoverCard(anchor(300 + 8 + 8), CARD, VIEW).side).toBe('above')
    expect(placeHoverCard(anchor(300 + 8 + 7), CARD, VIEW).side).toBe('below')
  })
})

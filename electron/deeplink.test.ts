import { describe, it, expect } from 'vitest'
import { pickDeeplinkTarget, type DeeplinkWindow } from './deeplink'

class FakeWin implements DeeplinkWindow {
  constructor(
    public readonly id: string,
    private destroyed = false,
  ) {}
  isDestroyed() {
    return this.destroyed
  }
  destroy() {
    this.destroyed = true
  }
}

describe('pickDeeplinkTarget', () => {
  it('returns null when there are no ready windows', () => {
    expect(pickDeeplinkTarget([], null)).toBeNull()
  })

  it('prefers the focused window when it is among the ready candidates', () => {
    const a = new FakeWin('a')
    const b = new FakeWin('b')
    expect(pickDeeplinkTarget([a, b], b)).toBe(b)
  })

  it('falls back to the first ready window when focused is not a candidate', () => {
    const a = new FakeWin('a')
    const b = new FakeWin('b')
    const other = new FakeWin('other')
    expect(pickDeeplinkTarget([a, b], other)).toBe(a)
  })

  it('falls back to the first ready window when nothing is focused', () => {
    const a = new FakeWin('a')
    const b = new FakeWin('b')
    expect(pickDeeplinkTarget([a, b], null)).toBe(a)
  })

  it('ignores destroyed windows', () => {
    const dead = new FakeWin('dead', true)
    const live = new FakeWin('live')
    expect(pickDeeplinkTarget([dead, live], null)).toBe(live)
  })

  it('ignores a destroyed focused window and falls back', () => {
    const a = new FakeWin('a')
    const focused = new FakeWin('focused', true)
    expect(pickDeeplinkTarget([a, focused], focused)).toBe(a)
  })

  it('never selects more than one window (single delivery)', () => {
    const wins = [new FakeWin('a'), new FakeWin('b'), new FakeWin('c')]
    const target = pickDeeplinkTarget(wins, null)
    expect(wins.filter((w) => w === target)).toHaveLength(1)
  })
})

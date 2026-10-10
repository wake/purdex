import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { StatusRow, type StatusRowProps } from './StatusRow'

const NOW = 1_800_000_000_000
const MIN = 60_000
const base: StatusRowProps = {
  state: 'idle', model: 'Opus 5.5 (1M)', effort: 'xhigh',
  contextUsed: 38, contextWindowTokens: 1_000_000,
  fiveHour: { pct: 66, resetsAtMs: NOW + 133 * MIN },
  sevenDay: { pct: 39, resetsAtMs: NOW + (3 * 24 + 4) * 60 * MIN },
  cost: 0.42, now: NOW,
}
const r = (over: Partial<StatusRowProps> = {}) => render(<StatusRow {...base} {...over} />)
const arc = (id: string) => screen.getByTestId(id).querySelector('[data-testid="usage-ring-arc"]')!

afterEach(cleanup)

describe('StatusRow content', () => {
  it('prints state · model · effort · context · 5h · 7d · cost with the Mac number semantics', () => {
    r()
    expect(screen.getByTestId('state-slot').dataset.state).toBe('idle')
    expect(screen.getByTestId('item-model').textContent).toBe('Opus 5.5 (1M) · xhigh')
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('62%') // number = LEFT
    expect(screen.getByTestId('ctx-tokens').textContent).toBe('620K left')
    expect(arc('status-seg-usage-context').getAttribute('data-shown')).toBe('38') // ring = USED
    expect(screen.getByTestId('status-seg-usage-five-hour').textContent).toBe('34%') // used 66 -> 34 left
    expect(arc('status-seg-usage-five-hour').getAttribute('data-shown')).toBe('34')
    expect(screen.getByTestId('five_hour-reset').textContent).toBe('↺2h')
    expect(screen.getByTestId('seven_day-reset').textContent).toBe('↺3d')
    expect(screen.getByTestId('item-cost').textContent).toBe('$0.42')
  })

  it('93 % of the context used -> red ring and 「7%」', () => {
    r({ contextUsed: 93 })
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('7%')
    const a = arc('status-seg-usage-context')
    expect(a.getAttribute('data-shown')).toBe('93')
    expect(a.getAttribute('data-tone')).toBe('danger')
  })

  it('every item is a tooltip with its numbers, and none is a button', () => {
    const { container } = r()
    expect(screen.getByTestId('item-context').title).toBe('Context 38% used, 62% left (380K / 1M)')
    expect(screen.getByTestId('item-five_hour').title).toBe('5-hour limit: 34% left，resets in 2h13m')
    expect(screen.getByTestId('item-seven_day').title).toContain('7-day limit: 61% left')
    expect(screen.getByTestId('item-seven_day').title).toContain('resets in 3d4h')
    expect(screen.getByTestId('item-cost').title).toBe("This session's cost so far $0.42")
    expect(screen.getByTestId('item-model').title).toBe('Opus 5.5 (1M) · xhigh')
    expect(container.querySelector('button')).toBeNull()
  })

  it('missing values read 「—」 and are not 0 %', () => {
    r({ contextUsed: null, fiveHour: null, sevenDay: null, cost: null })
    for (const id of ['status-seg-usage-context', 'status-seg-usage-five-hour', 'status-seg-usage-seven-day']) {
      expect(screen.getByTestId(id).textContent).toBe('—')
    }
    expect(screen.getByTestId('item-cost').textContent).toBe('—')
    expect(screen.queryByTestId('ctx-tokens')).toBeNull()
    expect(screen.getByTestId('item-five_hour').title).toBe('5-hour limit: no data')
  })

  it('0 used is 100 % left, distinct from missing', () => {
    r({ contextUsed: 0, fiveHour: { pct: 0, resetsAtMs: null } })
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('100%')
    expect(screen.getByTestId('status-seg-usage-five-hour').textContent).toBe('100%')
    expect(screen.queryByTestId('five_hour-reset')).toBeNull()
  })

  it('dims the usage items when the snapshot is over 10 minutes old', () => {
    r({ usageAt: NOW - 11 * MIN })
    expect(screen.getByTestId('status-seg-usage-context').dataset.dim).toBe('true')
    cleanup()
    r({ usageAt: NOW - 9 * MIN })
    expect(screen.getByTestId('status-seg-usage-context').dataset.dim).toBeUndefined()
  })
})

describe('StatusRow bad numbers', () => {
  const ctxArc = () => screen.getByTestId('status-seg-usage-context').querySelector('[data-testid="usage-ring-arc"]')
  it.each([[NaN], [Infinity], [-Infinity]])('a non-finite used share (%s) is a missing value: 「—」, no ring, no NaN anywhere', (bad) => {
    const { container } = r({ contextUsed: bad, fiveHour: { pct: bad, resetsAtMs: NOW + MIN }, sevenDay: { pct: bad, resetsAtMs: null }, cost: bad })
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('—')
    expect(screen.getByTestId('status-seg-usage-five-hour').textContent).toBe('—')
    expect(screen.getByTestId('status-seg-usage-seven-day').textContent).toBe('—')
    expect(screen.getByTestId('item-cost').textContent).toBe('—')
    expect(screen.queryByTestId('ctx-tokens')).toBeNull()
    expect(screen.queryByTestId('five_hour-reset')).toBeNull()
    expect(container.innerHTML).not.toMatch(/NaN|Infinity/)
  })
  it('out-of-range and fractional shares are clamped / rounded the same way in ring, number, tone, tooltip and tokens', () => {
    r({ contextUsed: 150 })
    expect(ctxArc()!.getAttribute('data-shown')).toBe('100')
    expect(ctxArc()!.getAttribute('data-tone')).toBe('danger')
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('0%')
    expect(screen.getByTestId('ctx-tokens').textContent).toBe('0 left')
    expect(screen.getByTestId('item-context').title).toBe('Context 100% used, 0% left (1M / 1M)')
    cleanup()
    r({ contextUsed: -5 })
    expect(ctxArc()!.getAttribute('data-shown')).toBe('0')
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('100%')
    expect(screen.getByTestId('ctx-tokens').textContent).toBe('1M left')
    cleanup()
    r({ contextUsed: 37.6 })
    expect(ctxArc()!.getAttribute('data-shown')).toBe('38')
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('62%')
    expect(screen.getByTestId('ctx-tokens').textContent).toBe('620K left')
    expect(screen.getByTestId('item-context').title).toBe('Context 38% used, 62% left (380K / 1M)')
  })
  it('a bad window size drops the token text but keeps the percentage', () => {
    for (const bad of [NaN, Infinity, 0, -1]) {
      cleanup()
      const { container } = r({ contextWindowTokens: bad })
      expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('62%')
      expect(screen.queryByTestId('ctx-tokens')).toBeNull()
      expect(container.innerHTML).not.toMatch(/NaN|Infinity/)
    }
  })
  it('a bad reset time or elapsed time never prints NaN', () => {
    const { container } = r({ fiveHour: { pct: 10, resetsAtMs: NaN }, state: 'running', elapsedMs: NaN })
    expect(screen.queryByTestId('five_hour-reset')).toBeNull()
    expect(container.innerHTML).not.toMatch(/NaN|Infinity/)
  })
})

describe('StatusRow narrowing (container-query classes; real layout is checked in Chromium)', () => {
  const has = (el: Element, cls: string) => el.className.split(/\s+/).includes(cls)
  it('the row is its own @container', () => {
    r()
    expect(has(screen.getByTestId('status-row'), '@container')).toBe(true)
  })
  it('each part drops at its own width, in the order cost → 7d → (1M) → tokens → reset → effort → 5h', () => {
    r()
    expect(has(screen.getByTestId('cost-group'), '@max-[640px]:hidden')).toBe(true)
    expect(has(screen.getByTestId('item-seven_day'), '@max-[560px]:hidden')).toBe(true)
    expect(has(screen.getByTestId('model-window'), '@max-[520px]:hidden')).toBe(true)
    expect(has(screen.getByTestId('ctx-tokens'), '@max-[500px]:hidden')).toBe(true)
    expect(has(screen.getByTestId('five_hour-reset'), '@max-[460px]:hidden')).toBe(true)
    expect(has(screen.getByTestId('model-effort'), '@max-[400px]:hidden')).toBe(true)
    expect(has(screen.getByTestId('item-five_hour'), '@max-[330px]:hidden')).toBe(true)
  })
  it('at 412 wide only 5h-and-below thresholds apply: context and 5h carry no class that hides them', () => {
    r()
    const widths = (el: Element) => Array.from(el.classList).filter((c) => c.startsWith('@max-[')).map((c) => Number(/\[(\d+)px\]/.exec(c)![1]))
    for (const id of ['item-context', 'item-model', 'item-five_hour', 'state-slot']) {
      expect(widths(screen.getByTestId(id)).every((w) => w < 412)).toBe(true)
    }
    expect(screen.getByTestId('item-context').className).not.toContain('@max')
    expect(screen.getByTestId('state-slot').className).not.toContain('@max')
  })
})

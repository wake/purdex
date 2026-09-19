// spa/src/components/execution/CostPanel.test.tsx
// P-B4 spec §4.3 P1–P4 / P6, plan Task 4. Expected numbers are the ones
// cost-summary.test pins on the 12-turn fixture, pushed through the
// formatters (§4.4) — the panel must render exactly what the rollup says.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, within, cleanup } from '@testing-library/react'
import CostPanel from './CostPanel'
import { costSummary, type CostSummary, type TurnCost } from '../../lib/nex/cost-summary'
import { formatTokens } from '../../lib/nex/format-cost'
import type { StreamMessage } from '../../lib/nex/message-types'
import turnsFixture from '../../lib/nex/__fixtures__/cost-turns-06GB2ZFD.json'

interface FixtureItem { seq: number; kind: string; payload: unknown }
/** What the reducer pushes: every non-lifecycle payload, in seq order (same as cost-summary.test). */
const fixturePayloads = (turnsFixture as { items: FixtureItem[] }).items
  .filter((i) => !i.kind.startsWith('execution.') && !i.kind.startsWith('lease.'))
  .map((i) => i.payload as StreamMessage)

const fixtureSummary = costSummary(fixturePayloads)

/** The fixture with the `modelUsage` of the first costed `result` removed → one unsplit turn. */
function unsplitPayloads(): StreamMessage[] {
  let done = false
  return fixturePayloads.map((m) => {
    const p = m as unknown as Record<string, unknown>
    if (done || p.type !== 'result' || p.parent_tool_use_id != null || !(typeof p.total_cost_usd === 'number' && p.total_cost_usd > 0)) return m
    done = true
    const copy = { ...p }
    delete copy.modelUsage
    return copy as unknown as StreamMessage
  })
}

function renderPanel(summary: CostSummary, onClose = vi.fn()) {
  const anchor = document.createElement('button')
  document.body.appendChild(anchor)
  const anchorRef = { current: anchor }
  const utils = render(<CostPanel summary={summary} hostId="h1" anchorRef={anchorRef} onClose={onClose} />)
  return { ...utils, anchor, onClose }
}

afterEach(() => {
  cleanup()
  document.body.innerHTML = ''
})

describe('CostPanel', () => {
  it('renders inside a FloatingPanel with testId cost-panel and the Cost title', () => {
    renderPanel(fixtureSummary)
    const panel = screen.getByTestId('cost-panel')
    expect(panel.getAttribute('aria-label')).toBe('Cost')
  })

  // P1
  it('totals line: $ total (4 dp) · turns · API rounds · API / wall', () => {
    renderPanel(fixtureSummary)
    const totals = screen.getByTestId('cost-totals')
    expect(totals.textContent).toContain('$0.2577')
    expect(totals.textContent).toContain('12 turns')
    expect(totals.textContent).toContain('22 API rounds')
    expect(totals.textContent).toContain('1m 05s API / 1m 33s wall')
  })

  // P2
  it('tokens row: four cells with formatTokens values, tabular-nums', () => {
    renderPanel(fixtureSummary)
    const row = screen.getByTestId('cost-tokens')
    expect(row.className).toMatch(/\btabular-nums\b/)
    const { output, input, cacheRead, cacheWrite } = fixtureSummary.tokens
    expect(formatTokens(output)).toBe('4.5k')
    const cell = (key: string) => within(row).getByTestId(`cost-tokens-${key}`)
    const expectCell = (key: string, label: string, value: number) => {
      const c = cell(key)
      expect(within(c).getByText(label)).toBeInTheDocument()
      expect(within(c).getByText(formatTokens(value))).toBeInTheDocument()
    }
    expectCell('output', 'output', output)
    expectCell('input', 'input', input)
    expectCell('cache_read', 'cache read', cacheRead)
    expectCell('cache_write', 'cache write', cacheWrite)
    // Cell order is fixed: output · input · cache read · cache write.
    expect(Array.from(row.children).map((c) => c.getAttribute('data-testid')))
      .toEqual(['cost-tokens-output', 'cost-tokens-input', 'cost-tokens-cache_read', 'cost-tokens-cache_write'])
  })

  it('tokens row is hidden when no turn had tokens', () => {
    const s: CostSummary = { ...fixtureSummary, turns: fixtureSummary.turns.map((t) => ({ ...t, tokens: null })) }
    renderPanel(s)
    expect(screen.queryByTestId('cost-tokens')).toBeNull()
  })

  // P3
  it('models list: sonnet first, haiku row shows $0.0010 and its out tokens; no note when every turn is split', () => {
    renderPanel(fixtureSummary)
    const models = screen.getByTestId('cost-models')
    const rows = within(models).getAllByTestId('cost-model')
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toContain('claude-sonnet-5')
    expect(rows[1].textContent).toContain('claude-haiku-4-5')
    expect(rows[1].textContent).toContain('$0.0010')
    expect(rows[1].textContent).toContain('12 out')
    expect(screen.queryByTestId('cost-models-note')).toBeNull()
  })

  it('models note appears with n when a costed turn has no per-model split', () => {
    const s = costSummary(unsplitPayloads())
    expect(s.unsplitTurns).toBe(1)
    renderPanel(s)
    const note = screen.getByTestId('cost-models-note')
    expect(note.textContent).toBe('1 turns without a per-model split')
  })

  it('models section is hidden when models is empty and nothing is unsplit', () => {
    const s: CostSummary = { ...fixtureSummary, models: [], unsplitTurns: 0 }
    renderPanel(s)
    expect(screen.queryByTestId('cost-models')).toBeNull()
  })

  it('models section still renders (note only) when models is empty but a turn is unsplit', () => {
    const s: CostSummary = { ...fixtureSummary, models: [], unsplitTurns: 2 }
    renderPanel(s)
    expect(screen.getByTestId('cost-models')).toBeInTheDocument()
    expect(screen.getByTestId('cost-models-note').textContent).toBe('2 turns without a per-model split')
  })

  // P4
  it('turns table: 12 rows in order, error rows 6 and 10 with subtype title, row 8 shows $0.0000', () => {
    renderPanel(fixtureSummary)
    const table = screen.getByTestId('cost-turns')
    const rows = within(table).getAllByTestId('cost-turn')
    expect(rows).toHaveLength(12)
    rows.forEach((row, i) => expect(row.textContent).toContain(`#${i + 1}`))
    const errorRows = rows.map((r, i) => (within(r).queryByTestId('turn-error') ? i + 1 : null)).filter((x) => x !== null)
    expect(errorRows).toEqual([6, 10])
    const err6 = within(rows[5]).getByTestId('turn-error')
    expect(err6.getAttribute('title')).toBe('error_during_execution')
    expect(err6.textContent).toBe('error')
    expect(err6.className).toMatch(/\btext-status-error\b/)
    expect(rows[7].textContent).toContain('$0.0000')
    // Row 1: values straight from the rollup.
    const t1 = fixtureSummary.turns[0]
    expect(rows[0].textContent).toContain('$0.0300')
    expect(rows[0].textContent).toContain(formatTokens(t1.tokens!.output))
    expect(rows[0].textContent).toContain(formatTokens(t1.tokens!.cacheRead))
  })

  it('a turn with tokens: null shows — in the token cells; null timings/rounds render 0.0s / —', () => {
    const nullTurn: TurnCost = {
      index: 13, costUsd: 0.5, tokens: null, durationMs: null, apiMs: null, rounds: null, subtype: '', isError: false, models: [],
    }
    const s: CostSummary = { ...fixtureSummary, turns: [...fixtureSummary.turns, nullTurn] }
    renderPanel(s)
    const rows = within(screen.getByTestId('cost-turns')).getAllByTestId('cost-turn')
    expect(rows).toHaveLength(13)
    const last = rows[12]
    expect(within(last).getByTestId('turn-out').textContent).toBe('—')
    expect(within(last).getByTestId('turn-cache-read').textContent).toBe('—')
    expect(within(last).getByTestId('turn-timing').textContent).toBe('0.0s / 0.0s')
    expect(within(last).getByTestId('turn-rounds').textContent).toBe('—')
    expect(within(last).queryByTestId('turn-error')).toBeNull()
  })

  it('turns wrapper is max-h-64 overflow-auto and scrolls to the bottom on mount', () => {
    const scrollTopSetter = vi.fn()
    const heightDesc = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight')
    const topDesc = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTop')
    // jsdom's `scrollHeight` / `scrollTop` live on Element.prototype (always 0);
    // shadow them on HTMLElement.prototype for this test and restore after.
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get: () => 999 })
    Object.defineProperty(HTMLElement.prototype, 'scrollTop', { configurable: true, set: scrollTopSetter, get: () => 0 })
    try {
      renderPanel(fixtureSummary)
      const table = screen.getByTestId('cost-turns')
      expect(table.className).toMatch(/\bmax-h-64\b/)
      expect(table.className).toMatch(/\boverflow-auto\b/)
      expect(scrollTopSetter).toHaveBeenCalledWith(999)
    } finally {
      if (heightDesc) Object.defineProperty(HTMLElement.prototype, 'scrollHeight', heightDesc)
      else delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollHeight
      if (topDesc) Object.defineProperty(HTMLElement.prototype, 'scrollTop', topDesc)
      else delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollTop
    }
  })

  // P6
  it('re-renders from props: a new summary with one more turn adds a row', () => {
    const { rerender, anchor } = renderPanel(fixtureSummary)
    expect(within(screen.getByTestId('cost-turns')).getAllByTestId('cost-turn')).toHaveLength(12)
    const extra: TurnCost = { ...fixtureSummary.turns[11], index: 13 }
    const s: CostSummary = { ...fixtureSummary, turns: [...fixtureSummary.turns, extra] }
    rerender(<CostPanel summary={s} hostId="h1" anchorRef={{ current: anchor }} onClose={() => {}} />)
    expect(within(screen.getByTestId('cost-turns')).getAllByTestId('cost-turn')).toHaveLength(13)
  })
})

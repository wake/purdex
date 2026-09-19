// spa/src/components/execution/CostPanel.test.tsx
// P-B4 spec §4.3 P1–P4 / P6, plan Task 4. Expected numbers are the ones
// cost-summary.test pins on the 12-turn fixture, pushed through the
// formatters (§4.4) — the panel must render exactly what the rollup says.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, within, cleanup, act, waitFor } from '@testing-library/react'
import CostPanel from './CostPanel'
import { costSummary, type CostSummary, type TurnCost } from '../../lib/nex/cost-summary'
import { formatTokens } from '../../lib/nex/format-cost'
import type { StreamMessage } from '../../lib/nex/message-types'
import turnsFixture from '../../lib/nex/__fixtures__/cost-turns-06GB2ZFD.json'
import type { NexHostInfo } from '../../lib/nex/types'

// P5: the quota row fetches the host card; every other export stays real.
vi.mock('../../lib/nex/nex-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, fetchNexHost: vi.fn() }
})

import * as nexApi from '../../lib/nex/nex-api'

const mockFetchNexHost = vi.mocked(nexApi.fetchNexHost)

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

beforeEach(() => {
  mockFetchNexHost.mockReset()
  mockFetchNexHost.mockResolvedValue({ active_account: '', quota: null })
})

afterEach(() => {
  cleanup()
  document.body.innerHTML = ''
})

describe('CostPanel', () => {
  it('renders inside a FloatingPanel with testId cost-panel and the Cost title', () => {
    renderPanel(fixtureSummary)
    const panel = screen.getByTestId('cost-panel')
    expect(panel.getAttribute('aria-label')).toBe('Cost')
    expect(panel.style.width).toBe('440px')
  })

  it('width is bounded by the viewport: innerWidth 320 → 304px (innerWidth − 16), 100 → 84px, and follows resize while open', () => {
    const desc = Object.getOwnPropertyDescriptor(window, 'innerWidth')
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 320 })
    try {
      renderPanel(fixtureSummary)
      expect(screen.getByTestId('cost-panel').style.width).toBe('304px')
      cleanup()
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: 100 })
      renderPanel(fixtureSummary)
      expect(screen.getByTestId('cost-panel').style.width).toBe('84px')
      // Resize while open: the width is state, not a render-time read.
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1200 })
      act(() => { window.dispatchEvent(new Event('resize')) })
      expect(screen.getByTestId('cost-panel').style.width).toBe('440px')
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: 300 })
      act(() => { window.dispatchEvent(new Event('resize')) })
      expect(screen.getByTestId('cost-panel').style.width).toBe('284px')
    } finally {
      if (desc) Object.defineProperty(window, 'innerWidth', desc)
      else delete (window as unknown as Record<string, unknown>).innerWidth
    }
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

  // P5
  describe('quota row', () => {
    const withQuota: NexHostInfo = {
      active_account: 'wake@x',
      quota: { five_hour_pct: 34.4, seven_day_pct: 12, resets_at: 0, source: 'usage_api' },
    }

    it('quota: null → no cost-quota row after the fetch settles', async () => {
      mockFetchNexHost.mockResolvedValue({ active_account: 'wake@x', quota: null })
      renderPanel(fixtureSummary)
      await waitFor(() => expect(mockFetchNexHost).toHaveBeenCalledTimes(1))
      await act(async () => { await Promise.resolve() })
      expect(screen.queryByTestId('cost-quota')).toBeNull()
    })

    it('renders the host account label and 5h / 7d / source with the raw API percentages (no rounding)', async () => {
      mockFetchNexHost.mockResolvedValue(withQuota)
      renderPanel(fixtureSummary)
      const row = await screen.findByTestId('cost-quota')
      expect(row.textContent).toContain('Host quota — wake@x')
      expect(row.textContent).toContain('5h 34.4% · 7d 12% · usage_api')
      expect(mockFetchNexHost).toHaveBeenCalledWith('h1')
    })

    it('percentages are not rounded: 0.4 stays 0.4%, 99.6 stays 99.6%', async () => {
      mockFetchNexHost.mockResolvedValue({ ...withQuota, quota: { ...withQuota.quota!, five_hour_pct: 0.4, seven_day_pct: 99.6 } })
      renderPanel(fixtureSummary)
      const row = await screen.findByTestId('cost-quota')
      expect(row.textContent).toContain('5h 0.4% · 7d 99.6% · usage_api')
    })

    it('a non-finite percentage hides the quota row', async () => {
      mockFetchNexHost.mockResolvedValue({ ...withQuota, quota: { ...withQuota.quota!, five_hour_pct: Number.NaN } })
      renderPanel(fixtureSummary)
      await waitFor(() => expect(mockFetchNexHost).toHaveBeenCalledTimes(1))
      await act(async () => { await Promise.resolve() })
      expect(screen.queryByTestId('cost-quota')).toBeNull()
    })

    it('fetch rejection → no row, nothing logged', async () => {
      const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
      try {
        mockFetchNexHost.mockRejectedValue(new Error('503'))
        renderPanel(fixtureSummary)
        await waitFor(() => expect(mockFetchNexHost).toHaveBeenCalledTimes(1))
        await act(async () => { await Promise.resolve() })
        expect(screen.queryByTestId('cost-quota')).toBeNull()
        expect(errSpy).not.toHaveBeenCalled()
        expect(warnSpy).not.toHaveBeenCalled()
      } finally {
        errSpy.mockRestore()
        warnSpy.mockRestore()
      }
    })

    it('a response arriving after unmount is ignored (cancelled flag, not an abort)', async () => {
      const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
      try {
        let resolve!: (h: NexHostInfo) => void
        mockFetchNexHost.mockReturnValue(new Promise<NexHostInfo>((r) => { resolve = r }))
        const { unmount } = renderPanel(fixtureSummary)
        expect(mockFetchNexHost).toHaveBeenCalledTimes(1)
        unmount()
        // Resolve outside `act` on purpose — the request was never aborted
        // (no signal), only its result is dropped. React 19 no longer warns
        // on a post-unmount setState, so this case only proves the late
        // response neither throws nor logs; the host-switch case below is
        // the one that observes the flag through the DOM.
        resolve(withQuota)
        await Promise.resolve()
        await Promise.resolve()
        expect(screen.queryByTestId('cost-quota')).toBeNull()
        expect(errSpy).not.toHaveBeenCalled()
      } finally {
        errSpy.mockRestore()
      }
    })

    it('a stale response from the previous hostId cannot overwrite the current host (cancelled flag)', async () => {
      let resolveH1!: (h: NexHostInfo) => void
      mockFetchNexHost.mockImplementation((hostId: string) => hostId === 'h1'
        ? new Promise<NexHostInfo>((r) => { resolveH1 = r })
        : Promise.resolve({ active_account: 'other@x', quota: null }))
      const { rerender, anchor } = renderPanel(fixtureSummary)
      expect(mockFetchNexHost).toHaveBeenLastCalledWith('h1')
      rerender(<CostPanel summary={fixtureSummary} hostId="h2" anchorRef={{ current: anchor }} onClose={() => {}} />)
      await waitFor(() => expect(mockFetchNexHost).toHaveBeenLastCalledWith('h2'))
      await act(async () => { await Promise.resolve() })
      // h1's answer lands after h2 already settled (quota: null → no row).
      await act(async () => { resolveH1(withQuota); await Promise.resolve() })
      expect(screen.queryByTestId('cost-quota')).toBeNull()
    })

    it('switching hostId clears the previous host quota immediately; h2 rejecting leaves it cleared', async () => {
      let rejectH2!: (e: Error) => void
      mockFetchNexHost.mockImplementation((hostId: string) => hostId === 'h1'
        ? Promise.resolve(withQuota)
        : new Promise<NexHostInfo>((_r, rej) => { rejectH2 = rej }))
      const { rerender, anchor } = renderPanel(fixtureSummary)
      await screen.findByTestId('cost-quota')
      rerender(<CostPanel summary={fixtureSummary} hostId="h2" anchorRef={{ current: anchor }} onClose={() => {}} />)
      // Synchronous: h2 is still pending, yet h1's row must already be gone.
      expect(screen.queryByTestId('cost-quota')).toBeNull()
      expect(mockFetchNexHost).toHaveBeenLastCalledWith('h2')
      await act(async () => { rejectH2(new Error('503')); await Promise.resolve() })
      expect(screen.queryByTestId('cost-quota')).toBeNull()
    })

    it('switching hostId to a host with quota: null leaves no row', async () => {
      mockFetchNexHost.mockImplementation((hostId: string) => Promise.resolve(hostId === 'h1'
        ? withQuota
        : { active_account: 'other@x', quota: null }))
      const { rerender, anchor } = renderPanel(fixtureSummary)
      await screen.findByTestId('cost-quota')
      rerender(<CostPanel summary={fixtureSummary} hostId="h2" anchorRef={{ current: anchor }} onClose={() => {}} />)
      expect(screen.queryByTestId('cost-quota')).toBeNull()
      await waitFor(() => expect(mockFetchNexHost).toHaveBeenLastCalledWith('h2'))
      await act(async () => { await Promise.resolve() })
      expect(screen.queryByTestId('cost-quota')).toBeNull()
    })

    it('fetches once per hostId: new summary objects do not refetch, a new hostId does', async () => {
      mockFetchNexHost.mockResolvedValue(withQuota)
      const { rerender, anchor } = renderPanel(fixtureSummary)
      await screen.findByTestId('cost-quota')
      expect(mockFetchNexHost).toHaveBeenCalledTimes(1)
      rerender(<CostPanel summary={{ ...fixtureSummary }} hostId="h1" anchorRef={{ current: anchor }} onClose={() => {}} />)
      rerender(<CostPanel summary={{ ...fixtureSummary }} hostId="h1" anchorRef={{ current: anchor }} onClose={() => {}} />)
      expect(mockFetchNexHost).toHaveBeenCalledTimes(1)
      rerender(<CostPanel summary={fixtureSummary} hostId="h2" anchorRef={{ current: anchor }} onClose={() => {}} />)
      await waitFor(() => expect(mockFetchNexHost).toHaveBeenCalledTimes(2))
      expect(mockFetchNexHost).toHaveBeenLastCalledWith('h2')
    })
  })
})

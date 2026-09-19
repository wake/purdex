import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import ExecutionHeader from './ExecutionHeader'
import type { ExecutionSummary } from '../../lib/nex/types'
import { costSummary } from '../../lib/nex/cost-summary'
import type { StreamMessage } from '../../lib/nex/message-types'
import turnsFixture from '../../lib/nex/__fixtures__/cost-turns-06GB2ZFD.json'

interface FixtureItem { seq: number; kind: string; payload: unknown }
/** What the reducer pushes: every non-lifecycle payload, in seq order (same as cost-summary.test). */
const fixturePayloads = (turnsFixture as { items: FixtureItem[] }).items
  .filter((i) => !i.kind.startsWith('execution.') && !i.kind.startsWith('lease.'))
  .map((i) => i.payload as StreamMessage)

const summary = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
  id: 'exc_1',
  state: 'idle',
  provider: 'claude',
  principal_id: 'p',
  cwd: '/Users/w/repo',
  mount_kind: 'dev',
  brief: 'b',
  labels: {},
  created_at: 0,
  updated_at: 0,
  duration_ms: null,
  event_count: 0,
  observers: 2,
  archived: false,
  effective_profile: 'standard',
  turn_count: 3,
  ...extra,
})

const baseProps = {
  cost: costSummary([]),
  hostId: 'h1',
  sse: 'open' as const,
  isMine: () => false,
  onInterrupt: vi.fn(),
  onTerminate: vi.fn(),
  busy: false,
}

describe('ExecutionHeader', () => {
  it('renders state dot text, profile, cwd basename, observers, and turns', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} />)
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    expect(screen.getByText(/standard/)).toBeInTheDocument()
    expect(screen.getByText('repo')).toBeInTheDocument()
    expect(screen.getByText(/2 observers/i)).toBeInTheDocument()
    expect(screen.getByText(/3 turns/i)).toBeInTheDocument()
  })

  it('renders lease line: "(you)" when isMine, raw principal otherwise, "no lease" when absent', () => {
    const { rerender } = render(
      <ExecutionHeader {...baseProps} summary={summary({ lease: { principal_id: 'pdx:mlab/t-me', expires_at: 1 } })} isMine={() => true} />,
    )
    expect(screen.getByTestId('execution-lease')).toHaveTextContent('(you)')

    rerender(
      <ExecutionHeader {...baseProps} summary={summary({ lease: { principal_id: 'pdx:mlab/t-other', expires_at: 1 } })} isMine={() => false} />,
    )
    expect(screen.getByTestId('execution-lease')).toHaveTextContent('pdx:mlab/t-other')

    rerender(<ExecutionHeader {...baseProps} summary={summary()} isMine={() => false} />)
    expect(screen.getByTestId('execution-lease')).toHaveTextContent(/no lease/i)
  })

  it('SSE badge text follows sse', () => {
    const { rerender } = render(<ExecutionHeader {...baseProps} summary={summary()} sse="idle" />)
    expect(screen.getByTestId('execution-sse')).toHaveTextContent(/connecting/i)

    rerender(<ExecutionHeader {...baseProps} summary={summary()} sse="open" />)
    expect(screen.getByTestId('execution-sse')).toHaveTextContent(/live/i)

    rerender(<ExecutionHeader {...baseProps} summary={summary()} sse="paused" />)
    expect(screen.getByTestId('execution-sse')).toHaveTextContent(/paused/i)
  })

  // P-C.3b task 4 / exec-to-terminal spec §4.2: "Take to terminal" exists
  // only when the view passes `onTakeBack` (it decides from `from` / summary).
  it('renders no take-back control without onTakeBack', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} />)
    expect(screen.queryByTestId('take-back')).toBeNull()
  })

  it('renders the take-back control with onTakeBack, labelled and clickable', () => {
    const onTakeBack = vi.fn()
    render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={onTakeBack} />)
    const btn = screen.getByTestId('take-back') as HTMLButtonElement
    expect(btn).toHaveTextContent(/take to terminal/i)
    expect(btn.disabled).toBe(false)
    fireEvent.click(btn)
    expect(onTakeBack).toHaveBeenCalledTimes(1)
  })

  it('disables the take-back control while takeBackBusy, independent of `busy`', () => {
    const onTakeBack = vi.fn()
    const { rerender } = render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={onTakeBack} takeBackBusy />)
    const btn = screen.getByTestId('take-back') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    fireEvent.click(btn)
    expect(onTakeBack).not.toHaveBeenCalled()
    // `busy` (terminal execution) gates interrupt/terminate, not take-back:
    // an ended execution can still go back to its terminal.
    rerender(<ExecutionHeader {...baseProps} summary={summary({ state: 'failed' })} busy onTakeBack={onTakeBack} />)
    expect((screen.getByTestId('take-back') as HTMLButtonElement).disabled).toBe(false)
  })

  // P-B4 spec §4.2 H1–H2: the cost is an anchor button with a hover summary.
  describe('cost anchor (P-B4 H1–H2)', () => {
    afterEach(() => { vi.useRealTimers() })
    const costBtn = () => screen.getByTestId('execution-cost') as HTMLButtonElement

    it('renders $0.00 for an empty summary', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} />)
      expect(costBtn()).toHaveTextContent('$0.00')
    })

    it('cost=null → disabled `$…` with no tooltip', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={null} />)
      const btn = costBtn()
      expect(btn.disabled).toBe(true)
      expect(btn).toHaveTextContent('$…')
      expect(screen.queryByRole('tooltip')).toBeNull()
    })

    it('cost from the 12-turn fixture → enabled `$0.26`', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      const btn = costBtn()
      expect(btn.disabled).toBe(false)
      expect(btn.textContent).toBe('$0.26')
      // H3: an enabled anchor is a toggle; closed by default.
      expect(btn.getAttribute('aria-expanded')).toBe('false')
    })

    it('hover 800 ms → one-line summary tooltip', () => {
      vi.useFakeTimers()
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      const tip = screen.getByRole('tooltip')
      expect(tip.textContent).toBe('12 turns · $0.2577 · 4.5k out · 1m 05s API / 1m 33s wall')
      expect(tip.className).toMatch(/\bopacity-0\b/)
      fireEvent.mouseEnter(costBtn())
      act(() => vi.advanceTimersByTime(799))
      expect(tip.className).toMatch(/\bopacity-0\b/)
      act(() => vi.advanceTimersByTime(1))
      expect(tip.className).toMatch(/\bopacity-100\b/)
    })

    // Codex R2 A1: header and tooltip share formatUsd — a MAX_VALUE cost never renders 'Infinity'.
    it('two MAX_VALUE costs → header text starts with $ and never contains Infinity; tooltip agrees', () => {
      const big = { type: 'result', total_cost_usd: Number.MAX_VALUE } as StreamMessage
      const cost = costSummary([big, big])
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={cost} />)
      const btn = costBtn()
      expect(btn.textContent?.startsWith('$')).toBe(true)
      expect(btn.textContent).not.toContain('Infinity')
      expect(btn.textContent).not.toContain('NaN')
      const tip = screen.getByRole('tooltip')
      expect(tip.textContent).not.toContain('Infinity')
      expect(tip.textContent).toContain('$')
    })

    // Codex R2 A4: the anchor is described by its tooltip.
    it('with cost → button aria-describedby equals the tooltip id; cost=null → no aria-describedby', () => {
      const { rerender } = render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      const tip = screen.getByRole('tooltip')
      expect(tip.id).not.toBe('')
      expect(costBtn().getAttribute('aria-describedby')).toBe(tip.id)
      rerender(<ExecutionHeader {...baseProps} summary={summary()} cost={null} />)
      expect(costBtn().hasAttribute('aria-describedby')).toBe(false)
    })

    it('leaving before 800 ms → tooltip never shows', () => {
      vi.useFakeTimers()
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      const tip = screen.getByRole('tooltip')
      fireEvent.mouseEnter(costBtn())
      act(() => vi.advanceTimersByTime(400))
      fireEvent.mouseLeave(costBtn())
      act(() => vi.advanceTimersByTime(800))
      expect(tip.className).toMatch(/\bopacity-0\b/)
    })
  })

  // P-B4 spec §4.2 H3: click toggles the CostPanel; FloatingPanel's outside-click
  // and Escape rules are wired through the anchor ref.
  describe('cost panel toggle (P-B4 H3)', () => {
    const costBtn = () => screen.getByTestId('execution-cost') as HTMLButtonElement

    it('click opens the panel and sets aria-expanded; Escape closes it', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      expect(costBtn().getAttribute('aria-expanded')).toBe('false')
      expect(screen.queryByTestId('cost-panel')).toBeNull()
      fireEvent.click(costBtn())
      expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
      expect(costBtn().getAttribute('aria-expanded')).toBe('true')
      fireEvent.keyDown(document, { key: 'Escape' })
      expect(screen.queryByTestId('cost-panel')).toBeNull()
      expect(costBtn().getAttribute('aria-expanded')).toBe('false')
    })

    it('click twice → open then closed (the anchor\'s own mousedown does not close it first)', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      fireEvent.mouseDown(costBtn())
      fireEvent.click(costBtn())
      expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
      fireEvent.mouseDown(costBtn())
      expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
      fireEvent.click(costBtn())
      expect(screen.queryByTestId('cost-panel')).toBeNull()
      expect(costBtn().getAttribute('aria-expanded')).toBe('false')
    })

    it('mousedown on another header element (interrupt) closes the panel', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      fireEvent.click(costBtn())
      expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
      fireEvent.mouseDown(screen.getByText(/^interrupt$/i))
      expect(screen.queryByTestId('cost-panel')).toBeNull()
      expect(costBtn().getAttribute('aria-expanded')).toBe('false')
    })

    it('the panel receives the header\'s summary and hostId', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      fireEvent.click(costBtn())
      expect(screen.getByTestId('cost-totals').textContent).toContain('$0.2577')
    })

    it('cost=null → no aria-expanded and click does nothing', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={null} />)
      expect(costBtn().hasAttribute('aria-expanded')).toBe(false)
      fireEvent.click(costBtn())
      expect(screen.queryByTestId('cost-panel')).toBeNull()
      expect(costBtn().hasAttribute('aria-expanded')).toBe(false)
    })
  })
})

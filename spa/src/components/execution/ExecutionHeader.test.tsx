import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, fireEvent, act, within } from '@testing-library/react'
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
  onInterrupt: vi.fn(),
  onTerminate: vi.fn(),
  busy: false,
}

describe('ExecutionHeader', () => {
  beforeEach(() => { vi.clearAllMocks() })

  // Worker pane spec §4.7: the header keeps only what you steer by.
  it('shows state, name, cost and the actions', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={vi.fn()} />)
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    const name = screen.getByTestId('worker-name')
    expect(name).toHaveTextContent(/^repo$/)
    expect(name.className).toMatch(/\bfont-medium\b/)
    expect(name.className).toMatch(/\btext-text-primary\b/)
    expect(screen.getByTestId('execution-cost')).toHaveTextContent('$0.00')
    expect(screen.getByRole('button', { name: /^interrupt$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^terminate$/i })).toBeInTheDocument()
    expect(screen.getByTestId('take-back')).toBeInTheDocument()
  })

  // Observers, lease and SSE moved to the dock (§4.6); turns are dropped;
  // provider/profile moved to the worker-info popover. `cost={null}` keeps the
  // cost tooltip ("N turns · …") out of the DOM so the turn check is honest.
  it('does not show observers, lease, sse or turns', () => {
    render(
      <ExecutionHeader {...baseProps} cost={null}
        summary={summary({ lease: { principal_id: 'pdx:mlab/t-me', expires_at: 1 } })} />,
    )
    expect(screen.queryByTestId('execution-sse')).toBeNull()
    expect(screen.queryByTestId('execution-lease')).toBeNull()
    expect(screen.queryByText(/observers/i)).toBeNull()
    expect(screen.queryByText(/\blive\b/i)).toBeNull()
    expect(screen.queryByText(/no lease|\(you\)|t-me/i)).toBeNull()
    expect(screen.queryByText(/turns/i)).toBeNull()
    expect(screen.queryByText(/standard/)).toBeNull()
  })

  // Spec §4.7: provider, profile and cwd live in a popover on the name.
  it('the name toggles the worker-info popover', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} />)
    const name = screen.getByTestId('worker-name')
    expect(name.tagName).toBe('BUTTON')
    expect(name.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(name)
    expect(screen.getByTestId('worker-info-panel')).toBeInTheDocument()
    expect(screen.getByTestId('worker-info-cwd')).toHaveTextContent('/Users/w/repo')
    expect(name.getAttribute('aria-expanded')).toBe('true')
    fireEvent.click(name)
    expect(screen.queryByTestId('worker-info-panel')).toBeNull()
  })

  it('styles terminate as destructive before the first click', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} />)
    const term = screen.getByRole('button', { name: /^terminate$/i })
    expect(term.className).toMatch(/\btext-status-error\b/)
    // The confirm step is unchanged: first click arms, the label changes.
    fireEvent.click(term)
    expect(screen.getByRole('button', { name: /confirm terminate/i }).className).toMatch(/\btext-status-error\b/)
    expect(baseProps.onTerminate).not.toHaveBeenCalled()
  })

  // Spec §1: take-to-terminal changes the pane's binding, it does not interrupt a turn.
  it('separates take-to-terminal from the interrupt actions', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={vi.fn()} />)
    const take = screen.getByTestId('take-back')
    const sep = take.previousElementSibling as HTMLElement | null
    expect(sep).not.toBeNull()
    expect(sep!.tagName).toBe('SPAN')
    expect(sep!.className).toMatch(/\bw-px\b/)
    expect(sep!.className).toMatch(/\bh-4\b/)
    expect(sep!.className).toMatch(/\bbg-border-subtle\b/)
    // The separator sits between the lease-backed actions and take-to-terminal.
    const interrupt = screen.getByRole('button', { name: /^interrupt$/i })
    expect(interrupt.compareDocumentPosition(sep!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  // jsdom evaluates no CSS, let alone container queries, so the narrow
  // layout is asserted structurally: the root is a `@container`, the name
  // truncates, the cost + lease-backed actions hide at `@max-md`, and an
  // overflow trigger (shown only at `@max-md`) opens a FloatingPanel that
  // carries them. The real narrow render is checked by screenshot (T6.4).
  it('renders an overflow trigger for narrow widths', () => {
    const { container } = render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={vi.fn()} />)
    const root = container.firstElementChild as HTMLElement
    expect(root.className).toMatch(/(^|\s)@container(\s|$)/)
    const name = screen.getByTestId('worker-name')
    expect(name.className).toMatch(/\btruncate\b/)
    expect(name.className).toMatch(/\bmin-w-0\b/)
    const wide = screen.getByTestId('header-wide-actions')
    expect(wide.className).toMatch(/@max-md:hidden/)
    expect(wide).toContainElement(screen.getByTestId('execution-cost'))
    expect(wide).toContainElement(screen.getByRole('button', { name: /^interrupt$/i }))
    expect(wide).toContainElement(screen.getByRole('button', { name: /^terminate$/i }))
    expect(wide).not.toContainElement(screen.getByTestId('take-back'))
    const trigger = screen.getByTestId('header-overflow')
    expect(trigger.className).toMatch(/(^|\s)hidden(\s|$)/)
    expect(trigger.className).toMatch(/@max-md:flex/)
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(trigger)
    const menu = screen.getByTestId('header-overflow-panel')
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    expect(within(menu).getByTestId('overflow-cost')).toHaveTextContent('$0.00')
    fireEvent.click(within(menu).getByTestId('overflow-interrupt'))
    expect(baseProps.onInterrupt).toHaveBeenCalledTimes(1)
    // Acting from the menu closes it, like any menu.
    expect(screen.queryByTestId('header-overflow-panel')).toBeNull()
    // Terminate keeps its two-step confirm inside the menu too.
    fireEvent.click(trigger)
    fireEvent.click(within(screen.getByTestId('header-overflow-panel')).getByTestId('overflow-terminate'))
    expect(baseProps.onTerminate).not.toHaveBeenCalled()
    fireEvent.click(within(screen.getByTestId('header-overflow-panel')).getByTestId('overflow-terminate'))
    expect(baseProps.onTerminate).toHaveBeenCalledTimes(1)
  })

  it('the overflow cost entry opens the cost panel', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
    fireEvent.click(screen.getByTestId('header-overflow'))
    fireEvent.click(screen.getByTestId('overflow-cost'))
    expect(screen.queryByTestId('header-overflow-panel')).toBeNull()
    expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
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

    // Spec §3.1.1 #8: the panel repeats the line the tooltip carries, so the
    // tooltip must not linger behind it.
    it('hides the hover summary while the cost panel is open', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      expect(screen.getByRole('tooltip')).toBeInTheDocument()
      fireEvent.click(costBtn())
      expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
      expect(screen.queryByRole('tooltip')).toBeNull()
      expect(costBtn().hasAttribute('aria-describedby')).toBe(false)
      // Closing the panel brings the hover affordance back.
      fireEvent.click(costBtn())
      expect(screen.getByRole('tooltip')).toBeInTheDocument()
      expect(costBtn().getAttribute('aria-describedby')).toBe(screen.getByRole('tooltip').id)
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

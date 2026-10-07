import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { useState } from 'react'
import { render, screen, fireEvent, act, within } from '@testing-library/react'
import ExecutionHeader from './ExecutionHeader'
import type { ExecutionSummary } from '../../lib/nex/types'
import { STATE_DOT_CLASSES } from '../../lib/nex/state-dot'
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
  onExit: vi.fn(),
  exitDisabled: false,
  busy: false,
  onModeChange: vi.fn(),
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
    expect(screen.getByTestId('header-exit')).toBeInTheDocument()
    expect(screen.getByTestId('view-mode')).toBeInTheDocument()
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

  it('offers 退出 (destructive) and calls onExit; no terminate control remains', () => {
    render(<ExecutionHeader {...baseProps} summary={summary({ state: 'idle' })} />)
    const exit = screen.getByTestId('header-exit')
    expect(exit.className).toMatch(/\btext-status-error\b/)
    fireEvent.click(exit)
    expect(baseProps.onExit).toHaveBeenCalledTimes(1)
    expect(screen.queryByText(/terminate|終止/i)).toBeNull()
  })

  it('disables 退出 when exitDisabled, in the wide row and the overflow', () => {
    render(<ExecutionHeader {...baseProps} summary={summary({ state: 'terminated' })} exitDisabled />)
    expect(screen.getByTestId('header-exit')).toBeDisabled()
    fireEvent.click(screen.getByTestId('header-overflow'))
    expect(within(screen.getByTestId('header-overflow-panel')).getByTestId('overflow-exit')).toBeDisabled()
  })

  // Spec §1: take-to-terminal changes the pane's binding, it does not interrupt
  // a turn. It now lives in the view menu, whose trigger keeps the separator.
  it('separates the view menu from the interrupt actions', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={vi.fn()} />)
    const take = screen.getByTestId('view-mode')
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
    expect(wide).toContainElement(screen.getByTestId('header-exit'))
    expect(wide).not.toContainElement(screen.getByTestId('view-mode'))
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
    // 退出 is one click in the menu too (ExecutionView asks when it needs to).
    fireEvent.click(trigger)
    fireEvent.click(within(screen.getByTestId('header-overflow-panel')).getByTestId('overflow-exit'))
    expect(baseProps.onExit).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('header-overflow-panel')).toBeNull()
  })

  // R2 plan T1.2: "Take to terminal" is the terminal item of the view menu
  // (指揮室／聊天／終端機); the header draws no button of its own for it.
  it('the header no longer draws a separate take-back button', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={vi.fn()} onModeChange={vi.fn()} />)
    expect(screen.queryByTestId('take-back')).toBeNull()
    expect(screen.queryByText(/take to terminal/i)).toBeNull()
    const trigger = screen.getByTestId('view-mode')
    expect(trigger).toHaveTextContent('Room')
    expect(trigger.getAttribute('aria-haspopup')).toBe('menu')
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(trigger)
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    expect(within(screen.getByTestId('view-mode-menu')).getByTestId('view-mode-terminal')).toHaveTextContent(/take to terminal/i)
  })

  it('the view menu switches the view and takes the worker to a terminal', () => {
    const onModeChange = vi.fn()
    const onTakeBack = vi.fn()
    render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={onTakeBack} onModeChange={onModeChange} />)
    fireEvent.click(screen.getByTestId('view-mode'))
    fireEvent.click(screen.getByTestId('view-mode-chat'))
    expect(onModeChange).toHaveBeenCalledWith('chat')
    expect(screen.queryByTestId('view-mode-menu')).toBeNull()
    fireEvent.click(screen.getByTestId('view-mode'))
    fireEvent.click(screen.getByTestId('view-mode-terminal'))
    expect(onTakeBack).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('view-mode-menu')).toBeNull()
  })

  // Worker pane spec §4.7: narrow leaves only name + state + the overflow
  // menu, so the view trigger (with its separator) hides at `@max-md` and the
  // overflow carries the same three items under a small "view" label.
  it('the overflow lists the three view items at narrow widths', () => {
    const onTakeBack = vi.fn()
    const onModeChange = vi.fn()
    const { rerender } = render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={onTakeBack} onModeChange={onModeChange} />)
    const take = screen.getByTestId('view-mode')
    const sep = take.previousElementSibling as HTMLElement
    const group = take.closest('[class*="@max-md:hidden"]')
    expect(group).not.toBeNull()
    expect(group).toContainElement(sep)
    expect(group).not.toContainElement(screen.getByTestId('header-overflow'))
    fireEvent.click(screen.getByTestId('header-overflow'))
    const panel = screen.getByTestId('header-overflow-panel')
    expect(within(panel).getByText('View')).toBeInTheDocument()
    expect(within(panel).getByTestId('view-mode-room').getAttribute('aria-pressed')).toBe('true')
    expect(within(panel).getByTestId('view-mode-chat').getAttribute('aria-pressed')).toBe('false')
    expect(within(panel).queryByTestId('overflow-take-back')).toBeNull()
    fireEvent.click(within(panel).getByTestId('view-mode-chat'))
    expect(onModeChange).toHaveBeenCalledWith('chat')
    expect(screen.queryByTestId('header-overflow-panel')).toBeNull()
    fireEvent.click(screen.getByTestId('header-overflow'))
    const item = within(screen.getByTestId('header-overflow-panel')).getByTestId('view-mode-terminal')
    expect(item).toHaveTextContent(/take to terminal/i)
    fireEvent.click(item)
    expect(onTakeBack).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('header-overflow-panel')).toBeNull()
    // Busy take-back disables the terminal item.
    rerender(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={onTakeBack} onModeChange={onModeChange} takeBackBusy />)
    fireEvent.click(screen.getByTestId('header-overflow'))
    expect((within(screen.getByTestId('header-overflow-panel')).getByTestId('view-mode-terminal') as HTMLButtonElement).disabled).toBe(true)
  })

  // F12: menuitemradio / menuitem are only valid inside a `menu`, and the
  // overflow panel is not one (it also holds interrupt / terminate). There the
  // views are toggle buttons (aria-pressed); the view menu keeps its radios.
  it('F12: the overflow draws the views as pressed buttons, the view menu as radios in a menu', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={vi.fn()} />)
    fireEvent.click(screen.getByTestId('header-overflow'))
    const panel = screen.getByTestId('header-overflow-panel')
    expect(within(panel).queryByRole('menuitemradio')).toBeNull()
    expect(within(panel).queryByRole('menuitem')).toBeNull()
    expect(panel.querySelector('[role="menu"]')).toBeNull()
    const room = within(panel).getByTestId('view-mode-room')
    const chat = within(panel).getByTestId('view-mode-chat')
    expect(room).toHaveAttribute('aria-pressed', 'true')
    expect(chat).toHaveAttribute('aria-pressed', 'false')
    expect(room).not.toHaveAttribute('aria-checked')
    expect(within(panel).getByTestId('view-mode-terminal')).not.toHaveAttribute('role')
    fireEvent.click(screen.getByTestId('header-overflow'))

    fireEvent.click(screen.getByTestId('view-mode'))
    const menu = within(screen.getByTestId('view-mode-menu')).getByRole('menu')
    expect(within(menu).getAllByRole('menuitemradio')).toHaveLength(2)
    expect(within(menu).getByTestId('view-mode-room')).toHaveAttribute('aria-checked', 'true')
    expect(within(menu).getByRole('menuitem')).toHaveAttribute('data-testid', 'view-mode-terminal')
  })

  it('has no terminal item in the overflow without onTakeBack', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} onModeChange={vi.fn()} />)
    fireEvent.click(screen.getByTestId('header-overflow'))
    const panel = screen.getByTestId('header-overflow-panel')
    expect(within(panel).getByTestId('view-mode-room')).toBeInTheDocument()
    expect(within(panel).queryByTestId('view-mode-terminal')).toBeNull()
  })

  // Spec §5: chat keeps state + cost only; everything else is in the overflow,
  // at every width (a `mode` branch, not a breakpoint).
  it("chat's header shows state, cost and the overflow only", () => {
    const { container } = render(
      <ExecutionHeader {...baseProps} summary={summary()} mode="chat" onModeChange={vi.fn()} onTakeBack={vi.fn()} />,
    )
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    const cost = screen.getByTestId('execution-cost')
    expect(cost.closest('[class*="@max-md:hidden"]')).toBeNull()
    const trigger = screen.getByTestId('header-overflow')
    expect(trigger.className).not.toMatch(/(^|\s)hidden(\s|$)/)
    expect(screen.queryByTestId('worker-name')).toBeNull()
    expect(screen.queryByTestId('header-wide-actions')).toBeNull()
    expect(screen.queryByTestId('view-mode')).toBeNull()
    expect(screen.queryByRole('button', { name: /^interrupt$/i })).toBeNull()
    expect(screen.queryByTestId('header-exit')).toBeNull()
    expect(container.querySelectorAll('button')).toHaveLength(2)
  })

  it("chat's overflow holds interrupt, exit and the view items at wide widths too", () => {
    const onModeChange = vi.fn()
    render(<ExecutionHeader {...baseProps} summary={summary()} mode="chat" onModeChange={onModeChange} onTakeBack={vi.fn()} />)
    fireEvent.click(screen.getByTestId('header-overflow'))
    const panel = screen.getByTestId('header-overflow-panel')
    expect(within(panel).queryByTestId('overflow-cost')).toBeNull()
    expect(within(panel).getByTestId('view-mode-chat').getAttribute('aria-pressed')).toBe('true')
    expect(within(panel).getByTestId('view-mode-terminal')).toBeInTheDocument()
    fireEvent.click(within(panel).getByTestId('overflow-interrupt'))
    expect(baseProps.onInterrupt).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByTestId('header-overflow'))
    fireEvent.click(within(screen.getByTestId('header-overflow-panel')).getByTestId('overflow-exit'))
    expect(baseProps.onExit).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByTestId('header-overflow'))
    fireEvent.click(within(screen.getByTestId('header-overflow-panel')).getByTestId('view-mode-room'))
    expect(onModeChange).toHaveBeenCalledWith('room')
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
  it('offers no terminal item without onTakeBack', () => {
    render(<ExecutionHeader {...baseProps} summary={summary()} onModeChange={vi.fn()} />)
    fireEvent.click(screen.getByTestId('view-mode'))
    expect(screen.queryByTestId('view-mode-terminal')).toBeNull()
  })

  it('disables the terminal item while takeBackBusy, independent of `busy`', () => {
    const onTakeBack = vi.fn()
    const { rerender } = render(<ExecutionHeader {...baseProps} summary={summary()} onTakeBack={onTakeBack} takeBackBusy />)
    fireEvent.click(screen.getByTestId('view-mode'))
    const btn = screen.getByTestId('view-mode-terminal') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    fireEvent.click(btn)
    expect(onTakeBack).not.toHaveBeenCalled()
    // `busy` (terminal execution) gates interrupt, not take-back:
    // an ended execution can still go back to its terminal.
    rerender(<ExecutionHeader {...baseProps} summary={summary({ state: 'failed' })} busy onTakeBack={onTakeBack} />)
    expect((screen.getByTestId('view-mode-terminal') as HTMLButtonElement).disabled).toBe(false)
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

    // R4 T2.1: seq 974 continues seq 958, so the fixture is $0.1973 (was $0.2577).
    it('cost from the 12-turn fixture → enabled `$0.20`', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      const btn = costBtn()
      expect(btn.disabled).toBe(false)
      expect(btn.textContent).toBe('$0.20')
      // H3: an enabled anchor is a toggle; closed by default.
      expect(btn.getAttribute('aria-expanded')).toBe('false')
    })

    it('hover 800 ms → one-line summary tooltip', () => {
      vi.useFakeTimers()
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
      const tip = screen.getByRole('tooltip')
      expect(tip.textContent).toBe('12 turns · $0.1973 · 3.9k out · 57.6s API / 1m 33s wall')
      expect(tip.className).toMatch(/\bopacity-0\b/)
      fireEvent.mouseEnter(costBtn())
      act(() => vi.advanceTimersByTime(799))
      expect(tip.className).toMatch(/\bopacity-0\b/)
      act(() => vi.advanceTimersByTime(1))
      expect(tip.className).toMatch(/\bopacity-100\b/)
    })

    // R4 T2.2 (Q3): a hand-over's first turn resumes an outside session, so its
    // result carries spend from before the hand-over.
    describe('hand-over note (Q3)', () => {
      const note = 'Includes spend from before the hand-over'
      const cost = () => costSummary(fixturePayloads)

      it('successful hand-over → tooltip adds the note, and so does the panel', () => {
        render(<ExecutionHeader {...baseProps} summary={summary({ resume_session_id: 'c191a5a0' })} cost={cost()} />)
        const tip = screen.getByRole('tooltip')
        expect(tip.textContent).toContain('12 turns · $0.1973')
        expect(within(tip).getByTestId('cost-prior-history-tip').textContent).toBe(note)
        fireEvent.click(costBtn())
        expect(screen.getByTestId('cost-prior-history').textContent).toBe(note)
      })

      it('no resume_session_id → no note anywhere', () => {
        render(<ExecutionHeader {...baseProps} summary={summary()} cost={cost()} />)
        expect(screen.getByRole('tooltip').textContent).not.toContain(note)
        fireEvent.click(costBtn())
        expect(screen.queryByTestId('cost-prior-history')).toBeNull()
      })

      it('review #7: hand-over but no costed result yet → no note anywhere', () => {
        render(<ExecutionHeader {...baseProps} summary={summary({ state: 'running', resume_session_id: 'c191a5a0' })} cost={costSummary([])} />)
        expect(screen.getByRole('tooltip').textContent).not.toContain(note)
        fireEvent.click(costBtn())
        expect(screen.queryByTestId('cost-prior-history')).toBeNull()
      })

      it('turn 1 resume rejected (terminal_reason session_expired) → no note', () => {
        const s = summary({ state: 'failed', resume_session_id: 'c191a5a0', terminal_reason: 'session_expired', last_turn_reason: 'session_expired' })
        render(<ExecutionHeader {...baseProps} summary={s} cost={cost()} />)
        expect(screen.getByRole('tooltip').textContent).not.toContain(note)
        fireEvent.click(costBtn())
        expect(screen.queryByTestId('cost-prior-history')).toBeNull()
      })
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
      expect(screen.getByTestId('cost-totals').textContent).toContain('$0.1973')
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

    // Codex R1 (PR #1464): an open panel must not outlive its anchor when the
    // pane crosses the `@md` breakpoint — the anchor goes `display:none`, its
    // rect is all zeros, and FloatingPanel's next reflow would pin the panel to
    // the top-left corner. The header watches its own size and closes a panel
    // whose anchor no longer has a box.
    describe('closes panels whose anchor disappears across the breakpoint', () => {
      let roCallbacks: Array<() => void> = []
      const box = { x: 10, y: 10, left: 10, top: 10, right: 60, bottom: 30, width: 50, height: 20, toJSON: () => ({}) } as DOMRect
      const zero = { x: 0, y: 0, left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0, toJSON: () => ({}) } as DOMRect
      const setRect = (el: HTMLElement, r: DOMRect) => { vi.spyOn(el, 'getBoundingClientRect').mockReturnValue(r) }
      const fireResize = () => act(() => { roCallbacks.forEach((cb) => cb()) })
      beforeEach(() => {
        roCallbacks = []
        vi.stubGlobal('ResizeObserver', class {
          private cb: () => void
          constructor(cb: () => void) { this.cb = cb }
          observe() { roCallbacks.push(this.cb) }
          unobserve() {}
          disconnect() { roCallbacks = roCallbacks.filter((c) => c !== this.cb) }
        })
      })
      afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

      it('wide → narrow: the cost panel opened from the inline button closes', () => {
        render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
        setRect(costBtn(), box)
        fireEvent.click(costBtn())
        fireResize()
        expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
        setRect(costBtn(), zero)
        fireResize()
        expect(screen.queryByTestId('cost-panel')).toBeNull()
        expect(costBtn().getAttribute('aria-expanded')).toBe('false')
      })

      it('narrow → wide: the cost panel and the overflow menu opened from the trigger close', () => {
        render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
        const trigger = screen.getByTestId('header-overflow')
        setRect(trigger, box)
        fireEvent.click(trigger)
        fireResize()
        expect(screen.getByTestId('header-overflow-panel')).toBeInTheDocument()
        setRect(trigger, zero)
        fireResize()
        expect(screen.queryByTestId('header-overflow-panel')).toBeNull()
        setRect(trigger, box)
        fireEvent.click(trigger)
        fireEvent.click(screen.getByTestId('overflow-cost'))
        fireResize()
        expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
        setRect(trigger, zero)
        fireResize()
        expect(screen.queryByTestId('cost-panel')).toBeNull()
      })

      // Codex re-review (PR #1464): FloatingPanel hands focus back to the
      // anchor it opened from, which is now `display:none` — a real browser
      // can't focus it and focus lands on <body>. The header moves focus to the
      // trigger that is visible on this side of the breakpoint instead.
      it('wide → narrow: focus moves to the visible overflow trigger, not the hidden cost button', () => {
        render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
        const trigger = screen.getByTestId('header-overflow')
        setRect(costBtn(), box)
        setRect(trigger, zero)
        costBtn().focus()
        fireEvent.click(costBtn())
        fireResize()
        expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
        setRect(costBtn(), zero)
        setRect(trigger, box)
        fireResize()
        expect(screen.queryByTestId('cost-panel')).toBeNull()
        expect(document.activeElement).toBe(trigger)
      })

      it('narrow → wide: focus moves to the visible cost button, not the hidden overflow trigger', () => {
        render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />)
        const trigger = screen.getByTestId('header-overflow')
        setRect(costBtn(), zero)
        setRect(trigger, box)
        // The overflow menu itself.
        trigger.focus()
        fireEvent.click(trigger)
        fireResize()
        expect(screen.getByTestId('header-overflow-panel')).toBeInTheDocument()
        setRect(costBtn(), box)
        setRect(trigger, zero)
        fireResize()
        expect(screen.queryByTestId('header-overflow-panel')).toBeNull()
        expect(document.activeElement).toBe(costBtn())
        // The cost panel opened from the overflow menu.
        setRect(costBtn(), zero)
        setRect(trigger, box)
        trigger.focus()
        fireEvent.click(trigger)
        fireEvent.click(screen.getByTestId('overflow-cost'))
        fireResize()
        expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
        setRect(costBtn(), box)
        setRect(trigger, zero)
        fireResize()
        expect(screen.queryByTestId('cost-panel')).toBeNull()
        expect(document.activeElement).toBe(costBtn())
      })

      // R2 plan T1.2 (plan review #3): the view menu is controlled by the
      // header so this same effect closes it, and hands focus to the trigger
      // visible on the narrow side (the overflow).
      it('the view menu closes when its anchor loses its box', () => {
        render(<ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} onTakeBack={vi.fn()} onModeChange={vi.fn()} />)
        const view = screen.getByTestId('view-mode')
        const trigger = screen.getByTestId('header-overflow')
        setRect(costBtn(), box)
        setRect(view, box)
        setRect(trigger, zero)
        view.focus()
        fireEvent.click(view)
        fireResize()
        expect(screen.getByTestId('view-mode-menu')).toBeInTheDocument()
        setRect(costBtn(), zero)
        setRect(view, zero)
        setRect(trigger, box)
        fireResize()
        expect(screen.queryByTestId('view-mode-menu')).toBeNull()
        expect(view.getAttribute('aria-expanded')).toBe('false')
        expect(document.activeElement).toBe(trigger)
      })

      it('does not steal focus the user has moved elsewhere while the panel was open', () => {
        render(<>
          <ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} />
          <input data-testid="elsewhere" />
        </>)
        const trigger = screen.getByTestId('header-overflow')
        setRect(costBtn(), box)
        setRect(trigger, zero)
        costBtn().focus()
        fireEvent.click(costBtn())
        fireResize()
        screen.getByTestId('elsewhere').focus()
        setRect(costBtn(), zero)
        setRect(trigger, box)
        fireResize()
        expect(screen.queryByTestId('cost-panel')).toBeNull()
        expect(document.activeElement).toBe(screen.getByTestId('elsewhere'))
      })
    })

    it('cost=null → no aria-expanded and click does nothing', () => {
      render(<ExecutionHeader {...baseProps} summary={summary()} cost={null} />)
      expect(costBtn().hasAttribute('aria-expanded')).toBe(false)
      fireEvent.click(costBtn())
      expect(screen.queryByTestId('cost-panel')).toBeNull()
      expect(costBtn().hasAttribute('aria-expanded')).toBe(false)
    })
  })

  // F5 / F6 / F7: a view switch — from the menu, or from elsewhere (Profile
  // Sync writes the pane's mode) — must not leave panels open behind it, and
  // must not drop focus on <body> when the trigger it came from is gone.
  describe('a view switch closes the panels and keeps focus on a visible trigger', () => {
    const box = { x: 10, y: 10, left: 10, top: 10, right: 60, bottom: 30, width: 50, height: 20, toJSON: () => ({}) } as DOMRect
    const zero = { x: 0, y: 0, left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0, toJSON: () => ({}) } as DOMRect
    /** The CSS at a width, by class: `hidden` / `@max-md:hidden` / `@max-md:flex`. */
    const layout = (width: 'wide' | 'narrow') => {
      const hiddenAt = (el: HTMLElement | null): boolean => {
        if (!el) return false
        const c = el.classList
        const hidden = width === 'wide' ? c.contains('hidden') : c.contains('@max-md:hidden') || (c.contains('hidden') && !c.contains('@max-md:flex'))
        return hidden || hiddenAt(el.parentElement)
      }
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
        return this.isConnected && !hiddenAt(this) ? box : zero
      })
    }
    afterEach(() => { vi.restoreAllMocks() })
    const panels = ['worker-info-panel', 'view-mode-menu', 'cost-panel', 'header-overflow-panel']
    const openPanels = () => panels.filter((id) => screen.queryByTestId(id))

    function Pane({ start = 'room' as 'room' | 'chat' }) {
      const [mode, setMode] = useState(start)
      return <ExecutionHeader {...baseProps} summary={summary()} cost={costSummary(fixturePayloads)} onTakeBack={vi.fn()} mode={mode} onModeChange={setMode} />
    }

    it('F5: a remote switch closes all four panels, and switching back reopens none', () => {
      layout('wide')
      const props = { ...baseProps, summary: summary(), cost: costSummary(fixturePayloads), onTakeBack: vi.fn() }
      const { rerender } = render(<ExecutionHeader {...props} mode="room" />)
      fireEvent.click(screen.getByTestId('worker-name'))
      fireEvent.click(screen.getByTestId('view-mode'))
      fireEvent.click(screen.getByTestId('execution-cost'))
      fireEvent.click(screen.getByTestId('header-overflow'))
      expect(openPanels()).toEqual(panels)
      rerender(<ExecutionHeader {...props} mode="chat" />)
      expect(openPanels()).toEqual([])
      rerender(<ExecutionHeader {...props} mode="room" />)
      expect(openPanels()).toEqual([])
    })

    it('F6: the cost panel opened in chat closes when the pane goes back to the room', () => {
      layout('narrow')
      const props = { ...baseProps, summary: summary(), cost: costSummary(fixturePayloads) }
      const { rerender } = render(<ExecutionHeader {...props} mode="chat" />)
      fireEvent.click(screen.getByTestId('execution-cost'))
      expect(screen.getByTestId('cost-panel')).toBeInTheDocument()
      rerender(<ExecutionHeader {...props} mode="room" />)
      expect(screen.queryByTestId('cost-panel')).toBeNull()
    })

    it('F7: room → chat through the view menu leaves focus on a visible trigger, not <body>', () => {
      layout('wide')
      render(<Pane />)
      const view = screen.getByTestId('view-mode')
      view.focus()
      fireEvent.click(view)
      fireEvent.click(screen.getByTestId('view-mode-chat'))
      expect(screen.queryByTestId('view-mode')).toBeNull()
      expect(document.activeElement).not.toBe(document.body)
      expect(document.activeElement).toBe(screen.getByTestId('execution-cost'))
    })

    it('F7: chat → room through the overflow leaves focus on a visible trigger, not the hidden overflow', () => {
      layout('wide')
      render(<Pane start="chat" />)
      const trigger = screen.getByTestId('header-overflow')
      trigger.focus()
      fireEvent.click(trigger)
      fireEvent.click(screen.getByTestId('view-mode-room'))
      expect(screen.getByTestId('view-mode')).toBeInTheDocument()
      expect(document.activeElement).not.toBe(document.body)
      expect(document.activeElement).not.toBe(screen.getByTestId('header-overflow'))
      expect(document.activeElement).toBe(screen.getByTestId('execution-cost'))
    })

    it('F5: the worker info left open does not pop back after room → chat → room', () => {
      layout('wide')
      render(<Pane />)
      fireEvent.click(screen.getByTestId('worker-name'))
      expect(screen.getByTestId('worker-info-panel')).toBeInTheDocument()
      fireEvent.click(screen.getByTestId('view-mode'))
      fireEvent.click(screen.getByTestId('view-mode-chat'))
      fireEvent.click(screen.getByTestId('header-overflow'))
      fireEvent.click(screen.getByTestId('view-mode-room'))
      expect(screen.getByTestId('view-mode')).toBeInTheDocument()
      expect(screen.queryByTestId('worker-info-panel')).toBeNull()
    })
  })

  // D8: state dot colour matches the terminal agent badge
  describe('state dot colours', () => {
    // The dot is the rounded-full span sharing a parent with the state label; no sibling-order dependency.
    const dotOf = () => screen.getByTestId('execution-state').parentElement!.querySelector('.rounded-full')

    it.each(Object.entries(STATE_DOT_CLASSES))('%s state has %s', (state, cls) => {
      render(<ExecutionHeader {...baseProps} summary={summary({ state: state as ExecutionSummary['state'] })} onTakeBack={vi.fn()} />)
      expect(dotOf()).toHaveClass(cls)
    })

    it('an unknown state falls back to bg-text-muted', () => {
      render(<ExecutionHeader {...baseProps} summary={summary({ state: 'weird' as ExecutionSummary['state'] })} onTakeBack={vi.fn()} />)
      expect(dotOf()).toHaveClass('bg-text-muted')
    })
  })
})

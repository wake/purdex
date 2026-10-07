// spa/src/components/executions/ExecutionRowCompact.test.tsx — R4 T4.1: the
// worker_rollup fields on a sidebar row (cost behind the cost_basis gate,
// running badge, activity as the dot's tooltip, hand-over note).
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { ExecutionRowCompact } from './ExecutionRowCompact'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import type { ExecutionSummary } from '../../lib/nex/types'

const NOW = 1_700_000_000_000

const row = (over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id: 'exc_a', state: 'running', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'brief', labels: {}, created_at: 0, updated_at: NOW, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary

const renderRow = (r: ExecutionSummary, showCost = true) =>
  render(<ExecutionRowCompact row={r} daemonHostId={null} now={NOW} showCost={showCost} onOpen={() => {}} />)

afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

describe('ExecutionRowCompact — rollup fields', () => {
  it('old daemon (no rollup fields): renders as before — no cost, no badge, dot tooltip is the state', () => {
    renderRow(row())
    expect(screen.queryByTestId('executions-cost')).toBeNull()
    expect(screen.queryByTestId('executions-running')).toBeNull()
    expect(screen.getByTestId('executions-state-dot')).toHaveAttribute('title', 'running')
  })

  it('cost after the age when cost_usd is a number and the gate allows it', () => {
    renderRow(row({ cost_usd: 0.1149 }))
    const cost = screen.getByTestId('executions-cost')
    expect(cost).toHaveTextContent('$0.11')
    // after the age
    expect(screen.getByTestId('executions-age').compareDocumentPosition(cost) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(cost).not.toHaveAttribute('title')
  })

  it('cost_basis gate closed (today\'s v0.13.1 daemon): cost hidden even with a number', () => {
    renderRow(row({ cost_usd: 0.1149 }), false)
    expect(screen.queryByTestId('executions-cost')).toBeNull()
  })

  it('cost_usd null → no cost', () => {
    renderRow(row({ cost_usd: null }))
    expect(screen.queryByTestId('executions-cost')).toBeNull()
  })

  it('Q3: the hand-over note is the cost\'s title when a resumed hand-over billed something (en + zh-TW)', () => {
    renderRow(row({ cost_usd: 0.5, resume_session_id: 'c191a5a0' }))
    expect(screen.getByTestId('executions-cost')).toHaveAttribute('title', 'Includes spend from before the hand-over')
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    expect(screen.getByTestId('executions-cost')).toHaveAttribute('title', '含接續前的費用')
  })

  it('Q3: no note when turn 1\'s resume failed', () => {
    renderRow(row({ state: 'failed', cost_usd: 0.5, resume_session_id: 'c191a5a0', terminal_reason: 'session_expired' }))
    expect(screen.getByTestId('executions-cost')).not.toHaveAttribute('title')
  })

  it('running badge: none at 0, icon + count at 2', () => {
    const { unmount } = renderRow(row({ running_tasks: 0 }))
    expect(screen.queryByTestId('executions-running')).toBeNull()
    unmount()
    renderRow(row({ running_tasks: 2 }))
    const badge = screen.getByTestId('executions-running')
    expect(badge).toHaveTextContent('2')
    expect(badge).toHaveAttribute('title', '2 running')
    expect(badge.querySelector('svg')).not.toBeNull()
  })

  it('dot tooltip follows activity.phase (en)', () => {
    const cases: [ExecutionSummary['activity'], string][] = [
      [{ phase: 'tool', tool: { name: 'Bash', tool_use_id: 't', since: 1 }, open_tools: 1 }, 'Running Bash'],
      [{ phase: 'model', open_tools: 0 }, 'Thinking'],
      [{ phase: 'starting', open_tools: 0 }, 'Starting'],
      [{ phase: 'idle', open_tools: 0 }, 'Idle'],
      [{ phase: 'queued', open_tools: 0 }, 'Queued'],
      [{ phase: 'awaiting_input', open_tools: 0 }, 'Thinking'], // unknown phase reads as model
    ]
    for (const [activity, title] of cases) {
      const { unmount } = renderRow(row({ activity }))
      expect(screen.getByTestId('executions-state-dot')).toHaveAttribute('title', title)
      unmount()
    }
  })

  it('tool phase without a tool name falls back to last_tool, then to a generic label', () => {
    const { unmount } = renderRow(row({ activity: { phase: 'tool', open_tools: 1 }, last_tool: { name: 'Read', tool_use_id: 'x', at: 1 } }))
    expect(screen.getByTestId('executions-state-dot')).toHaveAttribute('title', 'Running Read')
    unmount()
    renderRow(row({ activity: { phase: 'tool', open_tools: 1 } }))
    expect(screen.getByTestId('executions-state-dot')).toHaveAttribute('title', 'Running a tool')
  })

  it('ended phase keeps the existing label (the state)', () => {
    renderRow(row({ state: 'terminated', activity: { phase: 'ended', open_tools: 0 } }))
    expect(screen.getByTestId('executions-state-dot')).toHaveAttribute('title', 'terminated')
  })

  it('zh-TW activity labels', () => {
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    const cases: [ExecutionSummary['activity'], string][] = [
      [{ phase: 'tool', tool: { name: 'Bash', tool_use_id: 't', since: 1 }, open_tools: 1 }, '執行 Bash'],
      [{ phase: 'model', open_tools: 0 }, '思考中'],
      [{ phase: 'starting', open_tools: 0 }, '啟動中'],
      [{ phase: 'idle', open_tools: 0 }, '閒置'],
      [{ phase: 'queued', open_tools: 0 }, '排隊中'],
    ]
    for (const [activity, title] of cases) {
      const { unmount } = renderRow(row({ activity }))
      expect(screen.getByTestId('executions-state-dot')).toHaveAttribute('title', title)
      unmount()
    }
  })

  it('narrow widths: the brief stays the only shrinking part, the rollup items never wrap or shrink', () => {
    renderRow(row({ cost_usd: 0.11, running_tasks: 3 }))
    expect(screen.getByTestId('executions-brief')).toHaveClass('truncate', 'min-w-0', 'flex-1')
    for (const id of ['executions-cost', 'executions-running']) {
      expect(screen.getByTestId(id)).toHaveClass('shrink-0', 'whitespace-nowrap')
    }
    expect(screen.getByTestId('executions-row')).not.toHaveClass('flex-wrap')
  })

  it('a non-openable row names the rollup in its aria-label', () => {
    render(<ExecutionRowCompact row={row({ cost_usd: 0.11, running_tasks: 2, activity: { phase: 'model', open_tools: 0 } })} daemonHostId={null} now={NOW} showCost />)
    const label = screen.getByTestId('executions-row').getAttribute('aria-label') ?? ''
    expect(label).toContain('Thinking')
    expect(label).toContain('2 running')
    expect(label).toContain('$0.11')
  })

  // D8: state dot colour matches the terminal agent badge
  describe('state dot colours', () => {
    it('running state has bg-status-success', () => {
      renderRow(row({ state: 'running' }))
      expect(screen.getByTestId('executions-state-dot')).toHaveClass('bg-status-success')
    })

    it('idle state has bg-text-muted', () => {
      renderRow(row({ state: 'idle' }))
      expect(screen.getByTestId('executions-state-dot')).toHaveClass('bg-text-muted')
    })

    it('terminated state has bg-text-muted', () => {
      renderRow(row({ state: 'terminated' }))
      expect(screen.getByTestId('executions-state-dot')).toHaveClass('bg-text-muted')
    })

    it('failed state has bg-status-error', () => {
      renderRow(row({ state: 'failed' }))
      expect(screen.getByTestId('executions-state-dot')).toHaveClass('bg-status-error')
    })

    it('queued state has bg-status-warning', () => {
      renderRow(row({ state: 'queued' }))
      expect(screen.getByTestId('executions-state-dot')).toHaveClass('bg-status-warning')
    })
  })
})

// Permission channel PC2 / spec §5.4: 「等待核准」 on a row is the warning dot plus the HandPalm icon — never the queued
// look (queued is the same warning dot with no icon).
describe('ExecutionRowCompact — awaiting approval', () => {
  const pending = { request_id: 'r1', tool_name: 'Bash', since: NOW - 5_000 }
  const tool = { phase: 'tool', tool: { name: 'Bash', tool_use_id: 't', since: 1 }, open_tools: 1 } as const

  it('pending_permission set: warning dot, the HandPalm icon, and 「Awaiting approval」 as the activity tooltip (en)', () => {
    renderRow(row({ state: 'running', activity: tool, pending_permission: pending }))
    const dot = screen.getByTestId('executions-state-dot')
    expect(dot).toHaveClass('bg-status-warning')
    expect(dot).not.toHaveClass('bg-status-success')
    expect(dot).toHaveAttribute('title', 'Awaiting approval')
    const icon = screen.getByTestId('executions-awaiting')
    expect(icon.querySelector('svg')).not.toBeNull()
    expect(icon).toHaveAttribute('title', 'Awaiting approval')
    expect(icon).toHaveClass('shrink-0', 'text-status-warning')
  })

  it('zh-TW: the tooltip reads 等待核准', () => {
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    renderRow(row({ state: 'running', pending_permission: pending }))
    expect(screen.getByTestId('executions-state-dot')).toHaveAttribute('title', '等待核准')
    expect(screen.getByTestId('executions-awaiting')).toHaveAttribute('title', '等待核准')
  })

  it('pending_permission: null → the row as before (state colour, activity tooltip, no icon)', () => {
    renderRow(row({ state: 'running', activity: tool, pending_permission: null }))
    const dot = screen.getByTestId('executions-state-dot')
    expect(dot).toHaveClass('bg-status-success')
    expect(dot).toHaveAttribute('title', 'Running Bash')
    expect(screen.queryByTestId('executions-awaiting')).toBeNull()
  })

  it('the field absent (old daemon) → the row as before', () => {
    renderRow(row({ state: 'running', activity: tool }))
    const dot = screen.getByTestId('executions-state-dot')
    expect(dot).toHaveClass('bg-status-success')
    expect(dot).toHaveAttribute('title', 'Running Bash')
    expect(screen.queryByTestId('executions-awaiting')).toBeNull()
  })

  it.each([
    ['terminated', {}], ['rejected', {}], ['failed', {}], ['idle', { archived: true }],
  ])('pending + %s %j: no waiting dot text, no icon', (state, extra) => {
    renderRow(row({ state, ...extra, pending_permission: pending }))
    expect(screen.queryByTestId('executions-awaiting')).toBeNull()
    expect(screen.getByTestId('executions-state-dot')).not.toHaveAttribute('title', 'Awaiting approval')
  })

  it('a queued row keeps the plain warning dot with no icon', () => {
    renderRow(row({ state: 'queued', activity: { phase: 'queued', open_tools: 0 } }))
    const dot = screen.getByTestId('executions-state-dot')
    expect(dot).toHaveClass('bg-status-warning')
    expect(dot).toHaveAttribute('title', 'Queued')
    expect(screen.queryByTestId('executions-awaiting')).toBeNull()
  })

  it('a non-openable row names the state in its aria-label', () => {
    render(<ExecutionRowCompact row={row({ pending_permission: pending })} daemonHostId={null} now={NOW} />)
    expect(screen.getByTestId('executions-row').getAttribute('aria-label') ?? '').toContain('Awaiting approval')
  })
})

describe('ExecutionRowCompact — exit action', () => {
  it('renders an exit action beside the open button, not inside it', () => {
    const onOpen = vi.fn(), onExit = vi.fn()
    render(<ExecutionRowCompact row={row({ state: 'idle' })} daemonHostId={null} now={NOW} onOpen={onOpen} onExit={onExit} />)
    const exit = screen.getByTestId('executions-row-exit')
    expect(exit.closest('button[data-testid="executions-row"]')).toBeNull()
    expect(exit).toHaveAttribute('aria-label', 'Exit')
    expect(exit.className).toContain('opacity-0')
    expect(exit.className).not.toContain('hidden')
    fireEvent.click(exit)
    expect(onExit).toHaveBeenCalledTimes(1)
    expect(onOpen).not.toHaveBeenCalled()
  })

  // #1627 Q2 (user rule): like a storage row's actions, the exit sits in flow at the row's end with its width always
  // reserved — revealed on hover / keyboard focus, never laid over the age or the cost (jsdom has no layout: classes + order).
  it('sits in flow after the age and cost, its place always reserved, never absolutely over them', () => {
    render(<ExecutionRowCompact row={row({ state: 'idle', cost_usd: 0.5 })} daemonHostId={null} now={NOW} showCost onOpen={() => {}} onExit={() => {}} />)
    const exit = screen.getByTestId('executions-row-exit')
    expect(exit).not.toHaveClass('absolute')
    expect(exit.parentElement).not.toHaveClass('relative')
    expect(exit.parentElement!.lastElementChild).toBe(exit)
    for (const id of ['executions-age', 'executions-cost']) {
      expect(screen.getByTestId(id).compareDocumentPosition(exit) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    }
    expect(exit).toHaveClass('shrink-0', 'opacity-0', 'group-hover:opacity-100', 'group-focus-within:opacity-100', 'focus-visible:opacity-100')
    expect(exit.className).not.toMatch(/(^|\s)(hidden|invisible)(\s|$)/)
  })

  it('has no exit action without onExit', () => {
    render(<ExecutionRowCompact row={row()} daemonHostId={null} now={NOW} onOpen={() => {}} />)
    expect(screen.queryByTestId('executions-row-exit')).toBeNull()
  })
})

// #1771: a worker created by a handoff has an empty brief; its row is named by the conversation title (when the
// host capability says the field exists — fail closed), else the cwd basename. A row with a brief is unchanged.
describe('ExecutionRowCompact — a row without a brief is named (#1771)', () => {
  const HOST = 'host-a'
  const setTitleSupported = (supported: boolean) =>
    useNexHostStore.setState({
      byHost: {
        [HOST]: {
          info: null, error: null, fetchedAt: 0, generation: 0, fingerprint: '',
          phase: 'ready',
          capabilities: (supported ? { session_title: { sources: ['ai'], max_bytes: 200 } } : {}) as never,
        },
      },
    })
  const handoff = (over: Partial<ExecutionSummary> = {}) =>
    row({ brief: '', cwd: '/Users/wake/Workspace/wake/purdex', session_title: { text: 'Zebrafinch', source: 'ai' }, ...over })

  beforeEach(() => { useNexHostStore.setState({ byHost: {} }) })
  afterEach(() => { useNexHostStore.setState({ byHost: {} }) })

  it('an empty brief + the capability: the conversation title is the name (text and the button\'s name)', () => {
    setTitleSupported(true)
    render(<ExecutionRowCompact row={handoff()} hostId={HOST} daemonHostId={null} now={NOW} onOpen={() => {}} />)
    expect(screen.getByTestId('executions-brief').textContent).toBe('Zebrafinch')
    expect(screen.getByRole('button', { name: /Zebrafinch/ })).toBe(screen.getByTestId('executions-row'))
  })

  it('the same row without the capability: the cwd basename (the title is never read)', () => {
    setTitleSupported(false)
    render(<ExecutionRowCompact row={handoff()} hostId={HOST} daemonHostId={null} now={NOW} onOpen={() => {}} />)
    expect(screen.getByTestId('executions-brief').textContent).toBe('purdex')
  })

  it('the capability arriving later renames the row (read from the store, not snapshotted)', () => {
    setTitleSupported(false)
    render(<ExecutionRowCompact row={handoff()} hostId={HOST} daemonHostId={null} now={NOW} onOpen={() => {}} />)
    expect(screen.getByTestId('executions-brief').textContent).toBe('purdex')
    act(() => { setTitleSupported(true) })
    expect(screen.getByTestId('executions-brief').textContent).toBe('Zebrafinch')
  })

  it('a row that cannot know its host (no hostId) fails closed: the cwd basename', () => {
    setTitleSupported(true)
    render(<ExecutionRowCompact row={handoff()} daemonHostId={null} now={NOW} onOpen={() => {}} />)
    expect(screen.getByTestId('executions-brief').textContent).toBe('purdex')
  })

  it('a non-openable row\'s aria-label leads with the same name', () => {
    setTitleSupported(true)
    const { unmount } = render(<ExecutionRowCompact row={handoff()} hostId={HOST} daemonHostId={null} now={NOW} />)
    expect(screen.getByTestId('executions-row').getAttribute('aria-label') ?? '').toMatch(/^Zebrafinch · /)
    unmount()
    setTitleSupported(false)
    render(<ExecutionRowCompact row={handoff()} hostId={HOST} daemonHostId={null} now={NOW} />)
    expect(screen.getByTestId('executions-row').getAttribute('aria-label') ?? '').toMatch(/^purdex · /)
  })

  it('two waiting handoff workers can be told apart', () => {
    setTitleSupported(true)
    const pending = { request_id: 'r1', tool_name: 'Bash', since: NOW - 5_000 }
    render(
      <div>
        <ExecutionRowCompact row={handoff({ id: 'exc_1', pending_permission: pending })} hostId={HOST} daemonHostId={null} now={NOW} onOpen={() => {}} />
        <ExecutionRowCompact row={handoff({ id: 'exc_2', pending_permission: pending, session_title: { text: 'Kestrel', source: 'ai' } })} hostId={HOST} daemonHostId={null} now={NOW} onOpen={() => {}} />
      </div>,
    )
    expect(screen.getAllByTestId('executions-brief').map((el) => el.textContent)).toEqual(['Zebrafinch', 'Kestrel'])
  })

  it('pin: a row WITH a brief renders byte-identically to before #1771, title and capability notwithstanding', () => {
    setTitleSupported(true)
    const briefed = row({ brief: 'Fix the bug\nsecond line', cwd: '/w/repo', session_title: { text: 'Zebrafinch', source: 'ai' } })
    const { container, unmount } = render(<ExecutionRowCompact row={briefed} hostId={HOST} daemonHostId={null} now={NOW} onOpen={() => {}} />)
    expect(container.innerHTML).toMatchInlineSnapshot(`"<button type="button" data-testid="executions-row" title="exc_a" class="flex items-center gap-1.5 w-full min-w-0 px-3 py-1 text-left cursor-pointer hover:bg-surface-hover"><span data-testid="executions-state-dot" class="shrink-0 inline-block w-2 h-2 rounded-full bg-status-success" title="running"></span><span data-testid="executions-brief" class="flex-1 min-w-0 truncate text-xs text-text-primary">Fix the bug</span><span data-testid="executions-age" class="shrink-0 text-xs text-text-muted tabular-nums">just now</span></button>"`)
    unmount()
    const plain = render(<ExecutionRowCompact row={briefed} hostId={HOST} daemonHostId={null} now={NOW} />)
    expect(plain.container.innerHTML).toMatchInlineSnapshot(`"<div data-testid="executions-row" role="listitem" aria-label="Fix the bug · running · just now · exc_a" title="exc_a" class="flex items-center gap-1.5 w-full min-w-0 px-3 py-1 text-left"><span data-testid="executions-state-dot" class="shrink-0 inline-block w-2 h-2 rounded-full bg-status-success" title="running"></span><span data-testid="executions-brief" class="flex-1 min-w-0 truncate text-xs text-text-primary">Fix the bug</span><span data-testid="executions-age" class="shrink-0 text-xs text-text-muted tabular-nums">just now</span></div>"`)
  })
})

// spa/src/components/executions/ExecutionRowCompact.test.tsx — R4 T4.1: the
// worker_rollup fields on a sidebar row (cost behind the cost_basis gate,
// running badge, activity as the dot's tooltip, hand-over note).
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { ExecutionRowCompact } from './ExecutionRowCompact'
import { useI18nStore } from '../../stores/useI18nStore'
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
})

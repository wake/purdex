// U3 mount: the status row wired to the session's statusLine snapshot (the Mac bar's source) and the conversation.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { useAgentStore } from '../../stores/useAgentStore'
import type { ConversationItem } from '../../lib/conversations/types'
import { SessionStatusRow } from './SessionStatusRow'

const H = 'host-1'
const CODE = 'dev001'
const NOW = 1_800_000_000_000
const SNAP = {
  model: { display_name: 'Opus 5.5 (1M)' },
  effort: { level: 'xhigh' },
  cost: { total_cost_usd: 0.42 },
  context_window: { used_percentage: 38, context_window_size: 1_000_000 },
  rate_limits: { five_hour: { used_percentage: 66, resets_at: (NOW + 133 * 60_000) / 1000 }, seven_day: { used_percentage: 39, resets_at: (NOW + 76 * 3_600_000) / 1000 } },
}
const step = (over: object = {}): ConversationItem => ({
  type: 'step', id: 's', at: 5, index: 0, kind: 'execute', tool: 'Bash', status: 'done', summary: 'x', started_at: NOW - 42_000, input: null, ...over,
}) as ConversationItem
const ctx = (over: object = {}) => ({ hostId: H, items: [] as ConversationItem[], status: 'idle', usage: undefined, ...over })
const snapshot = (raw: Record<string, unknown>, at = NOW) => useAgentStore.setState({ ccStatus: { [`${H}:${CODE}`]: { receivedAt: at, raw } } })

beforeEach(() => { cleanup(); vi.useFakeTimers(); vi.setSystemTime(NOW); useAgentStore.setState({ ccStatus: {} }) })
afterEach(() => { vi.useRealTimers() })

describe('SessionStatusRow', () => {
  it('prints model · effort · context · 5h · 7d · cost from the session\'s statusLine snapshot, with the Mac bar\'s numbers', () => {
    snapshot(SNAP)
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    expect(screen.getByTestId('item-model').textContent).toBe('Opus 5.5 (1M) · xhigh')
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('62%')
    expect(screen.getByTestId('ctx-tokens').textContent).toBe('620K left')
    expect(screen.getByTestId('status-seg-usage-five-hour').textContent).toBe('34%')
    expect(screen.getByTestId('status-seg-usage-seven-day').textContent).toBe('61%')
    expect(screen.getByTestId('item-cost').textContent).toBe('$0.42')
    expect(screen.getByTestId('state-slot').dataset.state).toBe('idle')
  })

  it('reads the snapshot of THIS session only (host + code)', () => {
    useAgentStore.setState({ ccStatus: { [`other:${CODE}`]: { receivedAt: NOW, raw: SNAP }, [`${H}:zzz999`]: { receivedAt: NOW, raw: SNAP } } })
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    expect(screen.getByTestId('item-cost').textContent).toBe('—')
  })

  it('without a snapshot every value reads 「—」, never 0 %', () => {
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    expect(screen.getByTestId('item-model').textContent).toBe('—')
    for (const id of ['status-seg-usage-context', 'status-seg-usage-five-hour', 'status-seg-usage-seven-day']) {
      expect(screen.getByTestId(id).textContent).toBe('—')
      expect(screen.getByTestId(id)).toHaveAttribute('data-missing', 'true')
    }
    expect(screen.getByTestId('item-cost').textContent).toBe('—')
    expect(screen.queryByTestId('ctx-tokens')).toBeNull()
  })

  it('a partial snapshot fills what it has and dashes the rest; the conversation\'s own model / effort fill in when the snapshot has none', () => {
    snapshot({ rate_limits: { five_hour: { used_percentage: 10, resets_at: (NOW + 60_000) / 1000 } } })
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx({ usage: { model: 'Sonnet 5.5', effort: 'low' } })} />)
    expect(screen.getByTestId('item-model').textContent).toBe('Sonnet 5.5 · low')
    expect(screen.getByTestId('status-seg-usage-five-hour').textContent).toBe('90%')
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('—')
    expect(screen.getByTestId('status-seg-usage-seven-day').textContent).toBe('—')
  })

  it('the statusLine snapshot wins over the conversation\'s model', () => {
    snapshot(SNAP)
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx({ usage: { model: 'Sonnet 5.5', effort: 'low' } })} />)
    expect(screen.getByTestId('item-model').textContent).toBe('Opus 5.5 (1M) · xhigh')
  })

  it('a snapshot older than 10 minutes dims the usage items', () => {
    snapshot(SNAP, NOW - 11 * 60_000)
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    expect(screen.getByTestId('status-seg-usage-context')).toHaveAttribute('data-dim', 'true')
  })

  it('running shows the state with a clock that counts from the running step and ticks', () => {
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx({ status: 'running', items: [step({ status: 'running' })] })} />)
    expect(screen.getByTestId('state-slot').dataset.state).toBe('running')
    expect(screen.getByTestId('state-slot')).toHaveTextContent('0:42')
    act(() => { vi.advanceTimersByTime(3000) })
    expect(screen.getByTestId('state-slot')).toHaveTextContent('0:45')
  })

  it('idle for 29 s then running: the clock starts from the right value at once, not 29 s short', () => {
    const { rerender } = render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    act(() => { vi.advanceTimersByTime(29_000) })
    rerender(<SessionStatusRow sessionCode={CODE} ctx={ctx({ status: 'running', items: [step({ status: 'running', started_at: NOW + 29_000 - 42_000 })] })} />)
    expect(screen.getByTestId('state-slot')).toHaveTextContent('0:42')
  })

  it('failed, denied and exit N come from the newest step', () => {
    const { rerender } = render(<SessionStatusRow sessionCode={CODE} ctx={ctx({ items: [step({ status: 'failed' })] })} />)
    expect(screen.getByTestId('state-slot').dataset.state).toBe('failed')
    rerender(<SessionStatusRow sessionCode={CODE} ctx={ctx({ items: [step({ status: 'denied' })] })} />)
    expect(screen.getByTestId('state-slot').dataset.state).toBe('denied')
    rerender(<SessionStatusRow sessionCode={CODE} ctx={ctx({ items: [step({ status: 'failed', command: { text: 'x', exit_code: 7 } })] })} />)
    expect(screen.getByTestId('state-slot').dataset.state).toBe('exit')
    expect(screen.getByTestId('state-slot')).toHaveTextContent('7')
  })

  it('waiting for the person shows 「Waiting for you」, above a running step and an old failure', () => {
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx({ status: 'waiting', items: [step({ status: 'running' })] })} />)
    expect(screen.getByTestId('state-slot').dataset.state).toBe('waiting')
    expect(screen.getByTestId('state-slot')).toHaveTextContent('Waiting for you')
  })

  it('a new snapshot lands live', () => {
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    expect(screen.getByTestId('item-cost').textContent).toBe('—')
    act(() => snapshot(SNAP))
    expect(screen.getByTestId('item-cost').textContent).toBe('$0.42')
  })
})

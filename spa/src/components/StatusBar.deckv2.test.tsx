// Bottom bar after the deck revision: no cost, the orange model label is the model name only, and the right side reads in the
// same colour as the left.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import { HOST_ID, setupStores, makeTab } from './StatusBar.test-helpers'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))
beforeEach(() => { cleanup(); setupStores() })

describe('StatusBar (deck revision)', () => {
  it('shows no cost and no effort in the model label, whatever the snapshot carries', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useAgentStore.setState({
      lastEvents: { [ck]: { raw_event_name: 'PdxSessionStart', status: 'idle', agent_type: 'cc', broadcast_ts: Date.now(), model: 'Opus 5.5' } },
      statuses: { [ck]: 'idle' }, unread: {}, subagents: {}, models: { [ck]: 'Opus 5.5' },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    act(() => {
      useAgentStore.getState().setCcStatus(HOST_ID, 'dev001', {
        effort: { level: 'xhigh' }, cost: { total_cost_usd: 23.88 }, context_window: { used_percentage: 36 },
        rate_limits: { five_hour: { used_percentage: 25, resets_at: Date.now() / 1000 + 3600 } },
      })
    })
    expect(screen.getByTestId('agent-label').textContent).toBe('Opus 5.5')
    expect(screen.queryByTestId('status-seg-cost')).toBeNull()
    expect(screen.getByTestId('status-usage').textContent).not.toContain('$')
    // the rings stay here
    expect(screen.getByTestId('status-seg-usage-context')).toBeInTheDocument()
    expect(screen.getByTestId('status-seg-usage-five-hour')).toBeInTheDocument()
  })
})

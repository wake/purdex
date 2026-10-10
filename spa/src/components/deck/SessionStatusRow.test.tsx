// The status row under the input: `repo [worktree] [model · effort]` on the left, the cost on the right; no state cell, no rings.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { useAgentStore } from '../../stores/useAgentStore'
import { useHostStore } from '../../stores/useHostStore'
import { SessionStatusRow } from './SessionStatusRow'
import { hostTintBackground } from '../../lib/host-tint'

const H = 'host-1'
const CODE = 'dev001'
const WT = '/Users/wake/Workspace/wake/purdex/.claude/worktrees/iface-u1-3'
const ctx = (usage?: { model?: string; effort?: string }) => ({ hostId: H, usage })
const snapshot = (raw: Record<string, unknown>) => useAgentStore.setState({ ccStatus: { [`${H}:${CODE}`]: { receivedAt: Date.now(), raw } } })
const line = () => screen.getByTestId('status-row-line').textContent

beforeEach(() => { cleanup(); useAgentStore.setState({ ccStatus: {} }) })

describe('SessionStatusRow', () => {
  it('worktree path: `repo [worktree] [model · effort]`, cost on the right', () => {
    snapshot({ model: { display_name: 'Opus 5.5' }, effort: { level: 'xhigh' }, cost: { total_cost_usd: 23.884 } })
    render(<SessionStatusRow sessionCode={CODE} cwd={WT} ctx={ctx()} />)
    expect(line()).toBe('purdex [iface-u1-3] [Opus 5.5 · xhigh]')
    expect(screen.getByTestId('item-cost').textContent).toBe('$23.88')
    const row = screen.getByTestId('status-row')
    expect(row.lastElementChild).toBe(screen.getByTestId('item-cost'))
  })

  it('a path below the worktree root still resolves to the repo and the worktree', () => {
    snapshot({ model: { display_name: 'Opus 5.5' }, workspace: { current_dir: `${WT}/spa` } })
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    expect(line()).toBe('purdex [iface-u1-3] [Opus 5.5]')
  })

  it('a non-worktree cwd shows only its basename, no worktree brackets', () => {
    snapshot({ model: { display_name: 'Sonnet 5.5' }, effort: { level: 'low' } })
    render(<SessionStatusRow sessionCode={CODE} cwd="/Users/wake/Workspace/wake/mlab" ctx={ctx()} />)
    expect(line()).toBe('mlab [Sonnet 5.5 · low]')
  })

  it('a missing effort leaves the model alone; the conversation\'s own model fills in when there is no snapshot', () => {
    render(<SessionStatusRow sessionCode={CODE} cwd={WT} ctx={ctx({ model: 'Opus 5.5' })} />)
    expect(line()).toBe('purdex [iface-u1-3] [Opus 5.5]')
  })

  it('the snapshot\'s own worktree name and branch are shown when it carries them (no other branch source)', () => {
    snapshot({ model: { display_name: 'Opus 5.5' }, workspace: { current_dir: '/x/purdex' }, worktree: { name: 'feat-a', branch: 'feat/a' } })
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    expect(line()).toBe('purdex [feat-a] (feat/a) [Opus 5.5]')
  })

  it('no cost, no cwd, no model: a dash and no cost cell', () => {
    render(<SessionStatusRow sessionCode={CODE} ctx={ctx()} />)
    expect(line()).toBe('—')
    expect(screen.queryByTestId('item-cost')).toBeNull()
  })

  it('reads the snapshot of THIS session only (host + code)', () => {
    useAgentStore.setState({ ccStatus: { [`other:${CODE}`]: { receivedAt: 1, raw: { cost: { total_cost_usd: 9 } } }, [`${H}:zzz999`]: { receivedAt: 1, raw: { cost: { total_cost_usd: 9 } } } } })
    render(<SessionStatusRow sessionCode={CODE} cwd={WT} ctx={ctx()} />)
    expect(screen.queryByTestId('item-cost')).toBeNull()
  })

  it('has no state cell and no usage rings (those live in the bottom bar and the tab light)', () => {
    snapshot({ model: { display_name: 'Opus 5.5' }, cost: { total_cost_usd: 1 }, context_window: { used_percentage: 38 }, rate_limits: { five_hour: { used_percentage: 66, resets_at: 1 } } })
    render(<SessionStatusRow sessionCode={CODE} cwd={WT} ctx={ctx()} />)
    for (const id of ['state-slot', 'status-seg-usage-context', 'status-seg-usage-five-hour', 'status-seg-usage-seven-day', 'item-context']) expect(screen.queryByTestId(id)).toBeNull()
  })

  it('a new snapshot lands live', () => {
    snapshot({ model: { display_name: 'Opus 5.5' } })
    render(<SessionStatusRow sessionCode={CODE} cwd={WT} ctx={ctx()} />)
    expect(screen.queryByTestId('item-cost')).toBeNull()
    act(() => snapshot({ model: { display_name: 'Opus 5.5' }, cost: { total_cost_usd: 2 } }))
    expect(screen.getByTestId('item-cost').textContent).toBe('$2.00')
  })

  it('the row\'s ground is the host colour mixed into the surface; no colour, no tint', () => {
    snapshot({ model: { display_name: 'Opus 5.5' } })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 1, order: 0 } }, hostOrder: [H] })
    const { unmount } = render(<SessionStatusRow sessionCode={CODE} cwd={WT} ctx={ctx()} />)
    expect(screen.getByTestId('status-row').getAttribute('data-host-tinted')).toBeNull()
    expect(screen.getByTestId('status-row')).toHaveClass('text-text-secondary')
    unmount()
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 1, order: 0, color: '#3b82f6' } } })
    render(<SessionStatusRow sessionCode={CODE} cwd={WT} ctx={ctx()} />)
    const row = screen.getByTestId('status-row')
    expect(row.getAttribute('data-host-tinted')).toBe('true')
    expect(row.getAttribute('style')).toContain(hostTintBackground('#3b82f6'))
    expect(row).toHaveClass('text-text-primary')
  })
})

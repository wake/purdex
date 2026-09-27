import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, within, act } from '@testing-library/react'
import WorkerDock from './WorkerDock'
import type { WorkerTask } from '../../lib/nex/types'

const mine = (p: string | undefined) => p === 'pdx:mlab/t-me'

describe('WorkerDock', () => {
  it('shows the live state, observers and lease in one row', () => {
    render(<WorkerDock sse="open" observers={2} lease={{ principal_id: 'pdx:mlab/t-me', expires_at: 1 }} isMine={mine} />)
    const dock = screen.getByTestId('worker-dock')
    // Its own hairline top border: it sits between the transcript and the input.
    expect(dock.className).toMatch(/\bborder-t\b/)
    const row = screen.getByTestId('worker-dock-row')
    expect(row).toHaveTextContent('live · 2 observers · lease: you')
    expect(screen.getByTestId('worker-dock-dot').className).toMatch(/\bbg-status-success\b/)
    expect(screen.queryByTestId('worker-dock-table')).toBeNull()
  })

  it('follows the sse state', () => {
    const { rerender } = render(<WorkerDock sse="idle" observers={0} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent(/^connecting/)
    rerender(<WorkerDock sse="paused" observers={0} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent(/paused/)
    rerender(<WorkerDock sse="closed" observers={0} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent(/disconnected/)
    expect(screen.getByTestId('worker-dock-dot').className).toMatch(/\bbg-status-error\b/)
  })

  it('expands into a table', () => {
    render(<WorkerDock sse="open" observers={2} lease={{ principal_id: 'pdx:mlab/t-other', expires_at: 1 }} isMine={mine} />)
    const toggle = screen.getByTestId('worker-dock-toggle')
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(toggle.getAttribute('aria-label')).toMatch(/expand/i)
    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(toggle.getAttribute('aria-label')).toMatch(/collapse/i)
    const table = screen.getByTestId('worker-dock-table')
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent(/stream.*live/i)
    expect(rows[1]).toHaveTextContent(/observers.*2/i)
    expect(rows[2]).toHaveTextContent(/lease.*pdx:mlab\/t-other/i)
    // The collapsed row gives way to the table.
    expect(screen.queryByTestId('worker-dock-row')).toBeNull()
    fireEvent.click(toggle)
    expect(screen.queryByTestId('worker-dock-table')).toBeNull()
    expect(screen.getByTestId('worker-dock-row')).toBeInTheDocument()
  })

  it('marks the lease as yours', () => {
    render(<WorkerDock sse="open" observers={1} lease={{ principal_id: 'pdx:mlab/t-me', expires_at: 1 }} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent('lease: you')
    fireEvent.click(screen.getByTestId('worker-dock-toggle'))
    // The table carries the full principal plus the marker.
    const lease = within(screen.getByTestId('worker-dock-table')).getAllByRole('row')[2]
    expect(lease).toHaveTextContent('pdx:mlab/t-me (you)')
  })

  it('shows another holder by principal in the row', () => {
    render(<WorkerDock sse="open" observers={1} lease={{ principal_id: 'pdx:mlab/t-other', expires_at: 1 }} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent('lease: pdx:mlab/t-other')
  })

  it('says no lease when there is none', () => {
    render(<WorkerDock sse="open" observers={0} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent('live · 0 observers · no lease')
    fireEvent.click(screen.getByTestId('worker-dock-toggle'))
    expect(within(screen.getByTestId('worker-dock-table')).getAllByRole('row')[2]).toHaveTextContent(/no lease/)
  })
})

// R4 T3.2 (spec §4.6): running background tasks lead the dock.
describe('WorkerDock — running tasks', () => {
  const NOW = 1_800_000_000_000
  const task = (id: string, extra: Partial<WorkerTask> = {}): WorkerTask => ({
    task_id: id, turn_id: 't1', kind: 'shell', task_type: 'local_bash', tool_use_id: `tu_${id}`, parent_tool_use_id: null,
    description: `desc ${id}`, backgrounded: true, status: 'running', provider_status: null, closed_by: null,
    started_at: NOW - 4 * 60_000, ended_at: null, startSeq: 1, ...extra,
  })
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(NOW) })
  afterEach(() => vi.useRealTimers())

  it('no tasks: the row is exactly as before', () => {
    render(<WorkerDock sse="open" observers={0} isMine={mine} tasks={[]} />)
    expect(screen.queryByTestId('worker-dock-tasks')).toBeNull()
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent(/^live · 0 observers · no lease$/)
  })

  it('one task: count, label and elapsed before the other facts', () => {
    render(<WorkerDock sse="open" observers={0} isMine={mine} tasks={[task('a', { command: 'pnpm dev\n--port 1' })]} />)
    const tasks = screen.getByTestId('worker-dock-tasks')
    expect(tasks).toHaveTextContent('1 running')
    // Shell label = its command, first line only.
    expect(within(tasks).getAllByTestId('worker-dock-task')[0]).toHaveTextContent(/^pnpm dev \(4m\)$/)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent('live · 0 observers · no lease')
    expect(tasks.compareDocumentPosition(screen.getByTestId('worker-dock-row')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('three tasks: all listed in order; a subagent is labelled by its description', () => {
    render(<WorkerDock sse="open" observers={0} isMine={mine} tasks={[
      task('a', { command: 'pnpm dev' }),
      task('b', { kind: 'subagent', description: 'explore the repo', started_at: NOW - 30_000 }),
      task('c', { kind: 'other', description: 'watch', started_at: NOW - 2 * 3_600_000 - 5 * 60_000 }),
    ]} />)
    expect(screen.getByTestId('worker-dock-tasks')).toHaveTextContent('3 running')
    expect(screen.getAllByTestId('worker-dock-task').map((el) => el.textContent)).toEqual(['pnpm dev (4m)', 'explore the repo (<1m)', 'watch (2h 05m)'])
  })

  it('elapsed ticks every 30 s while tasks are shown', () => {
    render(<WorkerDock sse="open" observers={0} isMine={mine} tasks={[task('a', { command: 'x', started_at: NOW - 50_000 })]} />)
    expect(screen.getByTestId('worker-dock-task')).toHaveTextContent('x (<1m)')
    act(() => { vi.advanceTimersByTime(30_000) })
    expect(screen.getByTestId('worker-dock-task')).toHaveTextContent('x (1m)')
  })

  it('no interval runs without tasks, and it stops when they are gone', () => {
    const { rerender, unmount } = render(<WorkerDock sse="open" observers={0} isMine={mine} tasks={[]} />)
    expect(vi.getTimerCount()).toBe(0)
    rerender(<WorkerDock sse="open" observers={0} isMine={mine} tasks={[task('a')]} />)
    expect(vi.getTimerCount()).toBe(1)
    rerender(<WorkerDock sse="open" observers={0} isMine={mine} tasks={[]} />)
    expect(vi.getTimerCount()).toBe(0)
    rerender(<WorkerDock sse="open" observers={0} isMine={mine} tasks={[task('a')]} />)
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('expanded: one row per task with kind icon, full label, elapsed and inspect', () => {
    const onInspect = vi.fn()
    render(<WorkerDock sse="open" observers={0} isMine={mine} onInspect={onInspect} tasks={[
      task('a', { command: 'pnpm dev --port 5174' }),
      task('b', { kind: 'subagent', description: 'explore', tool_use_id: null }),
    ]} />)
    fireEvent.click(screen.getByTestId('worker-dock-toggle'))
    const rows = screen.getAllByTestId('worker-dock-task-row')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('pnpm dev --port 5174')
    expect(rows[0]).toHaveTextContent('4m')
    expect(within(rows[0]).getByTestId('worker-dock-task-icon').getAttribute('data-kind')).toBe('shell')
    expect(within(rows[1]).getByTestId('worker-dock-task-icon').getAttribute('data-kind')).toBe('subagent')
    fireEvent.click(within(rows[0]).getByTestId('worker-dock-inspect'))
    expect(onInspect).toHaveBeenCalledWith('tu_a')
    // No tool_use_id → nothing to scroll to → no button.
    expect(within(rows[1]).queryByTestId('worker-dock-inspect')).toBeNull()
    // The fact table still follows.
    expect(screen.getByTestId('worker-dock-table')).toBeInTheDocument()
  })
})

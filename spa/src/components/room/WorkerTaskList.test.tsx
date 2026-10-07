import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, within, act } from '@testing-library/react'
import WorkerTaskList from './WorkerTaskList'
import type { WorkerTask } from '../../lib/nex/types'

const NOW = 1_800_000_000_000
const task = (id: string, extra: Partial<WorkerTask> = {}): WorkerTask => ({
  task_id: id, turn_id: 't1', kind: 'shell', task_type: 'local_bash', tool_use_id: `tu_${id}`, parent_tool_use_id: null,
  description: `desc ${id}`, backgrounded: true, status: 'running', provider_status: null, closed_by: null,
  started_at: NOW - 4 * 60_000, ended_at: null, startSeq: 1, ...extra,
})

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(NOW) })
afterEach(() => vi.useRealTimers())

describe('WorkerTaskList', () => {
  it('draws nothing with no tasks — not even the main row', () => {
    const { container } = render(<WorkerTaskList tasks={[]} mainRunning />)
    expect(container).toBeEmptyDOMElement()
  })

  it('a terminal-style row per task: marker, name, text, elapsed', () => {
    render(<WorkerTaskList mainRunning tasks={[
      task('a', { kind: 'subagent', subagent_type: 'general-purpose', description: 'Running mutation tests…' }),
    ]} />)
    const row = screen.getByTestId('worker-task-row')
    expect(row).toHaveTextContent('●general-purposeRunning mutation tests…(4m)')
    expect(within(row).getByTestId('worker-task-marker')).toHaveTextContent('●')
  })

  it('a shell task is named bash and shows its command, first line only', () => {
    render(<WorkerTaskList mainRunning={false} tasks={[task('a', { command: 'pnpm dev\n--port 1' })]} />)
    expect(screen.getByTestId('worker-task-name')).toHaveTextContent('bash')
    expect(screen.getByTestId('worker-task-text')).toHaveTextContent(/^pnpm dev$/)
  })

  it('the main row leads and reflects the main agent', () => {
    const { rerender } = render(<WorkerTaskList mainRunning tasks={[task('a')]} />)
    const rows = () => screen.getByTestId('worker-task-list').querySelectorAll('li')
    expect(rows()[0]).toBe(screen.getByTestId('worker-task-main'))
    expect(screen.getByTestId('worker-task-main')).toHaveTextContent('●mainrunning')
    expect(screen.getByTestId('worker-task-main').dataset.running).toBe('true')
    rerender(<WorkerTaskList mainRunning={false} tasks={[task('a')]} />)
    expect(screen.getByTestId('worker-task-main')).toHaveTextContent('○mainidle')
    expect(screen.getByTestId('worker-task-main').dataset.running).toBe('false')
  })

  it('running rows are ●, ended rows a dimmed ○ with the time they took', () => {
    render(<WorkerTaskList mainRunning={false} tasks={[
      task('a'),
      task('b', { status: 'completed', started_at: NOW - 10 * 60_000, ended_at: NOW - 7 * 60_000 }),
    ]} />)
    const [a, b] = screen.getAllByTestId('worker-task-row')
    expect(a.dataset.running).toBe('true')
    expect(within(a).getByTestId('worker-task-marker')).toHaveTextContent('●')
    expect(b.dataset.running).toBe('false')
    expect(within(b).getByTestId('worker-task-marker')).toHaveTextContent('○')
    expect(b.className).toMatch(/opacity-60/)
    expect(within(b).getByTestId('worker-task-elapsed')).toHaveTextContent('(3m)')
  })

  it('stays out of the transcript\'s way: shrink-0, max height, its own scroll', () => {
    render(<WorkerTaskList mainRunning tasks={[task('a')]} />)
    const list = screen.getByTestId('worker-task-list')
    expect(list.className).toMatch(/\bshrink-0\b/)
    expect(list.className).toMatch(/\bmax-h-/)
    expect(list.className).toMatch(/\boverflow-y-auto\b/)
  })

  it('inspect calls back with the tool_use id; a row with none has no button', () => {
    const onInspect = vi.fn()
    render(<WorkerTaskList mainRunning onInspect={onInspect} tasks={[task('a'), task('b', { tool_use_id: null })]} />)
    const [a, b] = screen.getAllByTestId('worker-task-row')
    fireEvent.click(within(a).getByTestId('worker-task-inspect'))
    expect(onInspect).toHaveBeenCalledWith('tu_a')
    expect(within(b).queryByTestId('worker-task-inspect')).toBeNull()
  })

  it('no inspect buttons without a handler', () => {
    render(<WorkerTaskList mainRunning tasks={[task('a')]} />)
    expect(screen.queryByTestId('worker-task-inspect')).toBeNull()
  })

  it('elapsed ticks every 30 s while something runs', () => {
    render(<WorkerTaskList mainRunning tasks={[task('a', { started_at: NOW - 50_000 })]} />)
    expect(screen.getByTestId('worker-task-elapsed')).toHaveTextContent('(<1m)')
    act(() => { vi.advanceTimersByTime(30_000) })
    expect(screen.getByTestId('worker-task-elapsed')).toHaveTextContent('(1m)')
  })

  it('no interval runs when nothing is running or listed, and it stops with the last running task', () => {
    const { rerender, unmount } = render(<WorkerTaskList mainRunning={false} tasks={[]} />)
    expect(vi.getTimerCount()).toBe(0)
    rerender(<WorkerTaskList mainRunning={false} tasks={[task('a', { status: 'completed', ended_at: NOW })]} />)
    expect(vi.getTimerCount()).toBe(0)
    rerender(<WorkerTaskList mainRunning={false} tasks={[task('a')]} />)
    expect(vi.getTimerCount()).toBe(1)
    rerender(<WorkerTaskList mainRunning={false} tasks={[task('a', { status: 'completed', ended_at: NOW })]} />)
    expect(vi.getTimerCount()).toBe(0)
    rerender(<WorkerTaskList mainRunning={false} tasks={[task('a')]} />)
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})

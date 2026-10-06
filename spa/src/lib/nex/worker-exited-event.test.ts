import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useI18nStore } from '../../stores/useI18nStore'
import { emptyListCache } from './execution-list-effects'
import type { ExecutionSummary } from './types'
import { handleWorkerExited, parseWorkerExited, workerExitedName, type WorkerExitedEvent } from './worker-exited-event'

const rowOf = (p: Partial<ExecutionSummary> & { id: string }) => ({ brief: '', ...p }) as ExecutionSummary
const cacheOf = (items: ExecutionSummary[]) => ({ ...emptyListCache(), items })
const ev = (executionId: string, tmuxSession = 'proj-2'): WorkerExitedEvent =>
  ({ executionId, sessionId: 'S', reason: 'manual_resume', tmuxSession })
const value = (o: Record<string, unknown>) =>
  JSON.stringify({ execution_id: 'E9', session_id: 'S', reason: 'manual_resume', tmux_session: 'proj-2', ...o })

const refetch = vi.fn()
const original = useExecutionListStore.getState().refetch
beforeEach(() => {
  refetch.mockClear()
  useExecutionListStore.setState({ byHost: {}, refetch })
  useUndoToast.setState({ toast: null, notice: null })
})
afterEach(() => useExecutionListStore.setState({ byHost: {}, refetch: original }))

describe('nex-worker-exited', () => {
  it('parses the daemon value (JSON text)', () => {
    expect(parseWorkerExited(value({ execution_id: 'E1' })))
      .toEqual({ executionId: 'E1', sessionId: 'S', reason: 'manual_resume', tmuxSession: 'proj-2' })
    expect(parseWorkerExited({ execution_id: 'E1' })).toMatchObject({ executionId: 'E1', tmuxSession: '' })
    expect(parseWorkerExited('nope')).toBeNull()
    expect(parseWorkerExited('{"session_id":"S"}')).toBeNull()
  })

  it('names the worker by its list row, else the tmux session, else the execution id', () => {
    useExecutionListStore.setState({
      byHost: { h1: cacheOf([rowOf({ id: 'E1', session_title: { text: '修 bug', source: 'ai' }, brief: 'x' })]) },
    })
    expect(workerExitedName('h1', ev('E1'))).toBe('修 bug')
    expect(workerExitedName('h1', ev('E9'))).toBe('proj-2')
    expect(workerExitedName('h1', ev('E9', ''))).toBe('E9')
  })

  it('prefers the row label even when tmux_session is empty', () => {
    useExecutionListStore.setState({ byHost: { h1: cacheOf([rowOf({ id: 'E1', brief: 'do it\nmore' })]) } })
    expect(workerExitedName('h1', ev('E1', ''))).toBe('do it')
  })

  it('toasts and refetches', () => {
    handleWorkerExited('h1', value({}))
    const t = useI18nStore.getState().t
    expect(useUndoToast.getState().toast?.message).toBe(t('worker.exit.manual_resume', { name: 'proj-2' }))
    expect(refetch).toHaveBeenCalledWith('h1')
  })

  it('falls back to the execution id when tmux_session is empty and no row exists', () => {
    handleWorkerExited('h1', value({ tmux_session: '' }))
    const t = useI18nStore.getState().t
    expect(useUndoToast.getState().toast?.message).toBe(t('worker.exit.manual_resume', { name: 'E9' }))
  })

  it('refetches without a toast for any other reason', () => {
    handleWorkerExited('h1', value({ reason: 'something_else' }))
    expect(useUndoToast.getState().toast).toBeNull()
    expect(refetch).toHaveBeenCalledWith('h1')
  })

  it('ignores an unparseable value', () => {
    handleWorkerExited('h1', 'nope')
    expect(useUndoToast.getState().toast).toBeNull()
    expect(refetch).not.toHaveBeenCalled()
  })
})

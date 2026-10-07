import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore, selectSessionTitleSupported } from '../../stores/useNexHostStore'
import { workerRowName } from './worker-row-name'
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

const withTitleCap = (hostId: string, on: boolean) =>
  useNexHostStore.setState({
    byHost: { [hostId]: { phase: 'ready', capabilities: on ? { session_title: {} } : {} } },
  } as never)

const refetch = vi.fn()
const original = useExecutionListStore.getState().refetch
beforeEach(() => {
  refetch.mockClear()
  useExecutionListStore.setState({ byHost: {}, refetch })
  useUndoToast.setState({ toast: null, notice: null })
})
afterEach(() => {
  useExecutionListStore.setState({ byHost: {}, refetch: original })
  useNexHostStore.setState({ byHost: {} } as never)
})

describe('nex-worker-exited', () => {
  it('parses the daemon value (JSON text)', () => {
    expect(parseWorkerExited(value({ execution_id: 'E1' })))
      .toEqual({ executionId: 'E1', sessionId: 'S', reason: 'manual_resume', tmuxSession: 'proj-2' })
    expect(parseWorkerExited({ execution_id: 'E1' })).toMatchObject({ executionId: 'E1', tmuxSession: '' })
    expect(parseWorkerExited('{"session_id":"S"}')).toBeNull()
  })

  it('a value that is not JSON logs a debug line naming the event, never the value', () => {
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {})
    try {
      expect(parseWorkerExited('nope')).toBeNull()
      expect(parseWorkerExited('{"execution_id":"E1","secret')).toBeNull()
      expect(debug).toHaveBeenCalledTimes(2)
      const line = debug.mock.calls[1].map(String).join(' ')
      expect(line).toContain('nex-worker-exited')
      expect(line).not.toContain('secret')
    } finally {
      debug.mockRestore()
    }
  })

  it('names the worker like its list row: the brief wins over a title (rewritten: was title-first)', () => {
    withTitleCap('h1', true)
    useExecutionListStore.setState({
      byHost: { h1: cacheOf([rowOf({ id: 'E1', session_title: { text: '修 bug', source: 'ai' }, brief: 'x' })]) },
    })
    expect(workerExitedName('h1', ev('E1'))).toBe('x')
  })

  it('an empty brief falls to the title with the capability, to the cwd basename without it', () => {
    const row = rowOf({ id: 'E1', brief: '', cwd: '/a/proj', session_title: { text: '修 bug', source: 'ai' } })
    useExecutionListStore.setState({ byHost: { h1: cacheOf([row]) } })
    withTitleCap('h1', true)
    expect(workerExitedName('h1', ev('E1'))).toBe('修 bug')
    withTitleCap('h1', false)
    expect(workerExitedName('h1', ev('E1'))).toBe('proj')
  })

  it('a row with nothing to show, or no row, falls to the tmux session then the execution id', () => {
    useExecutionListStore.setState({ byHost: { h1: cacheOf([rowOf({ id: 'E1' })]) } })
    expect(workerExitedName('h1', ev('E1'))).toBe('proj-2')
    expect(workerExitedName('h1', ev('E1', ''))).toBe('E1')
    expect(workerExitedName('h1', ev('E9'))).toBe('proj-2')
    expect(workerExitedName('h1', ev('E9', ''))).toBe('E9')
  })

  it.each([
    [{ brief: 'b\nmore', cwd: '/x/y', session_title: { text: 't', source: 'ai' } }],
    [{ brief: '', cwd: '/x/y', session_title: { text: 't', source: 'ai' } }],
    [{ brief: ' \n ', cwd: '/x/y' }],
    [{ brief: '', session_title: { text: 'only title', source: 'ai' } }],
  ])('toast name equals workerRowName for %j under both capabilities', (p) => {
    const row = rowOf({ id: 'E1', ...p } as never)
    useExecutionListStore.setState({ byHost: { h1: cacheOf([row]) } })
    for (const on of [true, false]) {
      withTitleCap('h1', on)
      const want = workerRowName(row, selectSessionTitleSupported('h1')(useNexHostStore.getState())) || 'proj-2'
      expect(workerExitedName('h1', ev('E1'))).toBe(want)
    }
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

  it.each([[42], [null], [{ id: 'E1' }], [['E1']], [true], ['']])('a non-string or empty execution_id (%j) is ignored: no toast, no refetch', (id) => {
    expect(parseWorkerExited({ execution_id: id, reason: 'manual_resume' })).toBeNull()
    expect(parseWorkerExited(JSON.stringify({ execution_id: id, reason: 'manual_resume' }))).toBeNull()
    handleWorkerExited('h1', value({ execution_id: id }))
    expect(useUndoToast.getState().toast).toBeNull()
    expect(refetch).not.toHaveBeenCalled()
  })
})

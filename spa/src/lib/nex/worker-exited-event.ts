// spa/src/lib/nex/worker-exited-event.ts — the daemon's `nex-worker-exited`
// host event: a worker was exited because its conversation was resumed by hand
// in a terminal (Q1). Toast it, then refetch the host's list.
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { workerLabel } from './worker-label'

export interface WorkerExitedEvent { executionId: string; sessionId: string; reason: string; tmuxSession: string }

const str = (v: unknown): string => (typeof v === 'string' ? v : '')

/** The event's `value`: JSON text or an object; null unless it has a string execution_id. */
export function parseWorkerExited(value: unknown): WorkerExitedEvent | null {
  let o: unknown = value
  if (typeof value === 'string') {
    try { o = JSON.parse(value) } catch { return null }
  }
  if (!o || typeof o !== 'object') return null
  const r = o as Record<string, unknown>
  if (typeof r.execution_id !== 'string' || r.execution_id === '') return null
  return { executionId: r.execution_id, sessionId: str(r.session_id), reason: str(r.reason), tmuxSession: str(r.tmux_session) }
}

/**
 * The list row's label when the host's list has the row; else the tmux session
 * (empty on the daemon's re-check / retry / overflow paths); else the execution id.
 */
export function workerExitedName(hostId: string, ev: WorkerExitedEvent): string {
  const row = useExecutionListStore.getState().byHost[hostId]?.items.find((r) => r.id === ev.executionId)
  if (row) return workerLabel(row)
  return ev.tmuxSession || ev.executionId
}

/** Toast (manual_resume only) + refetch the host's list (any reason). */
export function handleWorkerExited(hostId: string, value: unknown): void {
  const ev = parseWorkerExited(value)
  if (!ev) return
  if (ev.reason === 'manual_resume') {
    const t = useI18nStore.getState().t
    useUndoToast.getState().show(t('worker.exit.manual_resume', { name: workerExitedName(hostId, ev) }))
  }
  useExecutionListStore.getState().refetch(hostId)
}

// spa/src/components/executions/useRowExit.tsx — the 退出 action of a worker list row, shared by the activity
// bar list (`ExecutionsView`) and `HostWorkerRows` (New Tab / Settings). A running worker is confirmed first;
// any other live worker exits at once. Exits on their way are tracked per `${hostId}:${executionId}` so the
// row's button stays disabled until the row is gone (or no longer live) from the list.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { ConfirmDialog } from '../ConfirmDialog'
import { exitWorker, exitErrorMessage } from '../../lib/nex/exit-worker'
import { executionKey, splitExecutionKey } from '../../stores/useExecutionStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import type { ExecutionSummary } from '../../lib/nex/types'

export interface RowExit {
  /** Hand to `ExecutionRowCompact`'s `onExit` (via the row). */
  requestExit: (row: ExecutionSummary) => void
  /** Execution ids (of this host) whose exit is on its way. */
  pendingIds: ReadonlySet<string>
  /** The running-worker confirm dialog, or null; render it once in the view. */
  dialog: ReactNode
}

/** `live` is the host's currently listed live rows (what pending ids and the confirm are judged against). */
export function useRowExit(hostId: string, live: readonly ExecutionSummary[]): RowExit {
  const t = useI18nStore((s) => s.t)
  const [confirmExitId, setConfirmExitId] = useState<string | null>(null)
  // The ref is the same-tick guard; the state renders the disabled button. A failure frees the id at once;
  // a success keeps it until the row is gone (or no longer live) from the list.
  const pendingRef = useRef<Set<string>>(new Set())
  const [pending, setPending] = useState<ReadonlySet<string>>(() => new Set())
  const setPendingIds = useCallback((next: Set<string>) => { pendingRef.current = next; setPending(next) }, [])

  useEffect(() => {
    if (pendingRef.current.size === 0) return
    // Only this host's keys are judged against this host's list; other hosts' keys are left alone.
    const liveKeys = new Set(live.map((r) => executionKey(hostId, r.id)))
    const kept = new Set([...pendingRef.current].filter((x) => splitExecutionKey(x).hostId !== hostId || liveKeys.has(x)))
    if (kept.size !== pendingRef.current.size) setPendingIds(kept)
  }, [live, hostId, setPendingIds])

  const pendingIds = useMemo(
    () => new Set([...pending].map(splitExecutionKey).filter((k) => k.hostId === hostId).map((k) => k.executionId)),
    [pending, hostId],
  )

  const runExit = async (executionId: string) => {
    const pKey = executionKey(hostId, executionId)
    if (pendingRef.current.has(pKey)) return
    setPendingIds(new Set(pendingRef.current).add(pKey))
    let keep = false
    try {
      const result = await exitWorker({ hostId, executionId })
      keep = result.exited !== false
    } catch (err) {
      useUndoToast.getState().show(exitErrorMessage(err, t))
    } finally {
      if (!keep) {
        const next = new Set(pendingRef.current)
        next.delete(pKey)
        setPendingIds(next)
      }
    }
  }

  const requestExit = (r: ExecutionSummary) => {
    if (r.state === 'running') setConfirmExitId(r.id)
    else void runExit(r.id)
  }

  const dialog = confirmExitId !== null ? (
    <ConfirmDialog testIdPrefix="exit" title={t('worker.exit.confirm_title')} body={t('worker.exit.confirm_running')}
      confirmLabel={t('worker.exit.button')} onCancel={() => setConfirmExitId(null)}
      onConfirm={() => { const eid = confirmExitId; setConfirmExitId(null); if (live.some((r) => r.id === eid)) void runExit(eid) }} />
  ) : null

  return { requestExit, pendingIds, dialog }
}

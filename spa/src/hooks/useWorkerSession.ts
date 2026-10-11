// spa/src/hooks/useWorkerSession.ts — what a view of one worker (the deck's WorkerDeckPane, as deck or chat) needs to be live on it: the SSE
// subscription, the lease (held while a permission request waits), the permission answer path, the host's capabilities and the cleanup of the
// reply box's draft memory once the worker has ended.
import { useEffect, useMemo } from 'react'
import { executionKey, useExecutionStore } from '../stores/useExecutionStore'
import { defaultExecutionState } from '../lib/nex/event-reducer'
import { useNexHostStore } from '../stores/useNexHostStore'
import { selectPendingPermission } from '../lib/nex/permissions'
import { workerEndedKind } from '../components/execution/WorkerEndedPane'
import { forgetWorkerDraft, workerDraftKey } from '../lib/nex/worker-draft-memory'
import { prunePermissionCards } from '../lib/nex/permission-card-memory'
import { useExecutionSubscription } from './useExecutionSubscription'
import { useExecutionLease } from './useExecutionLease'
import { usePermissionAnswer } from './usePermissionAnswer'

const EMPTY = defaultExecutionState()

export function useWorkerSession(hostId: string, executionId: string, isActive: boolean) {
  const key = executionKey(hostId, executionId)
  const st = useExecutionStore((s) => s.executions[key] ?? EMPTY)
  const { problem } = useExecutionSubscription(hostId, executionId, isActive)
  // Permission channel plan Task 9: while the worker waits on a request the
  // lease is held (no idle lapse) — the answer needs it, and may come hours
  // later. The card shows the earliest request this pane has not closed itself.
  const permissions = st.permissions
  const pendingPermission = useMemo(() => selectPendingPermission({ permissions }), [permissions])
  const lease = useExecutionLease(hostId, executionId, { hold: pendingPermission !== undefined })
  const permission = usePermissionAnswer(hostId, executionId, lease)
  const shownPermission = useMemo(() => selectPendingPermission({ permissions }, permission.closed), [permissions, permission.closed])
  useEffect(() => { void useNexHostStore.getState().ensure(hostId) }, [hostId])
  // The reply box's text outlives the pane (the input writes it through the worker adapter's draft memory); it goes when the worker has ended.
  const draftKey = workerDraftKey(hostId, executionId)
  const workerEnded = !!st.summary && !!workerEndedKind(st.summary)
  useEffect(() => { if (workerEnded) forgetWorkerDraft(draftKey) }, [workerEnded, draftKey])
  // The request card's note / open / expanded outlive a tab switch the same way (permission-card-memory), and so
  // does "this pane closed it" (an answer whose resolution has not arrived yet — closing drops the draft itself).
  // One rule drops both: a request the store no longer lists as pending — resolved, cancelled, expired — and every
  // request of an ended worker. Only once the history is in: before that the table may not have replayed a request
  // yet, and a closed request dropped then would come back as a card when it does.
  useEffect(() => {
    if (workerEnded) prunePermissionCards(hostId, executionId)
    else if (st.historyLoaded) prunePermissionCards(hostId, executionId, (id) => permissions[id]?.status === 'pending')
  }, [hostId, executionId, workerEnded, st.historyLoaded, permissions])
  return { key, st, problem, lease, permission, shownPermission, workerEnded }
}

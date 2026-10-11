// spa/src/hooks/useWorkerSession.ts — what any view of one worker (the room / chat ExecutionView, the deck's WorkerDeckPane) needs to
// be live on it: the SSE subscription, the lease (held while a permission request waits), the permission answer path, the host's
// capabilities, the reply box's draft memory and the attachment uploads. Moved out of ExecutionView unchanged (U4-1b): one worker
// view is mounted at a time, and both call this.
import { useCallback, useEffect, useMemo, useRef } from 'react'
import { executionKey, useExecutionStore } from '../stores/useExecutionStore'
import { defaultExecutionState } from '../lib/nex/event-reducer'
import { selectImageAttachments, useNexHostStore } from '../stores/useNexHostStore'
import { selectPendingPermission } from '../lib/nex/permissions'
import { workerEndedKind } from '../components/execution/WorkerEndedPane'
import { forgetWorkerDraft, workerDraftKey, writeWorkerDraft } from '../lib/nex/worker-draft-memory'
import { prunePermissionCards } from '../lib/nex/permission-card-memory'
import { useExecutionSubscription } from './useExecutionSubscription'
import { useExecutionLease } from './useExecutionLease'
import { usePermissionAnswer } from './usePermissionAnswer'
import { useWorkerUploads } from './useWorkerUploads'

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
  // Spec §9.2 (phase E): native images only when the host's capability
  // exists AND lists this execution's provider — null otherwise (an older
  // daemon, a codex execution, the summary not in yet), and then every image
  // goes by path exactly as in phase D. The result is reference-stable.
  const imageCaps = useNexHostStore(selectImageAttachments(hostId, st.summary?.provider ?? ''))
  useEffect(() => { void useNexHostStore.getState().ensure(hostId) }, [hostId])
  // The typed text, for planning an image added mid-draft against the request budget.
  const draftText = useRef('')
  // The reply box's text outlives this component: a tab switch unmounts the
  // pane (the alive pool keeps no execution tab by default), and the box would
  // come back empty. Written on every change, read as the initial value; a
  // restored failed-send `draft` still wins (it is the text that just failed).
  const draftKey = workerDraftKey(hostId, executionId)
  const onTextChange = useCallback((text: string) => { draftText.current = text; writeWorkerDraft(draftKey, text) }, [draftKey])
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
  const getDraftText = useCallback(() => draftText.current, [])
  // Spec §9.1: attachments. The chips live here, not in WorkerInput — the
  // input is re-keyed on the restored draft and remounts after a failed send —
  // and the whole pane is the drop target (TerminalView's overlay pattern).
  const uploads = useWorkerUploads(hostId, executionId, { caps: imageCaps, getText: getDraftText })
  return { key, st, problem, lease, permission, shownPermission, imageCaps, draftKey, onTextChange, uploads, workerEnded }
}

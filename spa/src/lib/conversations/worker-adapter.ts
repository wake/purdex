// spa/src/lib/conversations/worker-adapter.ts — the worker's (a Nexen execution's) deck adapter (U4-2, spec §3.4): a message goes out under the
// worker pane's control lease through `POST /v1/executions/{id}/messages` (Nexen queues it itself while a turn runs, so the App never holds one for
// idle: the send queue only gives its 3 s undo window and then waits for the transcript's echo), interrupt is Nexen's, a file is saved by the
// worker upload route (its path goes into the text like a terminal's attachment), and the draft is the worker's own memory (`worker-draft-memory`,
// shared with the room view).
//
// The lease belongs to the mounted worker view (`useWorkerSession`): the view registers it here while it is mounted and the port reads it at call
// time. A message whose undo window ends after the view went away (a tab switch) finds none and is refused — shown as failed, never lost silently.
import { interruptExecution, sendMessage, uploadWorkerFile } from '../nex/nex-api'
import { NexApiError } from '../nex/types'
import { forgetWorkerDraft, readWorkerDraft, workerDraftKey, writeWorkerDraft } from '../nex/worker-draft-memory'
import type { DeckAdapter } from './deck-adapter'
import type { SendOutcome } from './send'

/** What the port needs of the worker's lease (`ExecutionLeaseApi`). */
export interface WorkerLease {
  ensureLease(): Promise<string>
  forget(): void
}

const leases = new Map<string, WorkerLease>()

/** The mounted worker view of `hostId` + `executionId` offers its lease; the returned function takes it back (only if it is still this one). */
export function registerWorkerLease(hostId: string, executionId: string, lease: WorkerLease): () => void {
  const key = workerDraftKey(hostId, executionId)
  leases.set(key, lease)
  return () => { if (leases.get(key) === lease) leases.delete(key) }
}

/** The lease codes the daemon answers when the lease this client holds is no longer valid (it is dropped locally, the next send re-acquires). */
const STALE_LEASE = new Set(['lease_expired', 'lease_mismatch', 'lease_required'])

function outcomeOf(e: unknown, lease: WorkerLease | undefined): SendOutcome {
  if (!(e instanceof NexApiError)) return { kind: 'network' }
  if (STALE_LEASE.has(e.code)) lease?.forget()
  // a request that never reached the daemon was not run: a plain refusal the person may retry; anything the daemon answered is refused too
  return { kind: 'rejected', status: e.status, code: e.code }
}

export function workerAdapter(hostId: string, executionId: string): DeckAdapter {
  const draftKey = workerDraftKey(hostId, executionId)
  return {
    kind: 'worker',
    port: () => ({
      async submit(text) {
        const lease = leases.get(draftKey)
        if (!lease) return { kind: 'rejected', status: 0, code: 'pane_closed' }
        try {
          await sendMessage(hostId, executionId, await lease.ensureLease(), text)
          return { kind: 'accepted' }
        } catch (e) {
          return outcomeOf(e, lease)
        }
      },
      async interrupt() {
        const lease = leases.get(draftKey)
        if (!lease) return { kind: 'rejected', status: 0, code: 'pane_closed' }
        try {
          await interruptExecution(hostId, executionId, await lease.ensureLease())
          return { kind: 'accepted' }
        } catch (e) {
          return outcomeOf(e, lease)
        }
      },
    }),
    upload: async (file) => ({ path: (await uploadWorkerFile(hostId, executionId, file)).path }),
    draft: { read: () => readWorkerDraft(draftKey), write: (text) => writeWorkerDraft(draftKey, text), forget: () => forgetWorkerDraft(draftKey) },
  }
}

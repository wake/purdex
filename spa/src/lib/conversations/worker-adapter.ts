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
import { SUBMIT_TIMEOUT_MS, type SendOutcome } from './send'

/** What the port needs of the worker's lease (`ExecutionLeaseApi`). */
export interface WorkerLease {
  ensureLease(): Promise<string>
  forget(): void
}

// A stack per worker: two views of one execution (a split) each offer theirs, the newest answers, and the one left answers when the newest goes.
const leases = new Map<string, WorkerLease[]>()
const leaseOf = (key: string): WorkerLease | undefined => leases.get(key)?.at(-1)

/** The mounted worker view of `hostId` + `executionId` offers its lease; the returned function takes it back. */
export function registerWorkerLease(hostId: string, executionId: string, lease: WorkerLease): () => void {
  const key = workerDraftKey(hostId, executionId)
  leases.set(key, [...(leases.get(key) ?? []), lease])
  return () => {
    const rest = (leases.get(key) ?? []).filter((l) => l !== lease)
    if (rest.length === 0) leases.delete(key)
    else leases.set(key, rest)
  }
}

/** Tests only: module state outlives a test. */
export const clearAllWorkerLeases = (): void => { leases.clear() }

/** The lease codes the daemon answers when the lease this client holds is no longer valid (it is dropped locally, the next send re-acquires). */
const STALE_LEASE = new Set(['lease_expired', 'lease_mismatch', 'lease_required'])

/**
 * Nexen's answer is a refusal with its code. A request that got NO answer (the connection dropped, it timed out) may have run: `network`, which the
 * queue leaves `maybe` — the person confirms before it is sent again, since Nexen would take it twice.
 */
function outcomeOf(e: unknown, lease: WorkerLease | undefined): SendOutcome {
  if (!(e instanceof NexApiError) || e.code === 'network') return { kind: 'network' }
  if (STALE_LEASE.has(e.code)) lease?.forget()
  return { kind: 'rejected', status: e.status, code: e.code }
}

/** A request with no answer for this long is let go (its message is `maybe`), so one hung request cannot hold every message behind it. */
function within<T>(p: Promise<T>): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('timeout')), SUBMIT_TIMEOUT_MS)
    p.then((v) => { clearTimeout(timer); resolve(v) }, (e) => { clearTimeout(timer); reject(e) })
  })
}

/** How a worker's message comes back in the transcript: whenever Nexen hands it to the worker (a busy worker's queue holds it for the turn), as `user` or `queued`. */
const ECHO = { sources: ['user', 'queued'], afterMs: 10 * 60_000 } as const

export function workerAdapter(hostId: string, executionId: string): DeckAdapter {
  const draftKey = workerDraftKey(hostId, executionId)
  return {
    kind: 'worker',
    // no mod to wait for: Nexen answers for itself, and says so when it cannot (an older daemon reports no capability for a worker at all)
    canSend: () => true,
    recall: true, // the 3 s undo window is the App's: Up and Undo bring it back
    port: () => ({
      echo: ECHO,
      async submit(text) {
        const lease = leaseOf(draftKey)
        if (!lease) return { kind: 'rejected', status: 0, code: 'pane_closed' }
        try {
          await within((async () => sendMessage(hostId, executionId, await lease.ensureLease(), text))())
          return { kind: 'accepted' }
        } catch (e) {
          return outcomeOf(e, lease)
        }
      },
      async interrupt() {
        const lease = leaseOf(draftKey)
        if (!lease) return { kind: 'rejected', status: 0, code: 'pane_closed' }
        try {
          await within((async () => interruptExecution(hostId, executionId, await lease.ensureLease()))())
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

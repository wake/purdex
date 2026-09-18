// spa/src/components/hosts/peers/flow.ts — what the Peers page's write flows
// share: the one-at-a-time flow state the page shows under the affected row
// or candidate (plan Task 6), and the handler-local `ActionApi` table.
//
// Nothing here touches a host-api export at module load: tests that mock all
// of host-api without importOriginal (MemoryMonitorDisabled.test.tsx) import
// the module registry, and this file is reachable from it.
import {
  HostApiError, addPeerHost, commitRotation, deletePeerHost, listPeerHosts, rotatePeerHost, updatePeerHost, verifyPeerHost,
} from '../../../lib/host-api'
import type { ActionApi, FlowStep, Report } from '../../../lib/peer-pairing-actions'

/**
 * The single in-flight or last-finished flow. `step` is what the flow is on
 * while `running`; `error`/`hint` are what it left behind and stay until the
 * next flow on any key. Never carries a token (spec D-8).
 */
export interface FlowState {
  key: string
  step: FlowStep | null
  running: boolean
  error: string
  hint: string
}

/** What a flow leaves for the page to show. `key` re-homes the note (a pair that created a row reports under that row). */
export interface FlowResult {
  error?: string
  hint?: string
  key?: string
}

/** Runs one flow under `key`, then refreshes the page (spec §7.4). */
export type RunFlow = (key: string, fn: (report: Report) => Promise<FlowResult>) => Promise<void>
/** `RunFlow` with the key already bound — what a row or candidate hands its controls. */
export type BoundRunFlow = (fn: (report: Report) => Promise<FlowResult>) => Promise<void>

export const rowKey = (alias: string) => `row:${alias}`
export const candidateKey = (hostId: string) => `cand:${hostId}`
/** The self-alias editor's flow (self alias spec §4.3): owned by the `peers-self` line, never an orphan. */
export const selfKey = 'self'

/** Built inside a handler, per call — never at module load. No `cancel` member: no flow cancels. */
export function actionApi(): ActionApi {
  return {
    add: addPeerHost,
    update: updatePeerHost,
    delete: deletePeerHost,
    rotate: rotatePeerHost,
    list: listPeerHosts,
    verify: verifyPeerHost,
    commit: commitRotation,
  }
}

/** The daemon's own `{error}` text when there is one. */
export const errText = (e: unknown): string =>
  (e instanceof HostApiError ? e.detail : e instanceof Error ? e.message : String(e))

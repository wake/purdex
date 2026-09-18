// spa/src/lib/nex/handoff.ts — "Hand to nex" / "Take back to terminal"
// orchestration (P-C.3 spec §4.4 SPA). Imperative, no hooks: the daemon does
// the whole sequence under one per-session lock; this side only gates on
// readiness, composes the resume template, asks, and swaps pane content.
//
// Two things the daemon cannot do for us live here:
//
// - **Client single-flight.** A double-click or two panes on the same session
//   would send two requests; the daemon serialises them on its lock and
//   answers the loser `handoff_in_progress` after the winner finished — for
//   a handoff that is "CC already exited". Refusing the second call before
//   it leaves the client keeps the UI from ever racing itself.
// - **Checked swap.** The pane can be closed while the request is in flight.
//   `setPaneContent` would silently drop the write, and the execution the
//   daemon just created would be reachable only from the Executions list.
//   `trySetPaneContent` reports it, and the caller offers "open execution".
import { useTabStore } from '../../stores/useTabStore'
import { useNexHostStore, selectHandoffReady } from '../../stores/useNexHostStore'
import { resumeLookupFor, resumeTemplateFor } from '../resume-templates'
import type { TFunction } from '../pane-labels'
import type { ExecutionFrom, PaneContent } from '../../types/tab'
import { HandoffApiError, nexHandoff, nexTakeback, type NexHandoffResult, type NexTakebackResult } from './handoff-api'

/** In-flight keys: `handoff:<host>:<session>` and `takeback:<host>:<execution>`. */
const inFlight = new Set<string>()

async function singleFlight<T>(key: string, run: () => Promise<T>): Promise<T> {
  if (inFlight.has(key)) throw new HandoffApiError(0, 'handoff_in_progress', {})
  inFlight.add(key)
  try {
    return await run()
  } finally {
    inFlight.delete(key)
  }
}

export interface HandToNexArgs {
  hostId: string
  sessionCode: string
  tmuxInstance: string
  cachedName: string
  tabId: string
  paneId: string
}

export interface HandToNexOutcome {
  result: NexHandoffResult
  /** `false` when the pane was gone by the time the daemon answered; the handoff still happened. */
  swapped: boolean
}

/** The execution content a successful handoff writes into the pane (also what the recovery toast opens). */
export function executionContentFor(hostId: string, executionId: string, from: ExecutionFrom): PaneContent {
  return { kind: 'execution', executionId, host: hostId, from }
}

export async function handToNex(args: HandToNexArgs): Promise<HandToNexOutcome> {
  const { hostId, sessionCode, tmuxInstance, cachedName, tabId, paneId } = args
  return singleFlight(`handoff:${hostId}:${sessionCode}`, async () => {
    const nexHosts = useNexHostStore.getState()
    await nexHosts.ensure(hostId)
    if (!selectHandoffReady(hostId)(useNexHostStore.getState())) {
      throw new HandoffApiError(0, 'handoff_unsupported', {})
    }
    const result = await nexHandoff(hostId, sessionCode, {
      expected_tmux_instance: tmuxInstance,
      // `{id}` left for the daemon: it read the session id itself.
      rollback_command: resumeTemplateFor(resumeLookupFor(hostId), 'cc'),
    })
    const from: ExecutionFrom = { sessionCode, tmuxInstance, cachedName }
    const swapped = useTabStore.getState().trySetPaneContent(tabId, paneId, executionContentFor(hostId, result.execution_id, from))
    return { result, swapped }
  })
}

export interface TakeBackArgs {
  hostId: string
  executionId: string
  from: ExecutionFrom
  /** The lease this tab holds on the execution, if any — the daemon releases it as part of the take-back. */
  leaseId?: string
  tabId: string
  paneId: string
  /**
   * Called on success BEFORE the pane swap: the swap unmounts the execution
   * view, whose lease hook releases on cleanup — but the daemon already
   * consumed the lease, so the hook must forget it rather than release it.
   */
  forgetLease: () => void
}

export interface TakeBackOutcome {
  result: NexTakebackResult
  swapped: boolean
}

export async function takeBack(args: TakeBackArgs): Promise<TakeBackOutcome> {
  const { hostId, executionId, from, leaseId, tabId, paneId, forgetLease } = args
  return singleFlight(`takeback:${hostId}:${executionId}`, async () => {
    const resume_command = resumeTemplateFor(resumeLookupFor(hostId), 'cc')
    if (!resume_command) throw new HandoffApiError(0, 'missing_resume_command', {})
    const result = await nexTakeback(hostId, from.sessionCode, {
      expected_tmux_instance: from.tmuxInstance,
      execution_id: executionId,
      resume_command,
      ...(leaseId ? { lease_id: leaseId } : {}),
    })
    forgetLease()
    const swapped = useTabStore.getState().trySetPaneContent(tabId, paneId, {
      kind: 'tmux-session',
      hostId,
      sessionCode: from.sessionCode,
      mode: 'terminal',
      cachedName: from.cachedName,
      tmuxInstance: from.tmuxInstance,
    })
    return { result, swapped }
  })
}

// --- error map -------------------------------------------------------------

/**
 * Every code the two endpoints emit (plan "Measured baseline", from
 * internal/module/nex/{handoff,takeback}.go) plus the wrapper's own
 * `network` and `host_removed`. Each has a `handoff.error.<code>` locale string; anything else
 * (`http_<status>`, a future code) falls back to `handoff.error.generic`.
 */
export const HANDOFF_ERROR_CODES: readonly string[] = [
  // handoff
  'nex_unavailable', 'malformed_body', 'invalid_instance', 'session_missing',
  'handoff_unsupported', 'handoff_in_progress', 'tmux_instance_mismatch',
  'no_identity', 'no_cc', 'delegate_rejected', 'principal_unresolved',
  'session_lookup_failed', 'cc_exit_timeout',
  // takeback (codes not already above)
  'missing_execution_id', 'missing_resume_command', 'execution_not_found',
  'cc_already_running', 'execution_not_bound', 'held_by',
  'execution_not_settled', 'no_session_id', 'store_error', 'lease_error',
  'interrupt_failed', 'send_failed', 'interrupt_unconfirmed', 'cc_start_timeout',
  // client
  'network', 'host_removed',
]

const KNOWN = new Set(HANDOFF_ERROR_CODES)

/**
 * Codes whose body carries `session_id` (daemon handlers): the CC session
 * that is now detached from the pane and can be resumed by hand. Other
 * codes never carry it, so a stray `session_id` on them is not surfaced.
 */
export const SESSION_ID_CODES: ReadonlySet<string> = new Set([
  'tmux_instance_mismatch', 'delegate_rejected', // handoff
  'cc_already_running', 'send_failed', 'cc_start_timeout', // takeback
])

function str(body: Record<string, unknown>, field: string): string {
  const v = body[field]
  return typeof v === 'string' && v !== '' ? v : '?'
}

/** Params per code; only the fields the locale string interpolates. */
function paramsFor(t: TFunction, err: HandoffApiError): Record<string, string | number> | undefined {
  const b = err.body
  switch (err.code) {
    case 'cc_exit_timeout':
      return { step: str(b, 'step') }
    case 'delegate_rejected':
      return {
        reject_reason: str(b, 'reject_reason'),
        rolled_back: t(b.rolled_back === true ? 'handoff.rolled_back' : 'handoff.not_rolled_back'),
      }
    case 'execution_not_bound':
      return { execution_id: str(b, 'execution_id'), session_code: str(b, 'session_code') }
    case 'held_by':
      return { principal: str(b, 'principal') }
    case 'execution_not_settled':
      return { state: str(b, 'state') }
    default:
      return undefined
  }
}

/** The toast text for a failed handoff / take-back. */
export function handoffErrorMessage(t: TFunction, err: HandoffApiError): string {
  if (!KNOWN.has(err.code)) return t('handoff.error.generic', { code: err.code })
  return t(`handoff.error.${err.code}`, paramsFor(t, err))
}

/**
 * The CC session id to resume by hand, when the failure left one detached;
 * else null. `rolled_back: true` means the daemon already restarted CC in
 * the terminal, so there is nothing to resume by hand even though the body
 * still names the session.
 */
export function manualResumeHint(err: HandoffApiError): string | null {
  if (!SESSION_ID_CODES.has(err.code)) return null
  if (err.body.rolled_back === true) return null
  const id = err.body.session_id
  return typeof id === 'string' && id !== '' ? id : null
}

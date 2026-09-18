// spa/src/lib/nex/handoff.ts — "Hand to nex" / "Take back to terminal" /
// "Take to terminal" orchestration (P-C.3 spec §4.4 SPA, exec-to-terminal
// spec §4.2). Imperative, no hooks: the daemon does the whole sequence under
// one per-session (or per-execution) lock; this side only gates on readiness,
// composes the resume template and the session name, asks, and swaps pane
// content.
//
// Two things the daemon cannot do for us live here:
//
// - **Client single-flight.** A double-click or two panes on the same session
//   would send two requests; the daemon serialises them on its lock and
//   answers the loser `handoff_in_progress` after the winner finished — for
//   a handoff that is "CC already exited". Refusing the second call before
//   it leaves the client keeps the UI from ever racing itself.
// - **Checked swap.** The pane can be closed — or pointed at something else
//   — while the request is in flight. `setPaneContent` would silently drop
//   the write (or clobber whatever the pane shows now), and the execution
//   the daemon just created would be reachable only from the Executions
//   list. `trySetPaneContent` is a compare-and-swap on the content the call
//   started from; it reports a miss, and the caller offers "open execution".
import { useTabStore } from '../../stores/useTabStore'
import { useNexHostStore, selectHandoffReady } from '../../stores/useNexHostStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { resumeLookupFor, resumeTemplateFor } from '../resume-templates'
import { nextProjectSessionName } from '../launch-session-name'
import { slugForCwd } from './session-slug'
import type { TFunction } from '../pane-labels'
import type { ExecutionFrom, PaneContent } from '../../types/tab'
import {
  HandoffApiError, nexHandoff, nexTakeback, nexTakeToTerminal,
  type NexHandoffResult, type NexTakebackResult, type NexTakeToTerminalResult,
} from './handoff-api'

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
  /** G4: keep the idle tmux session as the anchor to come back to (default `true`). */
  keepSession?: boolean
}

export interface HandToNexOutcome {
  result: NexHandoffResult
  /** `false` when the pane was gone by the time the daemon answered; the handoff still happened. */
  swapped: boolean
}

/**
 * The execution content a successful handoff writes into the pane (also what
 * the recovery toast opens). `from` is omitted when the session was not kept:
 * the pane then has nothing to return to, and "Take to terminal" creates a
 * new session instead.
 */
export function executionContentFor(hostId: string, executionId: string, from?: ExecutionFrom): PaneContent {
  return from ? { kind: 'execution', executionId, host: hostId, from } : { kind: 'execution', executionId, host: hostId }
}

/** `from` for the execution pane, or undefined when the daemon says the session is gone. */
export function handoffFromFor(args: Pick<HandToNexArgs, 'sessionCode' | 'tmuxInstance' | 'cachedName'>, result: Pick<NexHandoffResult, 'session_kept'>): ExecutionFrom | undefined {
  // An old daemon omits the field: it never kills, so the session is there.
  if (result.session_kept === false) return undefined
  return { sessionCode: args.sessionCode, tmuxInstance: args.tmuxInstance, cachedName: args.cachedName }
}

export async function handToNex(args: HandToNexArgs): Promise<HandToNexOutcome> {
  const { hostId, sessionCode, tmuxInstance, keepSession = true, tabId, paneId } = args
  return singleFlight(`handoff:${hostId}:${sessionCode}`, async () => {
    const nexHosts = useNexHostStore.getState()
    await nexHosts.ensure(hostId)
    if (!selectHandoffReady(hostId)(useNexHostStore.getState())) {
      throw new HandoffApiError(0, 'handoff_unsupported', {})
    }
    // `resumeLookupFor` answers from defaults until the host config is in
    // the store; a load failure leaves it that way (defaults, as before).
    await useHostConfigStore.getState().ensureLoaded(hostId)
    const result = await nexHandoff(hostId, sessionCode, {
      expected_tmux_instance: tmuxInstance,
      // `{id}` left for the daemon: it read the session id itself.
      rollback_command: resumeTemplateFor(resumeLookupFor(hostId), 'cc'),
      keep_session: keepSession,
    })
    const swapped = useTabStore.getState().trySetPaneContent(
      tabId, paneId, executionContentFor(hostId, result.execution_id, handoffFromFor(args, result)),
      (c) => c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode && c.tmuxInstance === tmuxInstance,
    )
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
    // Same as handToNex: the host's override is only visible once loaded.
    await useHostConfigStore.getState().ensureLoaded(hostId)
    const resume_command = resumeTemplateFor(resumeLookupFor(hostId), 'cc')
    if (!resume_command) throw new HandoffApiError(0, 'missing_resume_command', {})
    const result = await nexTakeback(hostId, from.sessionCode, {
      expected_tmux_instance: from.tmuxInstance,
      execution_id: executionId,
      resume_command,
      ...(leaseId ? { lease_id: leaseId } : {}),
    })
    forgetLease()
    const swapped = useTabStore.getState().trySetPaneContent(
      tabId, paneId,
      { kind: 'tmux-session', hostId, sessionCode: from.sessionCode, mode: 'terminal', cachedName: from.cachedName, tmuxInstance: from.tmuxInstance },
      (c) => c.kind === 'execution' && c.executionId === executionId && (c.host ?? hostId) === hostId,
    )
    return { result, swapped }
  })
}

export interface TakeToTerminalArgs {
  hostId: string
  executionId: string
  /** The execution's cwd: the new tmux session is created there and its name follows the project it is in. */
  cwd: string
  leaseId?: string
  tabId: string
  paneId: string
  /** Same contract as `TakeBackArgs.forgetLease`. */
  forgetLease: () => void
}

export interface TakeToTerminalOutcome {
  result: NexTakeToTerminalResult
  swapped: boolean
}

/**
 * One retry after `session_exists` on a generated name (the daemon knew a
 * session the cached list did not); the refused name is counted as taken so
 * the second attempt strictly advances.
 */
const TAKE_TO_TERMINAL_NAME_RETRIES = 1

/** Refresh the host's session list so the sidebar shows (or drops) a session the daemon just changed; never throws. */
function refreshSessions(hostId: string): void {
  useSessionStore.getState().fetchHost(hostId).catch(() => { /* the next poll catches up */ })
}

/**
 * Take an execution with no origin session to a fresh terminal: the daemon
 * creates `{slug}-{N}` in the execution's cwd, resumes CC there and archives
 * the execution; the pane becomes that terminal. Shares the take-back
 * single-flight key so the two can never interleave on one execution.
 */
export async function takeToTerminal(args: TakeToTerminalArgs): Promise<TakeToTerminalOutcome> {
  const { hostId, executionId, cwd, leaseId, tabId, paneId, forgetLease } = args
  return singleFlight(`takeback:${hostId}:${executionId}`, async () => {
    // The host config carries both the resume template override and the
    // projects the slug is looked up from; neither is visible until loaded.
    await useHostConfigStore.getState().ensureLoaded(hostId)
    const resume_command = resumeTemplateFor(resumeLookupFor(hostId), 'cc')
    if (!resume_command) throw new HandoffApiError(0, 'missing_resume_command', {})
    const slug = slugForCwd(cwd, useHostConfigStore.getState().byHost[hostId]?.projects ?? [])
    const liveNames = (useSessionStore.getState().sessions[hostId] ?? []).map((s) => s.name)
    const refused: string[] = []
    const request = async (): Promise<NexTakeToTerminalResult> => {
      const session_name = nextProjectSessionName(slug, [...liveNames, ...refused])
      try {
        return await nexTakeToTerminal(hostId, executionId, {
          session_name,
          resume_command,
          ...(leaseId ? { lease_id: leaseId } : {}),
        })
      } catch (err) {
        if (err instanceof HandoffApiError) {
          if (err.code === 'session_exists' && refused.length < TAKE_TO_TERMINAL_NAME_RETRIES) {
            refused.push(session_name)
            return request()
          }
          // The tmux session exists but has no code yet (spec §4.1 step 6):
          // list it so the user can find and clean up the orphan.
          if (err.code === 'session_create_failed' && err.body.session_alive === true) refreshSessions(hostId)
        }
        throw err
      }
    }
    const result = await request()
    forgetLease()
    const { session } = result
    const swapped = useTabStore.getState().trySetPaneContent(
      tabId, paneId,
      { kind: 'tmux-session', hostId, sessionCode: session.code, mode: 'terminal', cachedName: session.name, tmuxInstance: session.tmux_instance ?? '' },
      (c) => c.kind === 'execution' && c.executionId === executionId && (c.host ?? hostId) === hostId,
    )
    refreshSessions(hostId)
    return { result, swapped }
  })
}

// --- error map -------------------------------------------------------------

/**
 * Every code the three endpoints emit (plan "Measured baseline", from
 * internal/module/nex/{handoff,takeback}.go, plus exec-to-terminal spec §4.1
 * for take_to_terminal.go) plus the wrapper's own `network` and
 * `host_removed`. Each has a `handoff.error.<code>` locale string; anything
 * else (`http_<status>`, a future code) falls back to `handoff.error.generic`.
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
  // take-to-terminal (codes not already above)
  'session_exists', 'missing_session_name', 'invalid_session_name', 'session_create_failed',
  'cwd_missing', 'provider_unsupported', 'takeback_in_progress',
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
  'cc_already_running', 'send_failed', 'cc_start_timeout', // takeback + take-to-terminal
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
    case 'session_exists':
    case 'session_create_failed':
      return { session_name: str(b, 'session_name') }
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

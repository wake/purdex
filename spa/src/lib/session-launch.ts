// spa/src/lib/session-launch.ts — create a session for the launcher and run a
// command in it (host-launcher spec §5.2).
//
// Everything goes through ONE `pinHost(hostId)` transport, the rebuild
// engine's: `hostFetch` would resolve an unknown or re-pointed host id to the
// active host, and a command typed for one machine must never run on another.
// The send is the same generation-guarded send-keys the rebuild uses.
import { pinHost, type PinnedTransport } from './rebuild/transport'
import { isValidSessionName } from './session-name'
import { nextProjectSessionName } from './launch-session-name'
import { useSessionStore } from '../stores/useSessionStore'
import type { Session } from './host-api'
import type { HostCommand, HostProject } from './host-config-api'

/**
 * Retries after a 409 on a GENERATED name — the daemon knew a session the
 * cached list did not. One initial attempt precedes them, so a project launch
 * tries at most `1 + MAX_GENERATED_NAME_RETRIES` names.
 */
export const MAX_GENERATED_NAME_RETRIES = 5

/**
 * `sendError` marker for "the command was never sent": the daemon created the
 * session without a tmux generation (pre-generation daemon), and the guarded
 * send has nothing to assert against. A stable value, not a message — the UI
 * localizes it instead of showing the transport's internal wording.
 */
export const SEND_UNSUPPORTED = 'unsupported_daemon'

export interface LaunchRequest {
  /** What the user typed; trimmed here. Empty means "generate from the project". */
  name: string
  project?: HostProject
  command?: HostCommand
}

export type LaunchOutcome =
  /** `sendError` is `SEND_UNSUPPORTED`, or a real send failure's message. */
  | { status: 'created'; session: Session; sendError?: string }
  | { status: 'failed'; reason: 'invalid_name' | 'create_failed' | 'host'; error: string }

export interface LaunchDeps {
  pin?: (hostId: string) => Pick<PinnedTransport, 'createSession' | 'sendKeys'>
  liveNames?: (hostId: string) => readonly string[]
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function isDuplicateName(err: unknown): boolean {
  return typeof err === 'object' && err !== null && (err as { status?: unknown }).status === 409
}

const storeLiveNames = (hostId: string): readonly string[] =>
  (useSessionStore.getState().sessions[hostId] ?? []).map((s) => s.name)

/** Never throws: every failure is an outcome the launcher can render. */
export async function launchSession(hostId: string, req: LaunchRequest, deps: LaunchDeps = {}): Promise<LaunchOutcome> {
  const typed = req.name.trim()
  const project = req.project
  if (typed ? !isValidSessionName(typed) : !project) {
    return { status: 'failed', reason: 'invalid_name', error: typed ? 'invalid session name' : 'session name required' }
  }

  let pinned: Pick<PinnedTransport, 'createSession' | 'sendKeys'>
  try {
    pinned = (deps.pin ?? pinHost)(hostId)
  } catch (err) {
    return { status: 'failed', reason: 'host', error: message(err) }
  }

  const cwd = project?.path ?? '~'
  const liveNames = (deps.liveNames ?? storeLiveNames)(hostId)
  // A typed name is the user's: one attempt, a duplicate is reported. A
  // generated name may lose a race with a session the cached list missed, so
  // it gets the initial attempt plus MAX_GENERATED_NAME_RETRIES more names.
  const attempts = typed ? 1 : 1 + MAX_GENERATED_NAME_RETRIES
  // Names this launch already had refused with a 409: the daemon knows them
  // even though the cached list does not, so the generator must count them as
  // taken or every retry would recompute the same losing name.
  const refused: string[] = []
  // `project` is set whenever `typed` is empty (checked above); the fallback
  // slug is unreachable and only keeps the name generation total.
  const nextName = (): string => typed || nextProjectSessionName(project?.slug ?? '', [...liveNames, ...refused])

  let session: Session | undefined
  let lastError: unknown
  for (let attempt = 0; attempt < attempts; attempt++) {
    const name = nextName()
    try {
      session = await pinned.createSession(name, cwd, 'terminal')
      break
    } catch (err) {
      lastError = err
      if (!isDuplicateName(err)) break
      refused.push(name)
    }
  }
  if (!session) return { status: 'failed', reason: 'create_failed', error: message(lastError) }
  if (!session.code) return { status: 'failed', reason: 'create_failed', error: 'empty session code' }

  if (!req.command) return { status: 'created', session }
  // No generation means no guarded send: the transport would reject it and the
  // user would read its internal wording. Report the session as created and say
  // why the command did not run.
  if (!session.tmux_instance) return { status: 'created', session, sendError: SEND_UNSUPPORTED }
  try {
    await pinned.sendKeys(session.code, req.command.command, session.tmux_instance)
    return { status: 'created', session }
  } catch (err) {
    return { status: 'created', session, sendError: message(err) }
  }
}

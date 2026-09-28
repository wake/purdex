// spa/src/lib/rebuild/provenance.ts — the daemon's `pdx_provenance` envelope,
// validated into the shape the rebuild record is written from (spec §4.3.1).
//
// The envelope is the ONLY thing the write path may read. On a proxy-collapsed
// event the outer `agent_type` names the session-projection winner while the
// rest of the detail describes the sender; re-deriving the agent from that
// field is exactly the mis-attribution the envelope exists to prevent.
import type { AgentExitReason } from '../../types/tab'

/** A validated envelope. Optional fields are '' when the daemon omitted them. */
export interface ParsedProvenance {
  agentType: string
  sessionId: string
  cwd: string
  tmuxPaneId: string
  tmuxInstance: string
  /** The daemon frame that is this agent run — the key an exit is matched on. */
  frameId: string
}

/** A payload field that is not a string is treated as absent, never coerced. */
function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

/**
 * Read `detail.pdx_provenance`. Returns null unless the envelope is a real
 * object, `owner_session_start` is strictly `true`, and both `agent_type` and
 * `tmux_instance` are non-empty — an unknown generation must never write,
 * because a record that cannot name its tmux generation cannot be guarded
 * against session-code reuse (spec §4.5).
 */
export function parseProvenance(
  detail: Record<string, unknown> | undefined,
): ParsedProvenance | null {
  const raw = detail?.pdx_provenance
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  const env = raw as Record<string, unknown>
  if (env.owner_session_start !== true) return null

  const agentType = str(env.agent_type)
  const tmuxInstance = str(env.tmux_instance)
  if (!agentType || !tmuxInstance) return null

  return {
    agentType,
    sessionId: str(env.session_id),
    cwd: str(env.cwd),
    tmuxPaneId: str(env.tmux_pane_id),
    tmuxInstance,
    frameId: str(env.frame_id),
  }
}

/** A validated `pdx_exit` envelope (agent-last-state spec §1). */
export interface ParsedExit {
  agentType: string
  sessionId: string
  tmuxPaneId: string
  tmuxInstance: string
  frameId: string
  reason: AgentExitReason
  /** Unix ms on the daemon's clock. Displayed, and stamped as the record's `capturedAt`
   *  (the same on every client — sync-conflict-fixes spec D1); never compared with a client clock. */
  at: number
}

const EXIT_REASONS: ReadonlySet<string> = new Set<AgentExitReason>(['session-end', 'process-dead'])

/**
 * Read `detail.pdx_exit`, the daemon's "this root agent run ended" envelope.
 *
 * Returns null unless it names a frame (the ONLY key an exit is applied by — an
 * exit that cannot say which run ended must change nothing), a generation (the
 * same reuse guard as provenance), a known reason and a positive time.
 */
export function parseExit(detail: Record<string, unknown> | undefined): ParsedExit | null {
  const raw = detail?.pdx_exit
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  const env = raw as Record<string, unknown>

  const frameId = str(env.frame_id)
  const tmuxInstance = str(env.tmux_instance)
  const reason = str(env.reason)
  const at = env.at
  if (!frameId || !tmuxInstance || !EXIT_REASONS.has(reason)) return null
  if (typeof at !== 'number' || !Number.isFinite(at) || at <= 0) return null

  return {
    agentType: str(env.agent_type),
    sessionId: str(env.session_id),
    tmuxPaneId: str(env.tmux_pane_id),
    tmuxInstance,
    frameId,
    reason: reason as AgentExitReason,
    at,
  }
}

/**
 * A daemon timestamp in nanoseconds (`broadcast_ts`, a frame's `started_at`) as
 * Unix milliseconds, for a record written from a HOST EVENT.
 *
 * Every attached client writes the same pane of the same synced `tabs.<ws>`
 * section when it sees the same event; stamped with each client's own clock
 * the payloads differ and Profile Sync locks the section as a conflict nobody
 * made (sync-conflict-fixes spec D1). The daemon's value is the same bytes on
 * every client. Anything that is not a positive finite number of at least one
 * millisecond inside a fixed 2020-01-01 .. 2100-01-01 window (see below) — an
 * older daemon that never sent it, 0, garbage — falls back to
 * `Date.now()`: no worse than before.
 *
 * Nanosecond epochs exceed Number.MAX_SAFE_INTEGER, so the parsed value is the
 * nearest double; that is still the same double on every client, and far
 * finer than the millisecond kept.
 */
export function daemonNsToMs(ns: unknown): number {
  return daemonNsToMsOrNull(ns) ?? Date.now()
}

/**
 * The same conversion as `daemonNsToMs`, but null when `ns` is not a valid
 * daemon time — so a writer can tell a daemon stamp from the client-clock
 * fallback. Only a daemon stamp may ORDER a write against synced content: a
 * client clock says nothing about which run is newer, so it must never be
 * used to reject one (sync-conflict-fixes spec, ordering rules).
 */
export function daemonNsToMsOrNull(ns: unknown): number | null {
  if (typeof ns !== 'number' || !Number.isFinite(ns)) return null
  const ms = Math.floor(ns / 1e6)
  return Number.isSafeInteger(ms) && ms >= DAEMON_MS_MIN && ms <= DAEMON_MS_MAX ? ms : null
}

/**
 * The sane window for a daemon time, as FIXED constants: a garbage value (1e300,
 * a seconds value mistaken for ns, a broken clock) must not become a record
 * stamp — `capturedAt` elects each group's newest record, and a far-future one
 * would win every election for good. Never relative to `Date.now()`: every
 * client must judge the same value the same way, or their payloads diverge.
 */
const DAEMON_MS_MIN = Date.UTC(2020, 0, 1)
const DAEMON_MS_MAX = Date.UTC(2100, 0, 1)

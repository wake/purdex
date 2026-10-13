// spa/src/lib/read-sync/reporter.ts — tells a session's host daemon "X read through T" (app-badge spec §7.1–§7.3).
//
// Two things call it, both a person having seen the session: a user read (the unread -> read transition of a host
// session key) and "seen live" (an actionable event of an agent that is on screen, so never marked unread). A received
// read never does: that would echo a read between devices forever.
//
// One POST per session per fixed 2 s window: the first report starts the window, the ones inside it only raise the
// largest captured cursor, so a busy agent cannot starve the POST and an event arriving inside the window can never
// raise T beyond what was captured (T is handed in by the caller, never read here at fire time). Only host sessions
// (not `exec-*` workers, not `cc-*` sessions outside tmux) on a host that declares `session.read_sync.v1`. Best effort:
// a 404 (a session the daemon does not have), a network failure or a throw is ignored and nothing is retried.
import { selectReadSyncV1, useNexHostStore } from '../../stores/useNexHostStore'
import { pinnedHostFetch } from '../host-api'
import { isNonTmuxAgentCode } from '../non-tmux-agent'
import { isExecAgentCode } from '../nex/worker-agent-status'
import { maxCursor, type ReadCursor } from './cursor'

export const READ_DEBOUNCE_MS = 2000

interface Pending {
  hostId: string
  code: string
  cursor: ReadCursor | undefined
  timer: ReturnType<typeof setTimeout>
}

const pending = new Map<string, Pending>()
/** NUL-joined, as the approval keys are: a host id may hold any printable separator, never NUL. */
const pendingKey = (hostId: string, code: string): string => `${hostId}\u0000${code}`

/** A tmux session of a host: the only thing the daemon's read route knows. */
const isHostSessionCode = (code: string): boolean => code !== '' && !isExecAgentCode(code) && !isNonTmuxAgentCode(code)

function send(hostId: string, code: string, cursor: ReadCursor | undefined): void {
  const init: RequestInit = {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cursor ? { through: cursor } : {}),
  }
  try {
    pinnedHostFetch(hostId, `/api/sessions/${encodeURIComponent(code)}/read`, init).catch(() => {})
  } catch { /* best effort */ }
}

/** `code` of `hostId` was seen through `cursor` (undefined: the event carried none). */
export function reportRead(hostId: string, code: string, cursor?: ReadCursor): void {
  if (!isHostSessionCode(code)) return
  if (!selectReadSyncV1(hostId)(useNexHostStore.getState())) return
  const key = pendingKey(hostId, code)
  const held = pending.get(key)
  if (held) {
    held.cursor = maxCursor(held.cursor, cursor)
    return
  }
  const entry: Pending = {
    hostId, code, cursor,
    timer: setTimeout(() => {
      pending.delete(key)
      send(hostId, code, entry.cursor)
    }, READ_DEBOUNCE_MS),
  }
  pending.set(key, entry)
}

/** Cancel every pending window (app teardown, tests). */
export function stopReadSync(): void {
  for (const p of pending.values()) clearTimeout(p.timer)
  pending.clear()
}

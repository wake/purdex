import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import type { EnsureReport, Remap, RemapEntry, SessionMeta } from './types'

/**
 * Reconcile persisted per-host session metadata against each host's live
 * session list, reattaching survivors and (optionally) rebuilding restorable
 * dead sessions.
 *
 * Contract highlights (see plan §8.1):
 * - Exactly one `listSessions` per host. If it throws (host offline), every
 *   entry for that host is `failed` and `createSession` is never called there.
 * - Rebuilt entries ALWAYS trust the returned Session object for `newCode` /
 *   `session` — the daemon may auto-rename or assign a different code.
 * - Per-session failure isolation: a single `createSession` rejection marks
 *   only that entry `failed` and never aborts the rest.
 * - `remap` is nested by hostId then oldCode, so identical code values under
 *   different hosts never collide.
 */
export async function ensureSessions(
  sessionMeta: Record<string, Record<string, SessionMeta>>,
  opts?: { rebuild?: boolean },
): Promise<{ remap: Remap; report: EnsureReport }> {
  const rebuild = opts?.rebuild !== false
  const remap: Remap = {}
  const report: EnsureReport = { reattached: 0, rebuilt: 0, failed: 0 }

  for (const [hostId, perHost] of Object.entries(sessionMeta)) {
    const perHostRemap: Record<string, RemapEntry> = {}
    remap[hostId] = perHostRemap

    let live: Session[] | null
    try {
      live = await listSessions(hostId)
    } catch {
      live = null // host offline — every entry fails, no createSession
    }

    for (const [oldCode, meta] of Object.entries(perHost)) {
      if (live === null) {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
        continue
      }

      const alive = live.find((s) => s.code === oldCode)
      if (alive) {
        perHostRemap[oldCode] = { status: 'reattached', newCode: oldCode, session: alive }
        report.reattached++
        continue
      }

      if (!rebuild || !meta.restorable || !meta.cwd) {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
        continue
      }

      try {
        const created = await createSession(hostId, meta.name, meta.cwd, meta.mode)
        // §8.1: trust the returned object, never the request values.
        perHostRemap[oldCode] = { status: 'rebuilt', newCode: created.code, session: created }
        report.rebuilt++
      } catch {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
      }
    }
  }

  return { remap, report }
}

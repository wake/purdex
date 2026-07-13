import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import { scanPaneTree, updatePaneInLayout } from '../pane-tree'
import type { PaneContent, PaneLayout } from '../../types/tab'
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

/**
 * Rewrite every `tmux-session` pane in a layout tree against a {@link Remap}
 * produced by {@link ensureSessions}, returning a NEW layout (input untouched).
 *
 * Per-pane behaviour (keyed by the composite `[hostId][sessionCode]`):
 * - `reattached` / `rebuilt` → adopt `entry.newCode`, refresh `cachedName` from
 *   `entry.session.name`, and clear any `terminated` marker (the session is now
 *   attachable again).
 * - `failed` → keep the pane's code but mark `terminated: 'tmux-restarted'`.
 *   The reason is FIXED for the restore path (codex plan-review): restore never
 *   guesses `'session-closed'` / `'host-removed'`.
 * - no matching entry → pane left exactly as-is.
 *
 * `opts.onlyTerminated` guards the "rebuild all sessions" action (spec §3.5):
 * when true, only panes that ALREADY carry a `terminated` marker are touched;
 * live panes are never rewritten even if their key matches a remap entry.
 */
export function remapLayoutSessions(
  layout: PaneLayout,
  remap: Remap,
  opts?: { onlyTerminated?: boolean },
): PaneLayout {
  const onlyTerminated = opts?.onlyTerminated === true

  // Collect the intended content updates first (scan does not mutate), then fold
  // them into fresh layouts via updatePaneInLayout so the input is never touched.
  const updates: Array<{ paneId: string; content: PaneContent }> = []

  scanPaneTree(layout, (pane) => {
    const content = pane.content
    if (content.kind !== 'tmux-session') return
    if (onlyTerminated && content.terminated === undefined) return

    const entry = remap[content.hostId]?.[content.sessionCode]
    if (!entry) return

    if (entry.status === 'failed') {
      updates.push({
        paneId: pane.id,
        content: { ...content, terminated: 'tmux-restarted' },
      })
      return
    }

    // reattached | rebuilt — adopt new code/name, clear terminated marker.
    const { terminated: _cleared, ...rest } = content
    void _cleared
    updates.push({
      paneId: pane.id,
      content: { ...rest, sessionCode: entry.newCode, cachedName: entry.session.name },
    })
  })

  let result = layout
  for (const { paneId, content } of updates) {
    result = updatePaneInLayout(result, paneId, content)
  }
  return result
}

/* eslint-disable react-refresh/only-export-components */
import { useRef, useState } from 'react'
import {
  CheckCircle,
  CircleDashed,
  CircleNotch,
  Warning,
  WarningCircle,
} from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useRebuildStore } from '../../../stores/useRebuildStore'
import { RestoreError } from '../../../lib/snapshot/types'
import type { RestoreReport, SessionMeta } from '../../../lib/snapshot/types'
import type { Session } from '../../../lib/host-api'
import type { HostLiveness, RecordHealth } from '../../../lib/rebuild/eligibility'
import type { PaneContent } from '../../../types/tab'

export type TFn = ReturnType<typeof useI18nStore.getState>['t']

export type Tone = 'idle' | 'busy' | 'success' | 'warn' | 'error'

export interface Status {
  tone: Tone
  message: string
  /** data-* attributes exposed for tests / debugging (counts, totals). */
  attrs?: Record<string, string | number>
  /** Rebuilt-but-unattached sessions to disclose (R3 B), name/cwd/host each. */
  unattached?: RestoreReport['rebuiltButUnattached']
}

export const IDLE: Status = { tone: 'idle', message: '' }

/**
 * Capture never takes the operation lock — it creates nothing — but it must not
 * photograph a half-rebuilt world either, so it carries an owner purely so the
 * "is anyone else holding the lock" test disables it like the rest.
 */
export const CAPTURE_OWNER = 'snapshot:capture'

export function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/** Per-host live-session lookup state: still loading, host offline, or the list. */
export type HostLive = 'loading' | 'offline' | Session[]

/**
 * Four-state (plus loading) health, shared by both tables. The per-tab records
 * table gets its verdict from `lib/rebuild/eligibility`, which is also what
 * "Rebuild all" acts on, so a badge and a button can never disagree.
 */
export type Health = RecordHealth

/** What `liveByHost` says about one host, reduced to what the records table needs. */
export function livenessOf(live: HostLive): HostLiveness {
  if (live === 'loading') return 'loading'
  if (live === 'offline') return 'offline'
  return 'online'
}

/**
 * Reconcile one captured {@link SessionMeta} against its host's live list. Mirrors
 * the engine's reattach rule (code AND name must match — a code-only match is a
 * different session after a tmux restart, so it is NOT "live").
 *
 * This is the LEGACY snapshot table's own policy and stays that way: its rows
 * are `SessionMeta`, not panes, so they carry no `terminated` verdict and no
 * generation — the live list is the only evidence there is. The per-tab records
 * table has both, and decides in `lib/rebuild/eligibility`.
 *
 * Precedence: an unreachable host wins (every row ⚪) → a live code+name match is
 * 🟢 → a non-restorable OR cwd-less entry is ⚠️ (the engine's `ensureSessions`
 * refuses to rebuild without a cwd, so it can never be 🔴) → otherwise 🔴
 * (restorable + cwd + dead, so "Rebuild all" can actually recreate it).
 */
export function computeHealth(meta: SessionMeta, live: HostLive): Health {
  if (live === 'loading') return 'loading'
  if (live === 'offline') return 'offline'
  const match = live.some((s) => s.code === meta.sessionCode && s.name === meta.name)
  if (match) return 'live'
  if (!meta.restorable || !meta.cwd) return 'structure'
  return 'dead'
}

const HEALTH_ICON: Record<Health, typeof CheckCircle> = {
  loading: CircleNotch,
  live: CheckCircle,
  dead: WarningCircle,
  structure: Warning,
  offline: CircleDashed,
}

const HEALTH_COLOR: Record<Health, string> = {
  loading: 'text-text-muted',
  live: 'text-green-500',
  dead: 'text-red-500',
  structure: 'text-yellow-500',
  offline: 'text-text-muted',
}

/**
 * The page's one health indicator, so the captured-snapshot table and the
 * per-tab records table cannot drift apart in icon, colour or wording.
 */
export function HealthBadge({
  health,
  testId,
  t,
}: {
  health: Health
  testId: string
  t: TFn
}) {
  const Icon = HEALTH_ICON[health]
  return (
    <span
      data-testid={testId}
      data-health={health}
      title={t(`settings.snapshot.health.${health}`)}
      className={`inline-flex items-center gap-1 ${HEALTH_COLOR[health]}`}
    >
      <Icon size={14} className={health === 'loading' ? 'animate-spin' : ''} />
      {t(`settings.snapshot.health.${health}`)}
    </span>
  )
}

/** Human label for a leaf pane, used in the Tabs tree. */
export function leafLabel(content: PaneContent): string {
  switch (content.kind) {
    case 'tmux-session':
      return content.cachedName || content.sessionCode
    case 'editor':
    case 'image-preview':
    case 'pdf-preview':
      return content.filePath
    case 'browser':
      return content.url
    default:
      return content.kind
  }
}

export function formatRelativeTime(t: TFn, ms: number): string {
  const diffSec = Math.floor((Date.now() - ms) / 1000)
  if (diffSec < 60) return t('settings.snapshot.time.secondsAgo', { n: diffSec })
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return t('settings.snapshot.time.minutesAgo', { n: diffMin })
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return t('settings.snapshot.time.hoursAgo', { n: diffHr })
  const diffDay = Math.floor(diffHr / 24)
  return t('settings.snapshot.time.daysAgo', { n: diffDay })
}

export function StatusLine({ status }: { status: Status }) {
  if (status.tone === 'idle' || !status.message) return null
  const Icon =
    status.tone === 'success' ? CheckCircle
    : status.tone === 'warn' ? Warning
    : status.tone === 'error' ? WarningCircle
    : CircleNotch
  const color =
    status.tone === 'success' ? 'text-green-500'
    : status.tone === 'warn' ? 'text-yellow-500'
    : status.tone === 'error' ? 'text-red-500'
    : 'text-text-secondary'
  return (
    <div
      data-testid="snapshot-status"
      data-tone={status.tone}
      {...status.attrs}
      className={`mt-4 text-xs ${color}`}
    >
      <div className="flex items-start gap-1.5">
        <Icon size={14} className={status.tone === 'busy' ? 'animate-spin mt-0.5' : 'mt-0.5'} />
        <span>{status.message}</span>
      </div>
      {status.unattached && status.unattached.length > 0 && (
        <ul className="mt-1 ml-5 flex flex-col gap-0.5 font-mono">
          {status.unattached.map((s, i) => (
            // name/cwd/host are runtime data (not i18n) — render literally so the
            // disclosure is always legible regardless of the active locale.
            <li key={`${s.hostId}:${s.name}:${i}`} data-testid="snapshot-unattached-item">
              {s.name} · {s.cwd} · {s.hostId}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/**
 * A resolved or failed RestoreReport as a status. `failed` (a RestoreError,
 * stores rolled back) takes ERROR-tone precedence: the engine only ever fills
 * rebuiltButUnattached on the failure path, so a non-empty list must NOT be
 * downgraded to a mild warning. The disclosure is always logged via
 * console.warn and shown under the status, whichever tone wins.
 */
export function statusForRestore(t: TFn, report: RestoreReport, failed: boolean): Status {
  const attrs = { 'data-reattached': report.reattached, 'data-rebuilt': report.rebuilt, 'data-failed': report.failed }
  const unattached = report.rebuiltButUnattached
  if (unattached.length > 0) console.warn('[snapshot] sessions rebuilt but could not be reattached', unattached)
  if (failed) {
    return { tone: 'error', message: t('settings.snapshot.toast.restoreError'), attrs, unattached: unattached.length > 0 ? unattached : undefined }
  }
  if (unattached.length > 0) {
    // Success-with-unattached — defensive only; the engine currently returns an
    // empty list on every success path.
    return { tone: 'warn', message: t('settings.snapshot.toast.rebuiltUnattached'), attrs, unattached }
  }
  return {
    tone: 'success',
    message: t('settings.snapshot.toast.restoreReport', { reattached: report.reattached, rebuilt: report.rebuilt, failed: report.failed }),
    attrs,
  }
}

/**
 * The single-flight guard + global operation lock + status line every snapshot
 * action on a page shares (spec §4.11 of the tab-rebuild work). Exactly ONE
 * instance per page: the host-scoped section owns it and hands it to the
 * client-scoped block, so capture and a host rebuild can never interleave
 * (host-launcher plan amendment A1).
 *
 * `busyRef` (not state) is the guard, so two synchronous clicks — which share
 * one render's `busy` closure — still only fire one action. It also covers
 * Capture and the cwd edit, neither of which takes the global lock.
 */
export function useSnapshotActions(t: TFn) {
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  // The GLOBAL operation lock. A Tab Rebuild takes it too, so every button whose
  // owner is not the current holder is disabled.
  const lockedBy = useRebuildStore((s) => s.lockedBy)
  const [status, setStatus] = useState<Status>(IDLE)
  const lockedOut = (owner: string) => lockedBy !== null && lockedBy !== owner

  const run = async (owner: string, busyKey: string, action: () => Promise<Status>, after?: () => void) => {
    if (busyRef.current) return
    // The residual window `disabled` cannot cover: the lock can be taken between
    // the last render and this click. Say why nothing happened.
    const holder = useRebuildStore.getState().lockedBy
    if (holder !== null && holder !== owner) {
      setStatus({ tone: 'warn', message: t('settings.snapshot.toast.locked', { owner: holder }) })
      return
    }
    busyRef.current = true
    setBusy(true)
    setStatus({ tone: 'busy', message: t(busyKey) })
    try {
      setStatus(await action())
    } catch (e) {
      setStatus({ tone: 'error', message: t('settings.snapshot.toast.restoreFailed', { reason: errMessage(e) }) })
    } finally {
      busyRef.current = false
      setBusy(false)
      after?.()
    }
  }

  return { busy, busyRef, status, setStatus, lockedOut, run }
}

export type SnapshotActions = ReturnType<typeof useSnapshotActions>

/** Restore-style action body: null → "nothing to undo", RestoreError → error status. */
export async function restoreAction(t: TFn, action: () => Promise<RestoreReport | null>): Promise<Status> {
  try {
    const report = await action()
    if (report === null) return { tone: 'warn', message: t('settings.snapshot.toast.undoNothing') }
    return statusForRestore(t, report, false)
  } catch (e) {
    if (e instanceof RestoreError) return statusForRestore(t, e.report, true)
    throw e
  }
}

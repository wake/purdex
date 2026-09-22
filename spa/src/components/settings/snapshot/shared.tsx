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
import type { Session } from '../../../lib/host-api'
import type { HostLiveness, RecordHealth } from '../../../lib/rebuild/eligibility'

export type TFn = ReturnType<typeof useI18nStore.getState>['t']

export type Tone = 'idle' | 'busy' | 'success' | 'warn' | 'error'

export interface Status {
  tone: Tone
  message: string
  /** data-* attributes exposed for tests / debugging (counts, totals). */
  attrs?: Record<string, string | number>
}

export const IDLE: Status = { tone: 'idle', message: '' }

export function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/** Per-host live-session lookup state: still loading, host offline, or the list. */
export type HostLive = 'loading' | 'offline' | Session[]

/**
 * Four-state (plus loading) health. The per-tab records
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
 * The page's one health indicator (icon, colour and wording).
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
    </div>
  )
}

/**
 * The single-flight guard + global operation lock + status line every rebuild
 * action on a page shares (spec §4.11 of the tab-rebuild work). Exactly ONE
 * instance per page.
 *
 * `busyRef` (not state) is the guard, so two synchronous clicks — which share
 * one render's `busy` closure — still only fire one action.
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

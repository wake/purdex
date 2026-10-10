// spa/src/components/status/UsageSegments.tsx — the usage readouts both status bars share: the context window and
// the 5-hour / weekly limits. `CcUsageSegments` reads a terminal session's Claude Code statusLine snapshot;
// `HostQuotaSegments` reads the Nexen host's quota for a worker pane. Both draw through `UsageSegment`, so a
// percentage looks the same wherever it comes from. Hidden when there is nothing to show.
import { useEffect, useState } from 'react'
import { useAgentStore } from '../../stores/useAgentStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNexHostQuota } from '../../hooks/useNexHostQuota'
import { compositeKey } from '../../lib/composite-key'
import {
  USAGE_STALE_MS, epochToMs, formatResetsIn, parseCcUsage, remainingPct, ringGeometry, ringTransform, usedPct,
  type UsageTone, type UsageWindow,
} from '../../lib/usage-display'
import type { Icon } from '@phosphor-icons/react'
import { Brain, CalendarBlank, Clock } from '@phosphor-icons/react'

/** Re-renders the caller every `ms` so "stale" and "resets in" age without a new snapshot arriving. */
function useNow(ms = 30_000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms)
    return () => clearInterval(id)
  }, [ms])
  return now
}

/** Ring colour per tone (on the USED share). The "ok" colour is the one to change if green gives way to neutral. */
const RING_TONE_CLASS: Record<UsageTone, string> = {
  ok: 'stroke-status-success',
  warn: 'stroke-status-warning',
  danger: 'stroke-status-error',
}

const RING_SIZE = 12
const RING_STROKE = 2.5
const RING_R = (RING_SIZE - RING_STROKE) / 2
const RING_C = 2 * Math.PI * RING_R

/** What a ring or a number shows. Context window: ring USED, number REMAINING. 5-hour / weekly limits: both REMAINING. */
export type UsageMode = 'used' | 'remaining'

function shownPct(used: number, mode: UsageMode): number {
  return mode === 'used' ? usedPct(used) : remainingPct(used)
}

/** From 12 o'clock: a `used` ring's bright arc grows counterclockwise; a `remaining` ring's runs clockwise (so it shrinks counterclockwise). Colour always follows `usageTone` of the USED share. */
function Ring({ used, mode }: { used: number; mode: UsageMode }) {
  const { sharePct: shown, direction, tone } = ringGeometry(used, mode)
  return (
    <svg width={RING_SIZE} height={RING_SIZE} viewBox={`0 0 ${RING_SIZE} ${RING_SIZE}`} aria-hidden="true" className="shrink-0">
      <circle cx={RING_SIZE / 2} cy={RING_SIZE / 2} r={RING_R} fill="none" strokeWidth={RING_STROKE} stroke="currentColor" className="text-border-default" />
      <circle
        data-testid="usage-ring-arc"
        data-used={usedPct(used)}
        data-shown={shown}
        data-tone={tone}
        data-direction={direction}
        className={RING_TONE_CLASS[tone]}
        cx={RING_SIZE / 2}
        cy={RING_SIZE / 2}
        r={RING_R}
        fill="none"
        strokeWidth={RING_STROKE}
        strokeDasharray={`${(shown / 100) * RING_C} ${RING_C}`}
        transform={ringTransform(RING_SIZE, direction)}
      />
    </svg>
  )
}

/** icon + ring + % (each used or remaining per `ring` / `number`). No text label: the tooltip (and aria-label) names which limit this is. */
export function UsageSegment({ testId, icon: IconCmp, used, ring, number, title, stale, className = '' }: {
  testId: string
  icon: Icon
  /** Used share, 0-100. Colour always follows it. `null` = no value: draws icon + 「—」 (never 0 %); only the status row passes it, the bars still hide a missing segment themselves. */
  used: number | null
  /** What the ring shows: 'used' fills with the used share, 'remaining' with what is left. */
  ring: UsageMode
  /** What the number shows. */
  number: UsageMode
  title: string
  stale: boolean
  className?: string
}) {
  if (used === null) {
    return (
      <span data-testid={testId} data-missing="true" role="img" title={title} aria-label={title}
        className={`flex shrink-0 items-center gap-1 select-none text-text-muted ${className}`}>
        <IconCmp size={10} aria-hidden="true" />
        <span>—</span>
      </span>
    )
  }
  return (
    <span
      data-testid={testId}
      data-dim={stale ? 'true' : undefined}
      role="img"
      title={title}
      aria-label={title}
      className={`flex shrink-0 items-center gap-1 tabular-nums select-none ${stale ? 'opacity-50' : ''} ${className}`}
    >
      <IconCmp size={10} className="text-text-muted" aria-hidden="true" />
      <Ring used={used} mode={ring} />
      <span className="text-text-secondary">{shownPct(used, number)}%</span>
    </span>
  )
}

type T = (key: string, params?: Record<string, string | number>) => string

function windowTitle(t: T, nameKey: string, w: UsageWindow, now: number, stale: boolean): string {
  const parts = [t(nameKey, { left: remainingPct(w.pct), pct: usedPct(w.pct) })]
  const left = w.resetsAtMs === null ? null : formatResetsIn(w.resetsAtMs, now)
  if (left) parts.push(t('status.usage.resets_in', { time: left }))
  if (stale) parts.push(t('status.usage.stale'))
  return parts.join(' — ')
}

/** The 5h and 7d rings; shared by both bars. */
function LimitSegments({ fiveHour, sevenDay, stale, now, idPrefix }: {
  fiveHour: UsageWindow | null
  sevenDay: UsageWindow | null
  stale: boolean
  now: number
  idPrefix: string
}) {
  const t = useI18nStore((s) => s.t)
  return (
    <>
      {fiveHour && (
        <UsageSegment
          testId={`${idPrefix}-five-hour`}
          icon={Clock}
          used={fiveHour.pct}
          ring="remaining"
          number="remaining"
          title={windowTitle(t, 'status.usage.five_hour', fiveHour, now, stale)}
          stale={stale}
          className="max-[700px]:hidden"
        />
      )}
      {sevenDay && (
        <UsageSegment
          testId={`${idPrefix}-seven-day`}
          icon={CalendarBlank}
          used={sevenDay.pct}
          ring="remaining"
          number="remaining"
          title={windowTitle(t, 'status.usage.seven_day', sevenDay, now, stale)}
          stale={stale}
          className="max-[700px]:hidden"
        />
      )}
    </>
  )
}

/** A terminal session's context window and rate limits, from its latest Claude Code statusLine snapshot. */
export function CcUsageSegments({ hostId, sessionCode }: { hostId: string | null; sessionCode: string | null }) {
  const t = useI18nStore((s) => s.t)
  const entry = useAgentStore((s) => hostId && sessionCode ? s.ccStatus[compositeKey(hostId, sessionCode)] : undefined)
  const now = useNow()
  const usage = parseCcUsage(entry?.raw)
  if (!entry || !usage) return null

  const stale = now - entry.receivedAt > USAGE_STALE_MS
  return (
    <div data-testid="status-usage" className={`flex shrink-0 items-center gap-2 ${usage.context !== null ? 'max-[600px]:hidden' : 'max-[700px]:hidden'}`}>
      {usage.context !== null && (
        <UsageSegment
          testId="status-seg-usage-context"
          icon={Brain}
          used={usage.context}
          ring="used"
          number="remaining"
          title={[t('status.usage.context', { left: remainingPct(usage.context), pct: usedPct(usage.context) }), stale ? t('status.usage.stale') : ''].filter(Boolean).join(' — ')}
          stale={stale}
          className="max-[600px]:hidden"
        />
      )}
      <LimitSegments fiveHour={usage.fiveHour} sevenDay={usage.sevenDay} stale={stale} now={now} idPrefix="status-seg-usage" />
    </div>
  )
}

/** A worker pane's host quota (the account the Nexen host is logged in as). */
export function HostQuotaSegments({ hostId }: { hostId: string }) {
  const { quota, fetchedAt } = useNexHostQuota(hostId)
  const now = useNow()
  if (!quota) return null
  const resetsAtMs = epochToMs(quota.resets_at)
  // Nexen reports one reset time for the reading; it is shown on the 5-hour window, the one it belongs to.
  const stale = now - fetchedAt > USAGE_STALE_MS
  return (
    <div data-testid="status-usage" className="flex shrink-0 items-center gap-2 max-[700px]:hidden">
      <LimitSegments
        fiveHour={{ pct: quota.five_hour_pct, resetsAtMs }}
        sevenDay={{ pct: quota.seven_day_pct, resetsAtMs: null }}
        stale={stale}
        now={now}
        idPrefix="status-seg-quota"
      />
    </div>
  )
}

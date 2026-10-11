// spa/src/components/status/UsageSegments.tsx — the usage readouts both status bars share: the context window and
// the 5-hour / weekly limits. `CcUsageSegments` reads a terminal session's context plus the host's freshest 5h / 7d (`usageFor`); nothing dims for age (user 2026-10-12);
// `HostQuotaSegments` reads the Nexen host's quota for a worker pane. Both draw through `UsageSegment`, so a
// percentage looks the same wherever it comes from. Hidden when there is nothing to show.
import { useEffect, useState } from 'react'
import { useAgentStore } from '../../stores/useAgentStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNexHostQuota } from '../../hooks/useNexHostQuota'
import {
  epochToMs, formatResetsIn, remainingPct, ringGeometry, ringTransform, usageFor, usedPct,
  type UsageTone, type UsageWindow,
} from '../../lib/usage-display'
import type { Icon } from '@phosphor-icons/react'
import { Brain, CalendarBlank, Clock } from '@phosphor-icons/react'

/** Re-renders the caller every `ms` so "resets in" ages without a new snapshot arriving. */
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
export function UsageSegment({ testId, icon: IconCmp, used, ring, number, title, className = '' }: {
  testId: string
  icon: Icon
  /** Used share, 0-100. Colour always follows it. `null` = no value: draws icon + 「—」 (never 0 %); only the status row passes it, the bars still hide a missing segment themselves. */
  used: number | null
  /** What the ring shows: 'used' fills with the used share, 'remaining' with what is left. */
  ring: UsageMode
  /** What the number shows. */
  number: UsageMode
  title: string
  className?: string
}) {
  if (used === null) {
    return (
      <span data-testid={testId} data-missing="true" role="img" title={title} aria-label={title}
        className={`flex shrink-0 items-center gap-1 select-none text-text-secondary ${className}`}>
        <IconCmp size={10} aria-hidden="true" />
        <span>—</span>
      </span>
    )
  }
  return (
    <span
      data-testid={testId}
      role="img"
      title={title}
      aria-label={title}
      className={`flex shrink-0 items-center gap-1 tabular-nums select-none ${className}`}
    >
      <IconCmp size={10} className="text-text-secondary" aria-hidden="true" />
      <Ring used={used} mode={ring} />
      <span className="text-text-secondary">{shownPct(used, number)}%</span>
    </span>
  )
}

type T = (key: string, params?: Record<string, string | number>) => string

function windowTitle(t: T, nameKey: string, w: UsageWindow, now: number): string {
  const parts = [t(nameKey, { left: remainingPct(w.pct), pct: usedPct(w.pct) })]
  const left = w.resetsAtMs === null ? null : formatResetsIn(w.resetsAtMs, now)
  if (left) parts.push(t('status.usage.resets_in', { time: left }))
  return parts.join(' — ')
}

/** A window whose reset time has passed has rolled over: 0% used (user 2026-10-12). No reset time = the value as given. */
function rolledOver(w: UsageWindow | null, now: number): UsageWindow | null {
  return w && w.resetsAtMs !== null && w.resetsAtMs <= now ? { ...w, pct: 0 } : w
}

/** Re-renders once when the nearest upcoming reset time is crossed (one timer, cleared on unmount; no polling). */
function useResetTick(windows: Array<UsageWindow | null>): void {
  const [, setTick] = useState(0)
  const nexts = windows.map((w) => w?.resetsAtMs ?? null).filter((m): m is number => m !== null && m > Date.now())
  const next = nexts.length ? Math.min(...nexts) : null
  useEffect(() => {
    if (next === null) return
    const id = setTimeout(() => setTick((n) => n + 1), Math.min(Math.max(next - Date.now(), 0) + 50, 2_000_000_000))
    return () => clearTimeout(id)
  }, [next])
}

/** The 5h and 7d rings; shared by both bars. */
function LimitSegments({ fiveHour: fiveHourIn, sevenDay: sevenDayIn, now: nowIn, idPrefix }: {
  fiveHour: UsageWindow | null
  sevenDay: UsageWindow | null
  now: number
  idPrefix: string
}) {
  const t = useI18nStore((s) => s.t)
  useResetTick([fiveHourIn, sevenDayIn])
  const now = Math.max(nowIn, Date.now())
  const fiveHour = rolledOver(fiveHourIn, now)
  const sevenDay = rolledOver(sevenDayIn, now)
  return (
    <>
      {fiveHour && (
        <UsageSegment
          testId={`${idPrefix}-five-hour`}
          icon={Clock}
          used={fiveHour.pct}
          ring="remaining"
          number="remaining"
          title={windowTitle(t, 'status.usage.five_hour', fiveHour, now)}
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
          title={windowTitle(t, 'status.usage.seven_day', sevenDay, now)}
          className="max-[700px]:hidden"
        />
      )}
    </>
  )
}

/** A terminal session's context window and rate limits, from its latest Claude Code statusLine snapshot. */
export function CcUsageSegments({ hostId, sessionCode }: { hostId: string | null; sessionCode: string | null }) {
  const t = useI18nStore((s) => s.t)
  const ccStatus = useAgentStore((s) => s.ccStatus)
  const now = useNow()
  const usage = hostId && sessionCode ? usageFor(hostId, sessionCode, ccStatus) : null
  if (!usage) return null

  return (
    <div data-testid="status-usage" className={`flex shrink-0 items-center gap-2 ${usage.context !== null ? 'max-[600px]:hidden' : 'max-[700px]:hidden'}`}>
      {usage.context !== null && (
        <UsageSegment
          testId="status-seg-usage-context"
          icon={Brain}
          used={usage.context}
          ring="used"
          number="remaining"
          title={t('status.usage.context', { left: remainingPct(usage.context), pct: usedPct(usage.context) })}
          className="max-[600px]:hidden"
        />
      )}
      <LimitSegments fiveHour={usage.fiveHour} sevenDay={usage.sevenDay} now={now} idPrefix="status-seg-usage" />
    </div>
  )
}

/** A worker pane's host quota (the account the Nexen host is logged in as). */
export function HostQuotaSegments({ hostId }: { hostId: string }) {
  const { quota } = useNexHostQuota(hostId)
  const now = useNow()
  if (!quota) return null
  const resetsAtMs = epochToMs(quota.resets_at)
  // Nexen reports one reset time for the reading; it is shown on the 5-hour window, the one it belongs to.
  return (
    <div data-testid="status-usage" className="flex shrink-0 items-center gap-2 max-[700px]:hidden">
      <LimitSegments
        fiveHour={{ pct: quota.five_hour_pct, resetsAtMs }}
        sevenDay={{ pct: quota.seven_day_pct, resetsAtMs: null }}
        now={now}
        idPrefix="status-seg-quota"
      />
    </div>
  )
}

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
  USAGE_STALE_MS, epochToMs, formatResetsIn, parseCcUsage, usageTone,
  type UsageTone, type UsageWindow,
} from '../../lib/usage-display'
import { Separator } from './StatusSegments'

const TONE_CLASS: Record<UsageTone, string> = {
  ok: 'text-text-secondary',
  warn: 'text-status-warning',
  danger: 'text-status-error',
}

/** Re-renders the caller every `ms` so "stale" and "resets in" age without a new snapshot arriving. */
function useNow(ms = 30_000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms)
    return () => clearInterval(id)
  }, [ms])
  return now
}

export function UsageSegment({ testId, label, pct, title, stale, className = '' }: {
  testId: string
  label: string
  pct: number
  title: string
  stale: boolean
  className?: string
}) {
  const tone = usageTone(pct)
  return (
    <span
      data-testid={testId}
      data-tone={tone}
      data-dim={stale ? 'true' : undefined}
      title={title}
      className={`shrink-0 tabular-nums select-none ${TONE_CLASS[tone]} ${stale ? 'opacity-50' : ''} ${className}`}
    >
      {label} {Math.round(pct)}%
    </span>
  )
}

type T = (key: string, params?: Record<string, string | number>) => string

function windowTitle(t: T, nameKey: string, w: UsageWindow, now: number, stale: boolean): string {
  const parts = [t(nameKey, { pct: Math.round(w.pct) })]
  const left = w.resetsAtMs === null ? null : formatResetsIn(w.resetsAtMs, now)
  if (left) parts.push(t('status.usage.resets_in', { time: left }))
  if (stale) parts.push(t('status.usage.stale'))
  return parts.join(' — ')
}

/** The 5h and 7d segments, each preceded by a separator; shared by both bars. */
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
        <>
          <Separator className="max-[700px]:hidden" />
          <UsageSegment
            testId={`${idPrefix}-five-hour`}
            label={t('status.usage.five_hour_short')}
            pct={fiveHour.pct}
            title={windowTitle(t, 'status.usage.five_hour', fiveHour, now, stale)}
            stale={stale}
            className="max-[700px]:hidden"
          />
        </>
      )}
      {sevenDay && (
        <>
          <Separator className="max-[700px]:hidden" />
          <UsageSegment
            testId={`${idPrefix}-seven-day`}
            label={t('status.usage.seven_day_short')}
            pct={sevenDay.pct}
            title={windowTitle(t, 'status.usage.seven_day', sevenDay, now, stale)}
            stale={stale}
            className="max-[700px]:hidden"
          />
        </>
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
    <>
      {usage.context !== null && (
        <>
          <Separator className="max-[600px]:hidden" />
          <UsageSegment
            testId="status-seg-usage-context"
            label={t('status.usage.context_short')}
            pct={usage.context}
            title={[t('status.usage.context', { pct: Math.round(usage.context) }), stale ? t('status.usage.stale') : ''].filter(Boolean).join(' — ')}
            stale={stale}
            className="max-[600px]:hidden"
          />
        </>
      )}
      <LimitSegments fiveHour={usage.fiveHour} sevenDay={usage.sevenDay} stale={stale} now={now} idPrefix="status-seg-usage" />
    </>
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
    <LimitSegments
      fiveHour={{ pct: quota.five_hour_pct, resetsAtMs }}
      sevenDay={{ pct: quota.seven_day_pct, resetsAtMs: null }}
      stale={stale}
      now={now}
      idPrefix="status-seg-quota"
    />
  )
}

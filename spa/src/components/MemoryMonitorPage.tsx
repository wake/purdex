import { useEffect, useRef, useState } from 'react'
import {
  fetchMonitorConfig,
  fetchMonitorSnapshot,
  type MonitorConfig,
  type MonitorHostDisk,
  type MonitorHostMemory,
  type MonitorSnapshot,
} from '../lib/host-api'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'

type LoadState = 'loading' | 'ready' | 'error'

interface MonitorData {
  hostId: string
  config: MonitorConfig
  snapshot: MonitorSnapshot
}

interface MonitorError {
  hostId: string
  message: string
}

export function MemoryMonitorPage() {
  const t = useI18nStore((s) => s.t)
  const activeHostId = useHostStore((s) => s.activeHostId)
  const host = useHostStore((s) => (activeHostId ? s.hosts[activeHostId] : undefined))
  const [data, setData] = useState<MonitorData | null>(null)
  const [error, setError] = useState<MonitorError | null>(null)
  const retryIntervalMS = useRef(5000)

  useEffect(() => {
    if (!activeHostId || !host) return

    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    retryIntervalMS.current = 5000

    const load = async () => {
      const requestHostId = activeHostId
      queueMicrotask(() => {
        if (cancelled) return
        setError((current) => (current?.hostId === requestHostId ? null : current))
      })

      try {
        const nextConfig = await fetchMonitorConfig(requestHostId)
        if (cancelled) return
        retryIntervalMS.current = nextConfig.refresh_interval_ms
        const nextSnapshot = await fetchMonitorSnapshot(requestHostId)
        if (cancelled) return
        setData({ hostId: requestHostId, config: nextConfig, snapshot: nextSnapshot })
        timer = setTimeout(load, nextConfig.refresh_interval_ms)
      } catch (err: unknown) {
        if (cancelled) return
        setError({ hostId: requestHostId, message: err instanceof Error ? err.message : String(err) })
        timer = setTimeout(load, retryIntervalMS.current)
      }
    }

    void load()

    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
  }, [activeHostId, host])

  if (!activeHostId || !host) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('performance_monitor.no_host')}</p>
      </div>
    )
  }

  const currentData = data?.hostId === activeHostId ? data : null
  const currentError = error?.hostId === activeHostId ? error : null
  const loadState: LoadState = currentData ? 'ready' : currentError ? 'error' : 'loading'
  const snapshot = currentData?.snapshot ?? null
  const config = currentData?.config ?? null

  return (
    <div className="flex-1 overflow-y-auto bg-bg-base">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-5 p-5">
        <header className="flex flex-col gap-1 border-b border-border-subtle pb-4">
          <p className="text-xs uppercase tracking-[0.28em] text-text-muted">{host.name}</p>
          <h2 className="text-2xl font-semibold text-text-primary">{t('performance_monitor.title')}</h2>
          <p className="text-sm text-text-muted">{t('performance_monitor.subtitle')}</p>
        </header>

        {loadState === 'loading' && (
          <div role="status" className="rounded-xl border border-border-subtle bg-bg-surface p-4 text-sm text-text-muted">
            {t('performance_monitor.loading')}
          </div>
        )}

        {currentError && (
          <div role="alert" className="rounded-xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-300">
            {t('performance_monitor.error', { reason: currentError?.message ?? t('performance_monitor.unknown_error') })}
          </div>
        )}

        {snapshot && (
          <section className="rounded-2xl border border-border-subtle bg-bg-surface/80 p-4 shadow-sm">
            <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
              <div>
                <h3 className="text-base font-semibold text-text-primary">{t('performance_monitor.host_summary')}</h3>
                <p className="text-xs text-text-muted">
                  {t('performance_monitor.sampled', { time: formatSampleTime(snapshot.sampled_at) })}
                </p>
              </div>
              <div className="rounded-full border border-border-subtle px-3 py-1 text-xs text-text-muted">
                {t('performance_monitor.refresh', {
                  seconds: Math.round((config ?? snapshot.config).refresh_interval_ms / 1000),
                })}
              </div>
            </div>

            <div className="grid gap-3 md:grid-cols-3">
              <MetricCard
                label={t('performance_monitor.cpu')}
                primary={formatCPU(snapshot.host.cpu.percent, snapshot.host.cpu.unavailable_reason, t)}
                detail={snapshot.host.cpu.unavailable_reason ? t('performance_monitor.unavailable') : t('performance_monitor.host_cpu_detail')}
              />
              <MetricCard
                label={t('performance_monitor.memory')}
                primary={formatBytePair(snapshot.host.memory, t)}
                detail={formatPercent(snapshot.host.memory.used_percent, t)}
              />
              <MetricCard
                label={t('performance_monitor.disk')}
                primary={formatBytePair(snapshot.host.disk, t)}
                detail={formatPercent(snapshot.host.disk.used_percent, t)}
              />
            </div>
          </section>
        )}
      </div>
    </div>
  )
}

function MetricCard({ label, primary, detail }: { label: string; primary: string; detail: string }) {
  return (
    <div className="rounded-xl border border-border-subtle bg-bg-elevated p-4">
      <div className="text-xs uppercase tracking-[0.18em] text-text-muted">{label}</div>
      <div className="mt-3 text-xl font-semibold text-text-primary">{primary}</div>
      <div className="mt-1 text-xs text-text-muted">{detail}</div>
    </div>
  )
}

function formatCPU(percent: number | null, reason: string | null, t: (key: string) => string) {
  if (percent === null) return reason === 'pending' ? t('performance_monitor.pending') : formatUnavailableReason(reason, t)
  return `${percent.toFixed(1)}%`
}

function formatBytePair(metric: MonitorHostMemory | MonitorHostDisk, t: (key: string) => string) {
  if (metric.used_bytes === null || metric.total_bytes === null) return formatUnavailableReason(metric.unavailable_reason, t)
  return `${formatBytes(metric.used_bytes)} / ${formatBytes(metric.total_bytes)}`
}

function formatPercent(percent: number | null, t: (key: string) => string) {
  if (percent === null) return t('performance_monitor.unavailable')
  return `${percent.toFixed(1)}%`
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  const formatted = value >= 10 || Number.isInteger(value) ? value.toFixed(0) : value.toFixed(1)
  return `${formatted} ${units[unitIndex]}`
}

function formatUnavailableReason(reason: string | null, t: (key: string) => string) {
  if (!reason) return t('performance_monitor.unavailable')
  const translated = t(`performance_monitor.reason.${reason}`)
  return translated.startsWith('performance_monitor.reason.') ? t('performance_monitor.unavailable') : translated
}

function formatSampleTime(sampledAt: number) {
  return new Date(sampledAt).toLocaleTimeString()
}

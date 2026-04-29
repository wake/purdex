import { useEffect, useRef, useState } from 'react'
import { collectLeaves } from '../lib/pane-tree'
import {
  fetchMonitorConfig,
  fetchMonitorSnapshot,
  type MonitorConfig,
  type MonitorHostDisk,
  type MonitorHostMemory,
  type MonitorSessionDaemonMetrics,
  type MonitorSnapshot,
} from '../lib/host-api'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import type { PaneContent, Tab } from '../types/tab'

type LoadState = 'loading' | 'ready' | 'error'

const MIN_SECONDARY_SNAPSHOT_TIMEOUT_MS = 1000

interface MonitorData {
  hostId: string
  activeHostKey: string
  snapshotHostKey: string
  config: MonitorConfig
  snapshotsByHostId: Record<string, MonitorSnapshot>
}

interface MonitorError {
  hostId: string
  message: string
}

interface PaneRow {
  tabId: string
  paneId: string
  kind: PaneContent['kind']
  content: PaneContent
}

interface SnapshotHostTarget {
  hostId: string
  key: string
}

export function MemoryMonitorPage() {
  const t = useI18nStore((s) => s.t)
  const activeHostId = useHostStore((s) => s.activeHostId)
  const hosts = useHostStore((s) => s.hosts)
  const host = activeHostId ? hosts[activeHostId] : undefined
  const activeHostKey = host ? snapshotHostTargetKey(host) : ''
  const tabs = useTabStore((s) => s.tabs)
  const tabOrder = useTabStore((s) => s.tabOrder)
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const [data, setData] = useState<MonitorData | null>(null)
  const [error, setError] = useState<MonitorError | null>(null)
  const retryIntervalMS = useRef(5000)
  const activeWorkspace = activeWorkspaceId ? workspaces.find((workspace) => workspace.id === activeWorkspaceId) : undefined
  const paneRows = collectPaneRows(tabs, activeWorkspace?.tabs ?? tabOrder)
  const snapshotHostTargets = collectSnapshotHostTargets(activeHostId, paneRows, hosts)
  const snapshotHostIdKey = snapshotHostTargets.map((target) => target.hostId).join('\0')
  const snapshotHostKey = snapshotHostTargets.map((target) => target.key).join('\0')

  useEffect(() => {
    if (!activeHostId || !host) return

    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const secondaryInFlight = new Set<string>()
    retryIntervalMS.current = 5000

    const load = async () => {
      const requestHostId = activeHostId
      const requestActiveHostKey = activeHostKey
      const requestSnapshotHostKey = snapshotHostKey
      const requestHostIds = snapshotHostIdKey ? snapshotHostIdKey.split('\0') : []
      queueMicrotask(() => {
        if (cancelled) return
        setError((current) => (current?.hostId === requestHostId ? null : current))
      })

      try {
        const nextConfig = await fetchMonitorConfig(requestHostId)
        if (cancelled) return
        retryIntervalMS.current = nextConfig.refresh_interval_ms
        const activeSnapshot = await fetchMonitorSnapshot(requestHostId)
        if (cancelled) return
        setData((current) => ({
          hostId: requestHostId,
          activeHostKey: requestActiveHostKey,
          snapshotHostKey: requestSnapshotHostKey,
          config: nextConfig,
          snapshotsByHostId: {
            ...preserveSecondarySnapshots(
              current?.snapshotHostKey === requestSnapshotHostKey ? current : null,
              requestHostId,
              requestHostIds,
            ),
            [requestHostId]: activeSnapshot,
          },
        }))
        timer = setTimeout(load, nextConfig.refresh_interval_ms)

        const secondaryHostIds = requestHostIds.filter((hostId) => hostId !== requestHostId)
        const secondaryTimeoutMS = Math.max(nextConfig.refresh_interval_ms, MIN_SECONDARY_SNAPSHOT_TIMEOUT_MS)
        for (const hostId of secondaryHostIds) {
          if (secondaryInFlight.has(hostId)) continue
          secondaryInFlight.add(hostId)
          void fetchSnapshotResult(hostId, secondaryTimeoutMS).then((result) => {
            if (cancelled) return
            secondaryInFlight.delete(hostId)

            const snapshot = result.snapshot
            if (snapshot) {
              setData((current) => {
                if (current?.hostId !== requestHostId) return current
                return {
                  ...current,
                  snapshotsByHostId: { ...current.snapshotsByHostId, [result.hostId]: snapshot },
                }
              })
            }

            if (result.error) setError({ hostId: requestHostId, message: formatSnapshotError(result.hostId, result.error) })
          })
        }
      } catch (err: unknown) {
        if (cancelled) return
        setError({ hostId: requestHostId, message: err instanceof Error ? err.message : String(err) })
        timer = setTimeout(load, retryIntervalMS.current)
      }
    }

    void load()

    return () => {
      cancelled = true
      secondaryInFlight.clear()
      if (timer) clearTimeout(timer)
    }
  }, [activeHostId, activeHostKey, host, snapshotHostIdKey, snapshotHostKey])

  if (!activeHostId || !host) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('performance_monitor.no_host')}</p>
      </div>
    )
  }

  const currentData = data?.hostId === activeHostId ? data : null
  const currentError = error?.hostId === activeHostId ? error : null
  const activeHostKeyMatches = currentData?.activeHostKey === activeHostKey
  const loadState: LoadState = currentData && activeHostKeyMatches ? 'ready' : currentError ? 'error' : 'loading'
  const snapshotsByHostId = currentData?.snapshotsByHostId ?? {}
  const rowSnapshotsByHostId = currentData?.snapshotHostKey === snapshotHostKey
    ? snapshotsByHostId
    : activeHostKeyMatches ? preserveActiveSnapshot(activeHostId, snapshotsByHostId) : {}
  const snapshot = activeHostKeyMatches && activeHostId ? snapshotsByHostId[activeHostId] ?? null : null
  const config = currentData?.config ?? null
  const validSnapshotHostIds = snapshotHostIdKey ? snapshotHostIdKey.split('\0') : []

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

        <section className="rounded-2xl border border-border-subtle bg-bg-surface/80 p-4 shadow-sm">
          <div className="mb-4">
            <h3 className="text-base font-semibold text-text-primary">{t('performance_monitor.open_panes')}</h3>
            <p className="text-xs text-text-muted">{t('performance_monitor.open_panes_desc')}</p>
          </div>

          <div className="overflow-x-auto text-xs">
            <table className="min-w-[720px] w-full border-collapse">
              <thead>
                <tr className="border-b border-border-subtle uppercase tracking-[0.16em] text-text-muted">
                  <th className="px-3 py-2 text-left font-medium">{t('performance_monitor.col.tab')}</th>
                  <th className="px-3 py-2 text-left font-medium">{t('performance_monitor.col.pane')}</th>
                  <th className="px-3 py-2 text-left font-medium">{t('performance_monitor.col.kind')}</th>
                  <th className="px-3 py-2 text-left font-medium">{t('performance_monitor.col.client')}</th>
                  <th className="px-3 py-2 text-left font-medium">{t('performance_monitor.col.daemon')}</th>
                </tr>
              </thead>
              <tbody>
                {paneRows.length === 0 ? (
                  <tr>
                    <td colSpan={5} className="px-3 py-4 text-center text-sm text-text-muted">
                      {t('performance_monitor.no_open_panes')}
                    </td>
                  </tr>
                ) : paneRows.map((row) => (
                  <tr
                    key={`${row.tabId}:${row.paneId}`}
                    data-testid={`monitor-row-${row.tabId}-${row.paneId}`}
                    className="border-b border-border-subtle last:border-b-0"
                  >
                    <td className="max-w-48 truncate px-3 py-2 font-mono text-text-primary">{row.tabId}</td>
                    <td className="max-w-48 truncate px-3 py-2 font-mono text-text-primary">{row.paneId}</td>
                    <td className="px-3 py-2 text-text-muted">{row.kind}</td>
                    <td className="px-3 py-2 text-text-muted">{t('performance_monitor.not_wired')}</td>
                    <td className="px-3 py-2 text-text-muted">
                      <DaemonMetricCell row={row} snapshotsByHostId={rowSnapshotsByHostId} validHostIds={validSnapshotHostIds} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
  )
}

function collectPaneRows(tabs: Record<string, Tab>, tabOrder: string[]): PaneRow[] {
  const rows: PaneRow[] = []
  for (const tabId of tabOrder) {
    const tab = tabs[tabId]
    if (!tab) continue
    for (const pane of collectLeaves(tab.layout)) {
      rows.push({ tabId, paneId: pane.id, kind: pane.content.kind, content: pane.content })
    }
  }
  return rows
}

function preserveSecondarySnapshots(current: MonitorData | null, activeHostId: string, requestHostIds: string[]) {
  if (!current) return {}
  return Object.fromEntries(
    requestHostIds.flatMap((hostId) => {
      const snapshot = hostId === activeHostId ? null : current.snapshotsByHostId[hostId]
      return snapshot ? [[hostId, snapshot] as const] : []
    }),
  )
}

function preserveActiveSnapshot(activeHostId: string | null, snapshotsByHostId: Record<string, MonitorSnapshot>) {
  if (!activeHostId || !snapshotsByHostId[activeHostId]) return {}
  return { [activeHostId]: snapshotsByHostId[activeHostId] }
}

function collectSnapshotHostTargets(activeHostId: string | null, rows: PaneRow[], hosts: Record<string, HostConfig>): SnapshotHostTarget[] {
  const rowHostIds = new Set<string>()

  for (const row of rows) {
    if (row.content.kind === 'tmux-session' && hosts[row.content.hostId] && row.content.hostId !== activeHostId) {
      rowHostIds.add(row.content.hostId)
    }
  }

  const hostIds = activeHostId && hosts[activeHostId]
    ? [activeHostId, ...[...rowHostIds].sort()]
    : [...rowHostIds].sort()
  return hostIds.map((hostId) => ({ hostId, key: snapshotHostTargetKey(hosts[hostId]) }))
}

function snapshotHostTargetKey(host: HostConfig) {
  return JSON.stringify([host.id, host.ip, host.port, host.token ?? ''])
}

async function fetchSnapshotResult(hostId: string, timeoutMS?: number): Promise<{ hostId: string; snapshot?: MonitorSnapshot; error?: unknown }> {
  let timeout: ReturnType<typeof setTimeout> | undefined
  try {
    const snapshot = timeoutMS === undefined
      ? await fetchMonitorSnapshot(hostId)
      : await Promise.race([
        fetchMonitorSnapshot(hostId),
        new Promise<MonitorSnapshot>((_, reject) => {
          timeout = setTimeout(() => reject(new Error('snapshot timed out')), timeoutMS)
        }),
      ])
    return { hostId, snapshot }
  } catch (error) {
    return { hostId, error }
  } finally {
    if (timeout) clearTimeout(timeout)
  }
}

function formatSnapshotError(hostId: string, error: unknown) {
  const message = error instanceof Error ? error.message : String(error)
  return `${hostId}: ${message}`
}

function DaemonMetricCell({
  row,
  snapshotsByHostId,
  validHostIds,
}: {
  row: PaneRow
  snapshotsByHostId: Record<string, MonitorSnapshot>
  validHostIds: string[]
}) {
  const t = useI18nStore((s) => s.t)
  const metrics = findDaemonMetrics(row, snapshotsByHostId, validHostIds)
  if (!metrics) return t('performance_monitor.not_wired')
  if (metrics.unavailable_reason) return formatUnavailableReason(metrics.unavailable_reason, t)

  return (
    <div className="flex flex-wrap gap-x-3 gap-y-1">
      <span>{t('performance_monitor.daemon_cpu', { value: formatDaemonCPU(metrics.cpu_percent, t) })}</span>
      <span>{t('performance_monitor.daemon_memory', { value: formatDaemonMemory(metrics.memory_bytes, t) })}</span>
      <span>{formatProcessCount(metrics.process_count, t)}</span>
    </div>
  )
}

function findDaemonMetrics(row: PaneRow, snapshotsByHostId: Record<string, MonitorSnapshot>, validHostIds: string[]): MonitorSessionDaemonMetrics | null {
  const content = row.content
  if (content.kind !== 'tmux-session' || !validHostIds.includes(content.hostId)) return null
  const snapshot = snapshotsByHostId[content.hostId]
  if (!snapshot) return null
  return snapshot.sessions.find((session) => session.session_code === content.sessionCode)?.daemon ?? null
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

function formatDaemonCPU(percent: number | null, t: (key: string) => string) {
  if (percent === null) return t('performance_monitor.pending')
  return `${percent.toFixed(1)}%`
}

function formatDaemonMemory(bytes: number | null, t: (key: string) => string) {
  if (bytes === null) return t('performance_monitor.unavailable')
  return formatBytes(bytes)
}

function formatProcessCount(count: number | null, t: (key: string, params?: Record<string, string | number>) => string) {
  if (count === null) return t('performance_monitor.processes_unavailable')
  return t(count === 1 ? 'performance_monitor.process_count_one' : 'performance_monitor.process_count_other', { count })
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

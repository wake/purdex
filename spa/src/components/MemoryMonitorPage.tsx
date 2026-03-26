import { useEffect, useState } from 'react'
import { useI18nStore } from '../stores/useI18nStore'

interface TabMetrics {
  paneId: string
  kind: string
  memoryKB: number
  cpuPercent: number
}

export function MemoryMonitorPage() {
  const t = useI18nStore((s) => s.t)
  const [metrics, setMetrics] = useState<TabMetrics[]>([])

  useEffect(() => {
    if (!window.electronAPI) return

    window.electronAPI.getProcessMetrics().then(setMetrics)
    const unsub = window.electronAPI.onMetricsUpdate(setMetrics)
    return unsub
  }, [])

  if (!window.electronAPI) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('monitor.requires_app')}</p>
      </div>
    )
  }

  return (
    <div className="flex-1 flex flex-col p-4 overflow-y-auto">
      <h2 className="text-lg text-text-secondary mb-4">{t('monitor.title')}</h2>
      <div className="text-xs font-mono">
        <div className="grid grid-cols-[1fr_100px_80px_80px_80px] gap-2 px-3 py-2 text-text-muted border-b border-border-default">
          <div>{t('monitor.col.tab')}</div>
          <div>{t('monitor.col.kind')}</div>
          <div>{t('monitor.col.memory')}</div>
          <div>{t('monitor.col.cpu')}</div>
          <div>{t('monitor.col.state')}</div>
        </div>
        {metrics.map((m) => (
          <div key={m.paneId} className="grid grid-cols-[1fr_100px_80px_80px_80px] gap-2 px-3 py-2 border-b border-border-subtle">
            <div className="text-text-primary truncate">{m.paneId}</div>
            <div className="text-text-muted">{m.kind}</div>
            <div className="text-text-primary">
              {m.memoryKB > 0 ? `${Math.round(m.memoryKB / 1024)} MB` : t('monitor.shared')}
            </div>
            <div className="text-text-primary">
              {m.cpuPercent > 0 ? `${m.cpuPercent.toFixed(1)}%` : t('monitor.shared')}
            </div>
            <div className="text-text-muted">{t('monitor.state.active')}</div>
          </div>
        ))}
        {metrics.length === 0 && (
          <div className="px-3 py-4 text-text-muted text-center">{t('monitor.shared')}</div>
        )}
      </div>
    </div>
  )
}

import { ChartBar } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'

export function MemoryMonitorNewTabSection({ onSelect }: NewTabProviderProps) {
  const t = useI18nStore((s) => s.t)

  return (
    <button
      onClick={() => onSelect({ kind: 'memory-monitor' })}
      className="flex items-center gap-2 px-2 py-1.5 w-full text-left text-xs text-text-secondary hover:bg-surface-hover rounded-md transition-colors"
    >
      <ChartBar size={16} className="text-text-muted flex-shrink-0" />
      {t('monitor.title')}
    </button>
  )
}

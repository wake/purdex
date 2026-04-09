import { useHistoryStore } from '../stores/useHistoryStore'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import type { PaneRendererProps } from '../lib/module-registry'
import { useI18nStore } from '../stores/useI18nStore'

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function HistoryPage(_props: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const browseHistory = useHistoryStore((s) => s.browseHistory)
  const tabs = useTabStore((s) => s.tabs)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const addTab = useTabStore((s) => s.addTab)

  // Show records in reverse chronological order
  const sorted = [...browseHistory].reverse()

  return (
    <div className="flex-1 flex flex-col overflow-y-auto p-4">
      <h2 className="text-sm font-medium text-text-secondary mb-4">{t('page.history.title')}</h2>
      {sorted.length === 0 && (
        <p className="text-sm text-text-muted">{t('page.history.empty')}</p>
      )}
      {sorted.map((record, i) => {
        const isOpen = !!tabs[record.tabId]
        return (
          <button
            key={`${record.tabId}-${record.visitedAt}-${i}`}
            className="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-white/5 text-left text-sm text-text-primary transition-colors"
            onClick={() => {
              if (isOpen) {
                setActiveTab(record.tabId)
              } else {
                const tab = createTab(record.paneContent)
                addTab(tab)
                setActiveTab(tab.id)
              }
            }}
          >
            <span className="text-text-muted">{record.paneContent.kind}</span>
            <span className={isOpen ? 'text-green-400' : 'text-text-muted'}>
              {isOpen ? t('common.open') : t('common.closed')}
            </span>
            <span className="text-xs text-text-muted ml-auto">
              {new Date(record.visitedAt).toLocaleTimeString()}
            </span>
          </button>
        )
      })}
    </div>
  )
}

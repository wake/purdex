import { useHistoryStore } from '../stores/useHistoryStore'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import type { PaneRendererProps } from '../lib/pane-registry'

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function HistoryPage(_props: PaneRendererProps) {
  const browseHistory = useHistoryStore((s) => s.browseHistory)
  const tabs = useTabStore((s) => s.tabs)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const addTab = useTabStore((s) => s.addTab)

  // Show records in reverse chronological order
  const sorted = [...browseHistory].reverse()

  return (
    <div className="flex-1 flex flex-col overflow-y-auto p-4">
      <h2 className="text-sm font-medium text-text-secondary mb-4">History</h2>
      {sorted.length === 0 && (
        <p className="text-sm text-text-muted">No browsing history yet</p>
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
              {isOpen ? 'Open' : 'Closed'}
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

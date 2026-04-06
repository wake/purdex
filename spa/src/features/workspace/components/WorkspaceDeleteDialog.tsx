import { useState } from 'react'
import { Trash, Warning } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface TabItem {
  id: string
  label: string
}

interface Props {
  workspaceName: string
  tabs: TabItem[]
  onConfirm: (closedTabIds: string[]) => void
  onCancel: () => void
}

export function WorkspaceDeleteDialog({ workspaceName, tabs, onConfirm, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  const [checkedIds, setCheckedIds] = useState<Set<string>>(() => new Set(tabs.map((tab) => tab.id)))

  const toggleTab = (tabId: string) => {
    setCheckedIds((prev) => {
      const next = new Set(prev)
      if (next.has(tabId)) {
        next.delete(tabId)
      } else {
        next.add(tabId)
      }
      return next
    })
  }

  const handleConfirm = () => {
    const closedTabIds = tabs.filter((tab) => checkedIds.has(tab.id)).map((tab) => tab.id)
    onConfirm(closedTabIds)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-md mx-4">
        {/* Header */}
        <div className="flex items-center gap-3 px-5 pt-5 pb-3">
          <div className="w-10 h-10 rounded-full bg-red-500/10 flex items-center justify-center flex-shrink-0">
            <Warning size={20} className="text-red-400" />
          </div>
          <div>
            <h3 className="text-sm font-semibold text-text-primary">
              {t('workspace.delete_title', { name: workspaceName })}
            </h3>
            {tabs.length > 0 && (
              <p className="text-xs text-text-muted mt-0.5">
                {t('workspace.delete_description')}
              </p>
            )}
          </div>
        </div>

        {/* Tab list */}
        {tabs.length > 0 && (
          <div className="px-5 py-3 max-h-48 overflow-y-auto">
            <div className="space-y-1.5">
              {tabs.map((tab) => (
                <label key={tab.id} className="flex items-center gap-2 cursor-pointer group">
                  <input
                    type="checkbox"
                    checked={checkedIds.has(tab.id)}
                    onChange={() => toggleTab(tab.id)}
                    className="rounded border-border-default"
                  />
                  <span className="text-sm text-text-secondary group-hover:text-text-primary truncate">
                    {tab.label}
                  </span>
                </label>
              ))}
            </div>
          </div>
        )}

        {/* Actions */}
        <div className="flex justify-end gap-2 px-5 py-4 border-t border-border-subtle">
          <button
            onClick={onCancel}
            className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer"
          >
            {t('common.cancel')}
          </button>
          <button
            onClick={handleConfirm}
            className="px-3 py-1.5 rounded text-xs bg-red-600 text-white hover:bg-red-500 cursor-pointer flex items-center gap-1.5"
          >
            <Trash size={14} />
            {t('workspace.delete_confirm')}
          </button>
        </div>
      </div>
    </div>
  )
}

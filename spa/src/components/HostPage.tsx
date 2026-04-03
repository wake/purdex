import { useEffect, useMemo, useRef, useState } from 'react'
import type { PaneRendererProps } from '../lib/pane-registry'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { HostSidebar } from './hosts/HostSidebar'
import { OverviewSection, type UndoToastInfo } from './hosts/OverviewSection'
import { SessionsSection } from './hosts/SessionsSection'
import { HooksSection } from './hosts/HooksSection'
import { UploadSection } from './hosts/UploadSection'
import { AddHostDialog } from './hosts/AddHostDialog'

export type HostSubPage = 'overview' | 'sessions' | 'hooks' | 'uploads'

interface Selection {
  hostId: string
  subPage: HostSubPage
}

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function HostPage(_props: PaneRendererProps) {
  const hostOrder = useHostStore((s) => s.hostOrder)
  const activeHostId = useHostStore((s) => s.activeHostId)

  const [selection, setSelection] = useState<Selection>(() => ({
    hostId: activeHostId ?? hostOrder[0] ?? '',
    subPage: 'overview',
  }))
  const [showAddHost, setShowAddHost] = useState(false)
  const [undoToast, setUndoToast] = useState<UndoToastInfo | null>(null)
  const undoTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const t = useI18nStore((s) => s.t)

  // Auto-dismiss undo toast after 5 seconds
  useEffect(() => {
    if (!undoToast) return
    undoTimerRef.current = setTimeout(() => setUndoToast(null), 5000)
    return () => {
      if (undoTimerRef.current) clearTimeout(undoTimerRef.current)
    }
  }, [undoToast])

  // Derive effective selection: reset when selected host no longer exists
  const effectiveSelection = useMemo<Selection>(() => {
    if (selection.hostId && !hostOrder.includes(selection.hostId)) {
      return { hostId: hostOrder[0] ?? '', subPage: 'overview' }
    }
    return selection
  }, [hostOrder, selection])

  const renderContent = () => {
    if (!effectiveSelection.hostId) {
      return <p className="text-text-muted">{t('hosts.no_host_selected')}</p>
    }
    switch (effectiveSelection.subPage) {
      case 'overview':
        return <OverviewSection hostId={effectiveSelection.hostId} onShowUndoToast={setUndoToast} />
      case 'sessions':
        return <SessionsSection hostId={effectiveSelection.hostId} />
      case 'hooks':
        return <HooksSection hostId={effectiveSelection.hostId} />
      case 'uploads':
        return <UploadSection hostId={effectiveSelection.hostId} />
    }
  }

  return (
    <div className="flex h-full">
      <HostSidebar
        selectedHostId={effectiveSelection.hostId}
        selectedSubPage={effectiveSelection.subPage}
        onSelect={(hostId, subPage) => setSelection({ hostId, subPage })}
        onAddHost={() => setShowAddHost(true)}
      />
      <div className="flex-1 overflow-y-auto p-6">
        {renderContent()}
      </div>
      {showAddHost && <AddHostDialog onClose={() => setShowAddHost(false)} />}
      {undoToast && (
        <div className="fixed bottom-4 left-1/2 -translate-x-1/2 bg-zinc-800 border border-zinc-700 rounded-lg px-4 py-3 flex items-center gap-3 shadow-lg z-50">
          <span className="text-sm text-zinc-300">
            {t('hosts.deleted_toast', { name: undoToast.name })}
          </span>
          <button
            className="text-sm text-blue-400 hover:text-blue-300 font-medium cursor-pointer"
            onClick={() => {
              undoToast.restore()
              setUndoToast(null)
            }}
          >
            {t('hosts.undo')}
          </button>
        </div>
      )}
    </div>
  )
}

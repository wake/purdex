import { useMemo, useState } from 'react'
import type { PaneRendererProps } from '../lib/module-registry'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { HostSidebar } from './hosts/HostSidebar'
import { OverviewSection } from './hosts/OverviewSection'
import { SessionsSection } from './hosts/SessionsSection'
import { HooksSection } from './hosts/HooksSection'
import { AgentsSection } from './hosts/AgentsSection'
import { UploadSection } from './hosts/UploadSection'
import { LogsSection } from './hosts/LogsSection'
import { AddHostDialog } from './hosts/AddHostDialog'

export type HostSubPage = 'overview' | 'sessions' | 'hooks' | 'agents' | 'uploads' | 'logs'

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
  const t = useI18nStore((s) => s.t)

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
        return <OverviewSection hostId={effectiveSelection.hostId} />
      case 'sessions':
        return <SessionsSection hostId={effectiveSelection.hostId} />
      case 'hooks':
        return <HooksSection hostId={effectiveSelection.hostId} />
      case 'agents':
        return <AgentsSection hostId={effectiveSelection.hostId} />
      case 'uploads':
        return <UploadSection hostId={effectiveSelection.hostId} />
      case 'logs':
        return <LogsSection hostId={effectiveSelection.hostId} />
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
    </div>
  )
}

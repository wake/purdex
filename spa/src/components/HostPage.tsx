import { useState } from 'react'
import type { PaneRendererProps } from '../lib/pane-registry'
import { useHostStore } from '../stores/useHostStore'
import { HostSidebar } from './hosts/HostSidebar'
import { OverviewSection } from './hosts/OverviewSection'
import { SessionsSection } from './hosts/SessionsSection'

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

  const renderContent = () => {
    if (!selection.hostId) {
      return <p className="text-text-muted">No host selected.</p>
    }
    switch (selection.subPage) {
      case 'overview':
        return <OverviewSection hostId={selection.hostId} />
      case 'sessions':
        return <SessionsSection hostId={selection.hostId} />
      case 'hooks':
      case 'uploads':
        return <p className="text-text-muted">Coming soon.</p>
    }
  }

  return (
    <div className="flex h-full">
      <HostSidebar
        selectedHostId={selection.hostId}
        selectedSubPage={selection.subPage}
        onSelect={(hostId, subPage) => setSelection({ hostId, subPage })}
      />
      <div className="flex-1 overflow-y-auto p-6">
        {renderContent()}
      </div>
    </div>
  )
}

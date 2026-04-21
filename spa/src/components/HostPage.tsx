import { useEffect, useState } from 'react'
import { useLocation } from 'wouter'
import type { PaneRendererProps } from '../lib/module-registry'
import { encodeHostRouteId, isHostSubPage, type HostSubPage } from '../lib/host-routes'
import { parseRoute } from '../lib/route-utils'
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

export type { HostSubPage } from '../lib/host-routes'

interface Selection {
  hostId: string
  subPage: HostSubPage
}

let lastSelection: Selection | null = null

function buildHostPath({ hostId, subPage }: Selection) {
  return `/hosts/${encodeHostRouteId(hostId)}/${subPage}`
}

function getFallbackHostId(hostOrder: string[], activeHostId: string | null) {
  if (activeHostId && hostOrder.includes(activeHostId)) return activeHostId
  return hostOrder[0] ?? null
}

function getFallbackSelection(hostOrder: string[], activeHostId: string | null): Selection | null {
  const hostId = getFallbackHostId(hostOrder, activeHostId)
  if (!hostId) return null

  return {
    hostId,
    subPage: lastSelection?.subPage ?? 'overview',
  }
}

function getFallbackSubPage() {
  return lastSelection?.subPage ?? 'overview'
}

function resolveSelection(location: string, hostOrder: string[], activeHostId: string | null) {
  const parsed = parseRoute(location)
  const isHostRoute = parsed?.kind === 'hosts' || parsed?.kind === 'hosts-invalid'

  if (hostOrder.length === 0) {
    return {
      selection: null,
      canonicalPath: isHostRoute && location !== '/hosts' ? '/hosts' : null as string | null,
      shouldPersistSelection: false,
    }
  }

  const fallbackSelection = getFallbackSelection(hostOrder, activeHostId)
  if (!fallbackSelection) {
    return {
      selection: null,
      canonicalPath: isHostRoute && location !== '/hosts' ? '/hosts' : null as string | null,
      shouldPersistSelection: false,
    }
  }

  if (parsed?.kind === 'hosts') {
    if (parsed.hostId && parsed.subPage) {
      if (hostOrder.includes(parsed.hostId)) {
        return {
          selection: { hostId: parsed.hostId, subPage: parsed.subPage },
          canonicalPath: null as string | null,
          shouldPersistSelection: true,
        }
      }

      return {
        selection: { hostId: fallbackSelection.hostId, subPage: parsed.subPage },
        canonicalPath: buildHostPath({ hostId: fallbackSelection.hostId, subPage: parsed.subPage }),
        shouldPersistSelection: true,
      }
    }

    if (lastSelection) {
      const hostId = hostOrder.includes(lastSelection.hostId) ? lastSelection.hostId : fallbackSelection.hostId
      const selection = { hostId, subPage: lastSelection.subPage }
      return { selection, canonicalPath: buildHostPath(selection), shouldPersistSelection: true }
    }

    return {
      selection: fallbackSelection,
      canonicalPath: buildHostPath(fallbackSelection),
      shouldPersistSelection: true,
    }
  }

  if (parsed?.kind === 'hosts-invalid') {
    const hostId = parsed.hostId && hostOrder.includes(parsed.hostId) ? parsed.hostId : fallbackSelection.hostId
    const selection = {
      hostId,
      subPage: parsed.subPage && isHostSubPage(parsed.subPage) ? parsed.subPage : getFallbackSubPage(),
    }

    return {
      selection,
      canonicalPath: buildHostPath(selection),
      shouldPersistSelection: true,
    }
  }

  return {
    selection: lastSelection ?? fallbackSelection,
    canonicalPath: null as string | null,
    shouldPersistSelection: false,
  }
}

/** @internal test-only — must co-locate to access module-scoped variable */
// eslint-disable-next-line react-refresh/only-export-components
export function resetLastHostSelection() {
  lastSelection = null
}

 
export function HostPage(_props: PaneRendererProps) {
  const [location, setLocation] = useLocation()
  const hostOrder = useHostStore((s) => s.hostOrder)
  const activeHostId = useHostStore((s) => s.activeHostId)
  const [showAddHost, setShowAddHost] = useState(false)
  const t = useI18nStore((s) => s.t)
  const { selection, canonicalPath, shouldPersistSelection } = resolveSelection(location, hostOrder, activeHostId)

  useEffect(() => {
    if (shouldPersistSelection && selection) lastSelection = selection
  }, [selection, shouldPersistSelection])

  useEffect(() => {
    if (canonicalPath && canonicalPath !== location) {
      setLocation(canonicalPath, { replace: true })
    }
  }, [canonicalPath, location, setLocation])

  const renderContent = () => {
    if (!selection?.hostId) {
      return <p className="text-text-muted">{t('hosts.no_host_selected')}</p>
    }
    switch (selection.subPage) {
      case 'overview':
        return <OverviewSection hostId={selection.hostId} />
      case 'sessions':
        return <SessionsSection hostId={selection.hostId} />
      case 'hooks':
        return <HooksSection hostId={selection.hostId} />
      case 'agents':
        return <AgentsSection hostId={selection.hostId} />
      case 'uploads':
        return <UploadSection hostId={selection.hostId} />
      case 'logs':
        return <LogsSection hostId={selection.hostId} />
    }
  }

  return (
    <div className="flex h-full">
      <HostSidebar
        selectedHostId={selection?.hostId ?? ''}
        selectedSubPage={selection?.subPage ?? lastSelection?.subPage ?? 'overview'}
        onSelect={(hostId, subPage) => setLocation(buildHostPath({ hostId, subPage }), { replace: true })}
        onAddHost={() => setShowAddHost(true)}
      />
      <div className="flex-1 overflow-y-auto p-6">
        {renderContent()}
      </div>
      {showAddHost && <AddHostDialog onClose={() => setShowAddHost(false)} />}
    </div>
  )
}

import { useEffect, useState } from 'react'
import { useLocation } from 'wouter'
import type { PaneRendererProps } from '../lib/module-registry'
import { encodeHostRouteId, isHostSubPage, type HostSubPage } from '../lib/host-routes'
import { parseRoute } from '../lib/route-utils'
import { listContributions } from '../lib/settings-contribution-registry'
import type { SettingsContribution, SettingsContextFor } from '../lib/settings-contribution-types'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { HostSidebar } from './hosts/HostSidebar'
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

/**
 * Returns true when a contribution may be selected for `ctx`.
 * A contribution is selectable when its `disabled` predicate is absent or
 * returns false.  Mirrors `SettingsPage.isSelectable` (PR-2 pattern).
 */
function isSelectable(
  c: SettingsContribution<'host'>,
  ctx: SettingsContextFor<'host'>,
): boolean {
  return c.disabled?.(ctx) !== true
}

/**
 * Given a target `hostId` and an optional `requestedSubPage`, return the best
 * selectable subPage:
 *
 * 1. If `requestedSubPage` is provided, exists in the registry AND is
 *    selectable for `hostId` → return it as-is.
 * 2. Otherwise return the first contribution that is selectable for `hostId`.
 * 3. Ultimate safety net: return `'overview'` literal if the registry is empty
 *    or every contribution is disabled.
 *
 * Replaces `getFallbackSubPage()` — strict superset that also checks
 * `disabled(ctx)`.
 */
function pickSelectableSubPage(hostId: string, requestedSubPage: string | null | undefined): HostSubPage {
  const ctx: SettingsContextFor<'host'> = { scope: 'host', hostId }
  const contributions = listContributions('host') as SettingsContribution<'host'>[]

  if (requestedSubPage) {
    const c = contributions.find((c) => c.localId === requestedSubPage)
    if (c && isSelectable(c, ctx)) return requestedSubPage as HostSubPage
  }

  const first = contributions.find((c) => isSelectable(c, ctx))
  return (first?.localId ?? 'overview') as HostSubPage
}

function getFallbackSelection(hostOrder: string[], activeHostId: string | null): Selection | null {
  const hostId = getFallbackHostId(hostOrder, activeHostId)
  if (!hostId) return null

  return {
    hostId,
    subPage: pickSelectableSubPage(hostId, lastSelection?.subPage),
  }
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
      const resolvedHostId = hostOrder.includes(parsed.hostId) ? parsed.hostId : fallbackSelection.hostId
      const resolvedSubPage = pickSelectableSubPage(resolvedHostId, parsed.subPage)
      const needsHostRedirect = resolvedHostId !== parsed.hostId
      const needsSubPageRedirect = resolvedSubPage !== parsed.subPage
      const needsRedirect = needsHostRedirect || needsSubPageRedirect

      if (!needsRedirect) {
        return {
          selection: { hostId: resolvedHostId, subPage: resolvedSubPage },
          canonicalPath: null as string | null,
          shouldPersistSelection: true,
        }
      }

      return {
        selection: { hostId: resolvedHostId, subPage: resolvedSubPage },
        canonicalPath: buildHostPath({ hostId: resolvedHostId, subPage: resolvedSubPage }),
        shouldPersistSelection: true,
      }
    }

    if (lastSelection) {
      const hostId = hostOrder.includes(lastSelection.hostId) ? lastSelection.hostId : fallbackSelection.hostId
      const subPage = pickSelectableSubPage(hostId, lastSelection.subPage)
      const selection = { hostId, subPage }
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
    const rawSubPage = parsed.subPage && isHostSubPage(parsed.subPage) ? parsed.subPage : null
    const selection = {
      hostId,
      subPage: pickSelectableSubPage(hostId, rawSubPage),
    }

    return {
      selection,
      canonicalPath: buildHostPath(selection),
      shouldPersistSelection: true,
    }
  }

  // Non-host route: keep lastSelection if available, clamped to live registry.
  if (lastSelection) {
    const hostId = hostOrder.includes(lastSelection.hostId) ? lastSelection.hostId : fallbackSelection.hostId
    const subPage = pickSelectableSubPage(hostId, lastSelection.subPage)
    return {
      selection: { hostId, subPage },
      canonicalPath: null as string | null,
      shouldPersistSelection: false,
    }
  }

  return {
    selection: fallbackSelection,
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
    const { hostId, subPage } = selection
    const contributions = listContributions('host') as SettingsContribution<'host'>[]
    const contribution = contributions.find((c) => c.localId === subPage)
    if (!contribution) {
      // subPage not in registry — self-heal via resolveSelection redirect.
      // Return null here; the canonicalPath effect will navigate away.
      return null
    }
    const ctx: SettingsContextFor<'host'> = { scope: 'host', hostId }
    // F7 + A.1: disabled contributions must not mount their body component.
    // `isSelectable` is the single predicate; resolveSelection already redirected
    // away, so this is a last-resort guard.
    if (!isSelectable(contribution, ctx)) {
      return null
    }
    const Component = contribution.component
    // Wrap with a key that includes hostId so switching hosts forces a remount
    // and prevents cross-host state leak (PR-4 analog of PR-3 F3).
    return <Component key={`${hostId}:${contribution.id}`} ctx={ctx} />
  }

  // A.3: HostSidebar selectedSubPage comes from pickSelectableSubPage so that
  // the sidebar highlight never lands on a disabled row after a cross-host
  // switch.
  const sidebarHostId = selection?.hostId ?? lastSelection?.hostId ?? ''
  const sidebarSubPage = sidebarHostId
    ? pickSelectableSubPage(sidebarHostId, selection?.subPage ?? lastSelection?.subPage)
    : (listContributions('host')[0]?.localId ?? 'overview') as HostSubPage

  return (
    <div className="flex h-full">
      <HostSidebar
        selectedHostId={selection?.hostId ?? ''}
        selectedSubPage={sidebarSubPage}
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

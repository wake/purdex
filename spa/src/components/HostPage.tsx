import { useEffect, useState } from 'react'
import { useLocation } from 'wouter'
import type { PaneRendererProps } from '../lib/module-registry'
import { encodeHostRouteId, isHostSubPage, type HostSubPage } from '../lib/host-routes'
import { parseRoute } from '../lib/route-utils'
import { listContributions } from '../lib/settings-contribution-registry'
import type { SettingsContribution, SettingsContextFor } from '../lib/settings-contribution-types'
import { useHostStore, type HostRuntime } from '../stores/useHostStore'
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

/**
 * Single source of truth for host-id fallback ordering across both the
 * runtime-independent pre-resolve path (`preResolveHostId`) and the
 * full resolve path (`getFallbackSelection` / `resolveSelection`).
 *
 * Order:
 *   1. lastSelection.hostId (if still in hostOrder)
 *   2. activeHostId (if still in hostOrder)
 *   3. hostOrder[0]
 *
 * Returning null only when hostOrder is empty.
 *
 * Extracting this helper guarantees both callsites agree on fallback
 * semantics; equivalence regression covered by host-selection-utils tests.
 *
 * @internal
 */
// eslint-disable-next-line react-refresh/only-export-components
export function pickHostIdFallback(
  hostOrder: string[],
  activeHostId: string | null,
  lastSel: { hostId: string } | null,
): string | null {
  if (hostOrder.length === 0) return null
  if (lastSel?.hostId && hostOrder.includes(lastSel.hostId)) return lastSel.hostId
  if (activeHostId && hostOrder.includes(activeHostId)) return activeHostId
  return hostOrder[0] ?? null
}

/**
 * Pure, runtime-independent first pass that picks the tentative hostId we
 * are about to render.  Used to drive a SELECTIVE runtime subscription —
 * only the runtime for this hostId is observed by HostPage, so background
 * host heartbeat ticks do NOT trigger HostPage re-renders.
 *
 * Selection priority:
 *   1. URL hostId (when /hosts/:id/... and :id is in hostOrder)
 *   2. shared fallback (lastSel → activeHostId → hostOrder[0])
 */
function preResolveHostId(
  location: string,
  hostOrder: string[],
  activeHostId: string | null,
  lastSel: Selection | null,
): string | null {
  if (hostOrder.length === 0) return null
  const parsed = parseRoute(location)
  if (
    (parsed?.kind === 'hosts' || parsed?.kind === 'hosts-invalid') &&
    parsed.hostId &&
    hostOrder.includes(parsed.hostId)
  ) {
    return parsed.hostId
  }
  return pickHostIdFallback(hostOrder, activeHostId, lastSel)
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
 * Given a target `hostId` + its `runtime` snapshot and an optional
 * `requestedSubPage`, return the best selectable subPage.  ctx-aware
 * via the runtime field so disabled(ctx) predicates can gate on runtime.
 *
 * 1. If `requestedSubPage` is provided, exists in the registry AND is
 *    selectable for `(hostId, runtime)` → return it as-is.
 * 2. Otherwise return the first contribution that is selectable.
 * 3. Ultimate safety net: return `'overview'` literal if the registry is
 *    empty or every contribution is disabled.
 */
function pickSelectableSubPage(
  hostId: string,
  requestedSubPage: string | null | undefined,
  runtime: HostRuntime | undefined,
): HostSubPage {
  const ctx: SettingsContextFor<'host'> = { scope: 'host', hostId, runtime }
  const contributions = listContributions('host') as SettingsContribution<'host'>[]

  if (requestedSubPage) {
    const c = contributions.find((c) => c.localId === requestedSubPage)
    if (c && isSelectable(c, ctx)) return requestedSubPage as HostSubPage
  }

  const first = contributions.find((c) => isSelectable(c, ctx))
  return (first?.localId ?? 'overview') as HostSubPage
}

/**
 * `tentativeHostId` and `tentativeRuntime` are the result of the pre-resolve
 * pass + selective subscription.  This second pass picks the final hostId and
 * sub-page using the same shared fallback helper for consistency.
 */
function getFallbackSelection(
  hostOrder: string[],
  activeHostId: string | null,
  tentativeRuntime: HostRuntime | undefined,
): Selection | null {
  const hostId = pickHostIdFallback(hostOrder, activeHostId, lastSelection)
  if (!hostId) return null

  return {
    hostId,
    subPage: pickSelectableSubPage(hostId, lastSelection?.subPage, tentativeRuntime),
  }
}

function resolveSelection(
  location: string,
  hostOrder: string[],
  activeHostId: string | null,
  tentativeRuntime: HostRuntime | undefined,
) {
  const parsed = parseRoute(location)
  const isHostRoute = parsed?.kind === 'hosts' || parsed?.kind === 'hosts-invalid'

  if (hostOrder.length === 0) {
    return {
      selection: null,
      canonicalPath: isHostRoute && location !== '/hosts' ? '/hosts' : null as string | null,
      shouldPersistSelection: false,
    }
  }

  const fallbackSelection = getFallbackSelection(hostOrder, activeHostId, tentativeRuntime)
  if (!fallbackSelection) {
    return {
      selection: null,
      canonicalPath: isHostRoute && location !== '/hosts' ? '/hosts' : null as string | null,
      shouldPersistSelection: false,
    }
  }

  // The runtime we use to evaluate sub-page disabled() predicates is the
  // tentative-host runtime — the final selected hostId equals tentativeHostId
  // when the URL/lastSelection is valid; if we fall back to a different host
  // (rare: tentativeHostId not in hostOrder) the fallback ctx will be evaluated
  // against tentativeRuntime, which is acceptable since selection rarely
  // changes mid-resolve.
  const evalRuntime = tentativeRuntime

  if (parsed?.kind === 'hosts') {
    if (parsed.hostId && parsed.subPage) {
      const resolvedHostId = hostOrder.includes(parsed.hostId) ? parsed.hostId : fallbackSelection.hostId
      const resolvedSubPage = pickSelectableSubPage(resolvedHostId, parsed.subPage, evalRuntime)
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
      const subPage = pickSelectableSubPage(hostId, lastSelection.subPage, evalRuntime)
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
      subPage: pickSelectableSubPage(hostId, rawSubPage, evalRuntime),
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
    const subPage = pickSelectableSubPage(hostId, lastSelection.subPage, evalRuntime)
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

  // Pass 1: pre-resolve a tentative hostId without observing runtime.
  const tentativeHostId = preResolveHostId(location, hostOrder, activeHostId, lastSelection)

  // Pass 2: SELECTIVE runtime subscription — only the tentative host's
  // runtime triggers re-renders.  Background host heartbeat ticks mutate
  // `runtime[otherId]` but the selector returns the same `runtime[tentativeHostId]`
  // reference, so zustand's shallow compare skips a re-render.
  const tentativeRuntime = useHostStore((s) =>
    tentativeHostId ? s.runtime[tentativeHostId] : undefined,
  )

  // Pass 3: full resolve with runtime-aware ctx for disabled() predicates.
  // tentativeHostId already drove the selective subscription above; resolveSelection
  // re-derives the final hostId from URL/order/active and uses tentativeRuntime
  // for ctx-aware sub-page selection.
  const { selection, canonicalPath, shouldPersistSelection } = resolveSelection(
    location, hostOrder, activeHostId, tentativeRuntime,
  )

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
    const ctx: SettingsContextFor<'host'> = { scope: 'host', hostId, runtime: tentativeRuntime }
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
    ? pickSelectableSubPage(sidebarHostId, selection?.subPage ?? lastSelection?.subPage, tentativeRuntime)
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

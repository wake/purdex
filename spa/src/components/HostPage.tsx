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
 * Builds a sub-page selection for a fallback hostId derived from the
 * render-local snapshot of `lastSelection`.  Both `lastSel` and
 * `tentativeRuntime` come from the caller so this function never reads
 * the module-scoped `lastSelection` directly — that's the R2 attacker A2
 * fix: a single render must use one consistent snapshot to avoid
 * cross-instance contamination from concurrent HostPage renders.
 */
function getFallbackSelection(
  hostOrder: string[],
  activeHostId: string | null,
  lastSel: Selection | null,
  tentativeRuntime: HostRuntime | undefined,
): Selection | null {
  const hostId = pickHostIdFallback(hostOrder, activeHostId, lastSel)
  if (!hostId) return null

  return {
    hostId,
    subPage: pickSelectableSubPage(hostId, lastSel?.subPage, tentativeRuntime),
  }
}

function resolveSelection(
  location: string,
  hostOrder: string[],
  activeHostId: string | null,
  tentativeHostId: string | null,
  tentativeRuntime: HostRuntime | undefined,
  lastSel: Selection | null,
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

  const fallbackSelection = getFallbackSelection(hostOrder, activeHostId, lastSel, tentativeRuntime)
  if (!fallbackSelection) {
    return {
      selection: null,
      canonicalPath: isHostRoute && location !== '/hosts' ? '/hosts' : null as string | null,
      shouldPersistSelection: false,
    }
  }

  // R2 attacker A2 — `tentativeRuntime` is the runtime *of the tentative
  // hostId only*.  When the final resolvedHostId differs from tentativeHostId
  // (e.g. URL hostId not in hostOrder, or fallback paths), evaluating
  // `disabled(ctx)` with the wrong host's runtime would mis-select sub-pages.
  // Be conservative: pass undefined runtime to pickSelectableSubPage in that
  // case, so disabled() predicates that gate on runtime fall through to the
  // "no info yet" branch instead of using stale data from another host.
  const evalRuntimeFor = (hostId: string): HostRuntime | undefined =>
    hostId === tentativeHostId ? tentativeRuntime : undefined

  if (parsed?.kind === 'hosts') {
    if (parsed.hostId && parsed.subPage) {
      const resolvedHostId = hostOrder.includes(parsed.hostId) ? parsed.hostId : fallbackSelection.hostId
      const resolvedSubPage = pickSelectableSubPage(resolvedHostId, parsed.subPage, evalRuntimeFor(resolvedHostId))
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

    if (lastSel) {
      const hostId = hostOrder.includes(lastSel.hostId) ? lastSel.hostId : fallbackSelection.hostId
      const subPage = pickSelectableSubPage(hostId, lastSel.subPage, evalRuntimeFor(hostId))
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
      subPage: pickSelectableSubPage(hostId, rawSubPage, evalRuntimeFor(hostId)),
    }

    return {
      selection,
      canonicalPath: buildHostPath(selection),
      shouldPersistSelection: true,
    }
  }

  // Non-host route: keep lastSel if available, clamped to live registry.
  if (lastSel) {
    const hostId = hostOrder.includes(lastSel.hostId) ? lastSel.hostId : fallbackSelection.hostId
    const subPage = pickSelectableSubPage(hostId, lastSel.subPage, evalRuntimeFor(hostId))
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


export function HostPage({ isActive }: PaneRendererProps) {
  const [location, setLocation] = useLocation()
  const hostOrder = useHostStore((s) => s.hostOrder)
  const activeHostId = useHostStore((s) => s.activeHostId)
  const [showAddHost, setShowAddHost] = useState(false)
  const t = useI18nStore((s) => s.t)

  // R2 attacker A2 fix — snapshot module-scoped lastSelection ONCE per render
  // and pass it to every helper.  preResolveHostId, resolveSelection, and
  // getFallbackSelection all consume this snapshot, so a render uses one
  // consistent view even if a concurrent HostPage instance's effect mutates
  // the module-scoped value mid-flight.
  const lastSelSnapshot = lastSelection

  // Pass 1: pre-resolve a tentative hostId without observing runtime.
  const tentativeHostId = preResolveHostId(location, hostOrder, activeHostId, lastSelSnapshot)

  // Pass 2: SELECTIVE runtime subscription — only the tentative host's
  // runtime triggers re-renders.  R1 standard P2 fix — `isActive` gates
  // the subscription so hidden HostPage instances (TabContent keeps
  // non-active panes mounted with `visibility: hidden`) do not re-render
  // on background-host heartbeat ticks.  Selector returns undefined when
  // inactive; resolveSelection then evaluates with no runtime info, which
  // is safe because the inactive page is not user-visible anyway.
  const tentativeRuntime = useHostStore((s) =>
    isActive && tentativeHostId ? s.runtime[tentativeHostId] : undefined,
  )

  // Pass 3: full resolve with runtime-aware ctx for disabled() predicates.
  // resolveSelection only assigns runtime to a host's ctx when the resolved
  // hostId equals tentativeHostId, otherwise it passes undefined (R2 A2).
  const { selection, canonicalPath, shouldPersistSelection } = resolveSelection(
    location, hostOrder, activeHostId, tentativeHostId, tentativeRuntime, lastSelSnapshot,
  )

  useEffect(() => {
    if (shouldPersistSelection && selection) lastSelection = selection
  }, [selection, shouldPersistSelection])

  useEffect(() => {
    // R1 standard P2 fix — only the active HostPage may push canonical-path
    // redirects.  Hidden instances must not race the active one to
    // setLocation, which would otherwise navigate the user mid-interaction.
    if (!isActive) return
    if (canonicalPath && canonicalPath !== location) {
      setLocation(canonicalPath, { replace: true })
    }
  }, [isActive, canonicalPath, location, setLocation])

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

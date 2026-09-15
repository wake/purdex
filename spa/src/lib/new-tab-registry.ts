import type { PaneContent } from '../types/tab'

export interface NewTabProviderProps {
  onSelect: (content: PaneContent) => void
}

export interface NewTabProvider {
  id: string
  label: string // i18n key
  /** Optional interpolation params for `label` (e.g. `{ host: 'mlab' }`). */
  labelParams?: Record<string, string>
  icon: string
  order: number
  component: React.ComponentType<NewTabProviderProps>
  disabled?: boolean
  disabledReason?: string // i18n key
  /**
   * Optional — ties a provider to a module registered in `useModuleEnabledStore`.
   * `NewTabPage` filters out providers whose owning module is disabled; legacy
   * providers with no `moduleId` are always visible (spec §4.9.3).
   */
  moduleId?: string
}

/**
 * A dynamic set of providers derived from live state (e.g. one sessions block
 * per host). `getProviders()` is read on every `getNewTabProviders()` call;
 * `subscribe` notifies when that set may have changed. `ownsId` lets the
 * layout bootstrap prune ids this source used to produce but no longer does
 * (removed host, legacy id) — ids nobody owns are never pruned.
 */
export interface NewTabProviderSource {
  id: string
  getProviders: () => NewTabProvider[]
  subscribe: (listener: () => void) => () => void
  ownsId: (providerId: string) => boolean
  /**
   * Whether `getProviders()` reflects real (hydrated) state. While false, the
   * layout bootstrap neither places this source's providers nor prunes ids it
   * owns — a pre-hydration host list must not erase persisted placements.
   * Omitted = always ready.
   */
  isReady?: () => boolean
}

const providers = new Map<string, NewTabProvider>()
const sources = new Map<string, NewTabProviderSource>()

const isSourceReady = (s: NewTabProviderSource) => s.isReady?.() ?? true

function snapshot(readyOnly = false): NewTabProvider[] {
  const all = [...providers.values()]
  for (const source of sources.values()) {
    if (readyOnly && !isSourceReady(source)) continue
    all.push(...source.getProviders())
  }
  // Array.prototype.sort is stable, so equal orders keep source order.
  return all.sort((a, b) => a.order - b.order)
}

export function registerNewTabProvider(provider: NewTabProvider): void {
  // Map-by-id storage means a re-register with the same id replaces the
  // previous entry rather than duplicating it. HMR / bootstrap can call this
  // repeatedly without leaking stale providers.
  providers.set(provider.id, provider)
}

/** Register a dynamic provider source. Same-id re-registration replaces. */
export function registerNewTabProviderSource(source: NewTabProviderSource): void {
  sources.set(source.id, source)
}

/**
 * Drop every provider tagged with `moduleId === ownerModuleId`. Used by
 * `registerEditorNewTabProviders()` (and any future module-owned helper) to
 * stay idempotent across HMR / re-bootstrap.
 */
export function unregisterNewTabProvidersByModule(ownerModuleId: string): void {
  for (const [id, p] of providers) {
    if (p.moduleId === ownerModuleId) providers.delete(id)
  }
}

export function getNewTabProviders(): NewTabProvider[] {
  return snapshot()
}

/** Static providers plus those of sources whose state is ready (hydrated). */
export function getReadyNewTabProviders(): NewTabProvider[] {
  return snapshot(true)
}

/** Subscribe to changes of any registered source. Returns an unsubscribe. */
export function subscribeNewTabProviders(listener: () => void): () => void {
  const unsubs = [...sources.values()].map((s) => s.subscribe(listener))
  return () => unsubs.forEach((u) => u())
}

/**
 * Ids owned by a registered source that its current providers no longer
 * include. An id owned by any not-yet-ready source is never reported stale.
 */
export function getStaleNewTabProviderIds(ids: string[]): string[] {
  const live = new Set(snapshot().map((p) => p.id))
  const all = [...sources.values()]
  return ids.filter((id) => {
    if (live.has(id)) return false
    const owners = all.filter((s) => s.ownsId(id))
    return owners.length > 0 && owners.every(isSourceReady)
  })
}

export function clearNewTabRegistry(): void {
  providers.clear()
  sources.clear()
}

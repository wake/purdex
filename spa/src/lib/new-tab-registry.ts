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
  /**
   * Ids this source owns that are never stale, whether or not it currently
   * produces them — a host-bearing column (`sessions:<id>` / `headless:<id>`)
   * naming a host this device does not have is kept verbatim, so it comes back
   * when the host is added here and a device lacking a host never prunes it
   * for every device (host ownership spec §3.2). Omitted = none retained.
   */
  retainsId?: (providerId: string) => boolean
  /**
   * Retired ids this source replaces (e.g. legacy `sessions` → every
   * `sessions:<hostId>`). Applied by the bootstrap before stale pruning, and
   * only while the source is ready so `to` reflects the real state.
   */
  migrations?: () => NewTabProviderMigration[]
  /**
   * Optional — the module that owns this source. `unregisterNewTabProvidersByModule`
   * removes matching sources too (and `subscribeNewTabProviders` releases the
   * subscription it holds on them). Sources without one (e.g. sessions) are
   * never module-owned and only go away via `unregisterNewTabProviderSource`.
   */
  moduleId?: string
}

export interface NewTabProviderMigration {
  from: string
  to: string[]
}

const providers = new Map<string, NewTabProvider>()
const sources = new Map<string, NewTabProviderSource>()
/** Notified when the registry itself changes (provider/source register, replace, clear). */
const registryListeners = new Set<() => void>()

function notifyRegistryChange(): void {
  // Copy: a listener may (un)subscribe while we iterate.
  for (const l of [...registryListeners]) l()
}

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
  notifyRegistryChange()
}

/** Register a dynamic provider source. Same-id re-registration replaces. */
export function registerNewTabProviderSource(source: NewTabProviderSource): void {
  sources.set(source.id, source)
  notifyRegistryChange()
}

/** Remove one dynamic source by id. No-op (no notification) for unknown ids. */
export function unregisterNewTabProviderSource(id: string): void {
  if (sources.delete(id)) notifyRegistryChange()
}

/**
 * Drop every provider AND every source tagged with `moduleId === ownerModuleId`.
 * Used by `registerEditorNewTabProviders()` (and any future module-owned
 * helper) to stay idempotent across HMR / re-bootstrap, and by a module's
 * disable path. Subscribers re-sync on the registry notification, which
 * releases the per-source subscription each of them holds on a removed source.
 */
export function unregisterNewTabProvidersByModule(ownerModuleId: string): void {
  let changed = false
  for (const [id, p] of providers) {
    if (p.moduleId === ownerModuleId) {
      providers.delete(id)
      changed = true
    }
  }
  for (const [id, s] of sources) {
    if (s.moduleId === ownerModuleId) {
      sources.delete(id)
      changed = true
    }
  }
  if (changed) notifyRegistryChange()
}

export function getNewTabProviders(): NewTabProvider[] {
  return snapshot()
}

/** Static providers plus those of sources whose state is ready (hydrated). */
export function getReadyNewTabProviders(): NewTabProvider[] {
  return snapshot(true)
}

/** Id migrations declared by ready sources (unready sources are skipped). */
export function getNewTabProviderMigrations(): NewTabProviderMigration[] {
  return [...sources.values()]
    .filter(isSourceReady)
    .flatMap((s) => s.migrations?.() ?? [])
}

/**
 * Subscribe to any change in the provider set: registry changes (providers or
 * sources registered, replaced, cleared) and each registered source's own
 * emitter. One stable subscription — per-source subscriptions are re-synced
 * whenever the source set changes, so sources added later are followed and
 * replaced/cleared ones are released. Returns an unsubscribe.
 */
export function subscribeNewTabProviders(listener: () => void): () => void {
  const attached = new Map<NewTabProviderSource, () => void>()
  const syncSources = () => {
    for (const [src, unsub] of attached) {
      if (sources.get(src.id) !== src) {
        unsub()
        attached.delete(src)
      }
    }
    for (const src of sources.values()) {
      if (!attached.has(src)) attached.set(src, src.subscribe(listener))
    }
  }
  const onRegistryChange = () => {
    syncSources()
    listener()
  }
  syncSources()
  registryListeners.add(onRegistryChange)
  return () => {
    registryListeners.delete(onRegistryChange)
    for (const unsub of attached.values()) unsub()
    attached.clear()
  }
}

/**
 * Ids owned by a registered source that its current providers no longer
 * include. An id owned by any not-yet-ready source, or retained by any owner
 * (`retainsId`), is never reported stale.
 */
export function getStaleNewTabProviderIds(ids: string[]): string[] {
  const live = new Set(snapshot().map((p) => p.id))
  const all = [...sources.values()]
  return ids.filter((id) => {
    if (live.has(id)) return false
    const owners = all.filter((s) => s.ownsId(id))
    if (owners.some((s) => s.retainsId?.(id) === true)) return false
    return owners.length > 0 && owners.every(isSourceReady)
  })
}

export function clearNewTabRegistry(): void {
  providers.clear()
  sources.clear()
  notifyRegistryChange()
}

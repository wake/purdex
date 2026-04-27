import type { PaneContent } from '../types/tab'

export interface NewTabProviderProps {
  onSelect: (content: PaneContent) => void
}

export interface NewTabProvider {
  id: string
  label: string
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

const providers = new Map<string, NewTabProvider>()

function snapshot(): NewTabProvider[] {
  return [...providers.values()].sort((a, b) => a.order - b.order)
}

export function registerNewTabProvider(provider: NewTabProvider): void {
  // Map-by-id storage means a re-register with the same id replaces the
  // previous entry rather than duplicating it. HMR / bootstrap can call this
  // repeatedly without leaking stale providers.
  providers.set(provider.id, provider)
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

export function clearNewTabRegistry(): void {
  providers.clear()
}

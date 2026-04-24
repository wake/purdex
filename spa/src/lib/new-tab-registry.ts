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

const providers: NewTabProvider[] = []

export function registerNewTabProvider(provider: NewTabProvider): void {
  providers.push(provider)
  providers.sort((a, b) => a.order - b.order)
}

export function getNewTabProviders(): NewTabProvider[] {
  return [...providers]
}

export function clearNewTabRegistry(): void {
  providers.length = 0
}

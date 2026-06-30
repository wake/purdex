import { useMemo, useState, useEffect } from 'react'
import { getNewTabProviders } from '../lib/new-tab-registry'
import { useI18nStore } from '../stores/useI18nStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { useBreakpoint } from '../hooks/useBreakpoint'
import { resolveProfile } from '../lib/resolve-profile'
import { colsClass } from '../lib/cols-class'
import type { PaneContent } from '../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function NewTabPage({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  const [hydrated, setHydrated] = useState(useNewTabLayoutStore.persist.hasHydrated())
  useEffect(() => {
    if (hydrated) return
    return useNewTabLayoutStore.persist.onFinishHydration(() => setHydrated(true))
  }, [hydrated])

  const { isWide, isMid } = useBreakpoint()
  const profiles = useNewTabLayoutStore((s) => s.profiles)
  const profileKey = resolveProfile(isWide, isMid, profiles)
  const profile = profiles[profileKey]

  // P1 reload-required contract (matches PaneLayoutRenderer + file-opener
  // registry): module enable/disable does NOT flip the New Tab UI live —
  // a fresh registerBuiltinModules() bootstrap is the canonical way to make
  // the change visible. Reading isEnabled via getState() avoids a subscription
  // that would otherwise hide editor entries the moment the user toggles
  // Editor in the Switchboard while the actual openers / terminal-link
  // bindings still wait for the next bootstrap. Going fully-immediate is
  // tracked in issue #678.
  const providers = useMemo(() => {
    // A2-4 / A2-5: providers carrying a `moduleId` are hidden when the owning
    // module is disabled. Legacy providers with no `moduleId` are always
    // visible (back-compat, spec §4.9.3).
    const isEnabled = useModuleEnabledStore.getState().isEnabled
    return getNewTabProviders().filter((p) => !p.moduleId || isEnabled(p.moduleId))
    // Empty deps: providers / module enable state are deliberately captured
    // at render-creation time only. Subsequent toggles wait for re-bootstrap.
  }, [])
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])

  if (!hydrated) {
    return <div className="h-full w-full" />
  }

  if (providers.length === 0) {
    return (
      <div className="h-full w-full flex items-center justify-center" data-testid="newtab-empty-state">
        <p className="text-sm text-text-secondary">{t('page.newtab.empty')}</p>
      </div>
    )
  }

  // v1.4 §4.9.7 (F8): a user's pinned profile may reference only
  // providers that are currently filtered out (e.g. pinned `editor` +
  // `editor-buffers`, then disabled the Editor module). In that case
  // every column resolves to an empty list — the old code rendered
  // nothing but the grid, leaving the tab visually blank with no cue.
  // Fall back to the existing empty state when NO column has anything
  // visible; keep the grid otherwise.
  const hasAnyVisibleEntry = profile.columns.some((col) => col.some((id) => byId[id]))
  if (!hasAnyVisibleEntry) {
    return (
      <div className="h-full w-full flex items-center justify-center" data-testid="newtab-empty-state">
        <p className="text-sm text-text-secondary">{t('page.newtab.empty')}</p>
      </div>
    )
  }

  const gridCols = colsClass(profile.columns.length)

  return (
    <div className={`h-full w-full grid overflow-hidden gap-6 px-6 pt-8 ${gridCols}`}>
      {profile.columns.map((col, i) => (
        <div key={`${profileKey}-${i}`} className="flex flex-col gap-6 overflow-y-auto">
          {col.map((id) => {
            const p = byId[id]
            if (!p) return null
            return (
              <section key={id} className="w-full">
                <h3 className="text-sm font-medium text-text-secondary mb-2 px-2">
                  {t(p.label)}
                  {p.disabled && p.disabledReason && (
                    <span className="text-text-muted text-xs ml-2">— {t(p.disabledReason)}</span>
                  )}
                </h3>
                {!p.disabled && <p.component onSelect={onSelect} />}
              </section>
            )
          })}
        </div>
      ))}
    </div>
  )
}

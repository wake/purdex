import { useMemo, useState, useEffect } from 'react'
import { getNewTabProviders } from '../lib/new-tab-registry'
import { useI18nStore } from '../stores/useI18nStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useMediaQuery } from '../hooks/useMediaQuery'
import { resolveProfile } from '../lib/resolve-profile'
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

  const isWide = useMediaQuery('(min-width: 1024px)')
  const isMid = useMediaQuery('(min-width: 640px)')
  const profiles = useNewTabLayoutStore((s) => s.profiles)
  const profileKey = resolveProfile(isWide, isMid, profiles)
  const profile = profiles[profileKey]

  const providers = getNewTabProviders()
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])

  if (!hydrated) {
    return <div className="flex-1" />
  }

  if (providers.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-secondary">{t('page.newtab.empty')}</p>
      </div>
    )
  }

  const gridCols = profile.columns.length === 3 ? 'grid-cols-3'
                 : profile.columns.length === 2 ? 'grid-cols-2'
                 : 'grid-cols-1'

  return (
    <div className={`flex-1 grid overflow-hidden gap-6 px-6 pt-8 ${gridCols}`}>
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

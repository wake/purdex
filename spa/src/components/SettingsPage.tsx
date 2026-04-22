import { createContext, useContext, useEffect, useMemo, useState } from 'react'
import { useLocation } from 'wouter'
import type { PaneRendererProps } from '../lib/module-registry'
import { listContributions } from '../lib/settings-contribution-registry'
import type { SettingsContextFor } from '../lib/settings-contribution-types'
import { SettingsSidebar } from './settings/SettingsSidebar'
import { WorkspaceSettingsPage } from '../features/workspace/components/WorkspaceSettingsPage'

// Persists across unmount/remount (keepAliveCount=0 destroys component on tab switch)
let lastSection: string | null = null

/** @internal test-only — must co-locate to access module-scoped variable */
// eslint-disable-next-line react-refresh/only-export-components
export function resetLastSection() { lastSection = null }

interface SettingsRouteCtx {
  subsection: string | null
  setSubsection: (sub: string | null) => void
}

// eslint-disable-next-line react-refresh/only-export-components
export const SettingsRouteContext = createContext<SettingsRouteCtx>({
  subsection: null,
  setSubsection: () => {},
})

// eslint-disable-next-line react-refresh/only-export-components
export function useSettingsRoute() {
  return useContext(SettingsRouteContext)
}

export function SettingsPage(props: PaneRendererProps) {
  const content = props.pane.content
  if (content.kind === 'settings' && typeof content.scope === 'object') {
    return <WorkspaceSettingsPage workspaceId={content.scope.workspaceId} />
  }

  return <GlobalSettingsPage />
}

function GlobalSettingsPage() {
  const [location, setLocation] = useLocation()
  // Pull purdex-scoped contributions from the new registry. `listContributions`
  // returns a fresh array sorted by `order` ascending on each call.
  const sections = listContributions('purdex')

  // `ctx` is stable per page render — §5.3 rule 4 says only the shell is
  // allowed to construct it. Purdex scope carries no entity id.
  const ctx: SettingsContextFor<'purdex'> = useMemo(() => ({ scope: 'purdex' as const }), [])

  // F7: a contribution whose `disabled(ctx)` returns true is treated as
  // invalid for navigation — it stays visible in the sidebar (rendered
  // greyed out) but the shell will never mount it. `isSelectable()` is
  // the single predicate used by (a) the URL self-heal effect and (b)
  // the default-section fallback.
  const isSelectable = (localId: string): boolean => {
    const c = sections.find((s) => s.localId === localId)
    if (!c) return false
    if (c.disabled && c.disabled(ctx) === true) return false
    return true
  }
  const firstSelectable = sections.find((s) => isSelectable(s.localId))?.localId ?? ''

  const pathAfterSettings = location.startsWith('/settings/')
    ? location.slice('/settings/'.length)
    : null
  const parts = pathAfterSettings ? pathAfterSettings.split('/') : []
  const urlSection = parts[0] || null
  const urlSubsection = parts[1] || null

  const [activeSection, setActiveSection] = useState(() => {
    if (urlSection && isSelectable(urlSection)) return urlSection
    if (lastSection && isSelectable(lastSection)) return lastSection
    return firstSelectable
  })

  // URL → activeSection (e.g. back/forward navigation or TitleBar click).
  // F7: only honor URL sections that are actually selectable under ctx.
  useEffect(() => {
    if (urlSection && isSelectable(urlSection) && urlSection !== activeSection) {
      setActiveSection(urlSection)
      lastSection = urlSection
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlSection])

  // Self-heal:
  //   1. invalid OR disabled-by-ctx section → /settings/<activeSection>
  //   2. >2 path segments (e.g. /settings/sync/extra/level3) → /settings/<section>
  // Valid /settings/<section>/<subsection> URLs are preserved and exposed via
  // SettingsRouteContext so sub-components can render subsection views.
  useEffect(() => {
    if (!urlSection) return
    if (!isSelectable(urlSection)) {
      setLocation(`/settings/${activeSection}`, { replace: true })
      return
    }
    if (parts.length > 2) {
      setLocation(`/settings/${urlSection}`, { replace: true })
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlSection, urlSubsection, activeSection])

  // F7: if the current URL is `/settings` (no urlSection) and the
  // initial pick was not selectable (e.g. all contributions disabled
  // except one further down the order), advance the URL to match the
  // mounted section so route-sync round-trips correctly.
  useEffect(() => {
    if (urlSection) return
    if (!activeSection) return
    if (location === `/settings/${activeSection}`) return
    // Only push the canonical URL when we actually have a selectable
    // section to mount — avoids spurious history entries during tests
    // that register zero contributions.
    if (isSelectable(activeSection)) {
      setLocation(`/settings/${activeSection}`, { replace: true })
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlSection, activeSection])

  const handleSelectSection = (id: string) => {
    // Guard: the sidebar already suppresses clicks on disabled rows, but
    // belt-and-braces in case a future caller bypasses that UI path.
    if (!isSelectable(id)) return
    lastSection = id
    setActiveSection(id)
    setLocation(`/settings/${id}`, { replace: true })
  }

  const ActiveComponent = isSelectable(activeSection)
    ? sections.find((s) => s.localId === activeSection)?.component
    : undefined

  return (
    <SettingsRouteContext.Provider
      value={{
        subsection: urlSubsection,
        setSubsection: (sub) =>
          setLocation(
            sub ? `/settings/${activeSection}/${sub}` : `/settings/${activeSection}`,
            { replace: true },
          ),
      }}
    >
      <div className="flex h-full">
        <SettingsSidebar activeSection={activeSection} onSelectSection={handleSelectSection} />
        <div className="flex-1 overflow-y-auto p-6">
          {ActiveComponent && <ActiveComponent ctx={ctx} />}
        </div>
      </div>
    </SettingsRouteContext.Provider>
  )
}

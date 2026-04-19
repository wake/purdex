import { createContext, useContext, useEffect, useState } from 'react'
import { useLocation } from 'wouter'
import type { PaneRendererProps } from '../lib/module-registry'
import { getSettingsSections } from '../lib/settings-section-registry'
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
  const sections = getSettingsSections()

  const pathAfterSettings = location.startsWith('/settings/')
    ? location.slice('/settings/'.length)
    : null
  const parts = pathAfterSettings ? pathAfterSettings.split('/') : []
  const urlSection = parts[0] || null
  const urlSubsection = parts[1] || null

  const [activeSection, setActiveSection] = useState(() => {
    if (urlSection && sections.some((s) => s.id === urlSection)) return urlSection
    if (lastSection && sections.some((s) => s.id === lastSection)) return lastSection
    return sections.find((s) => s.component)?.id ?? ''
  })

  // URL → activeSection (e.g. back/forward navigation or TitleBar click)
  useEffect(() => {
    if (urlSection && sections.some((s) => s.id === urlSection) && urlSection !== activeSection) {
      setActiveSection(urlSection)
      lastSection = urlSection
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlSection])

  // Self-heal:
  //   1. invalid section → /settings/<activeSection>
  //   2. >2 path segments (e.g. /settings/sync/extra/level3) → /settings/<section>
  // Valid /settings/<section>/<subsection> URLs are preserved and exposed via
  // SettingsRouteContext so sub-components can render subsection views.
  useEffect(() => {
    if (!urlSection) return
    const sectionValid = sections.some((s) => s.id === urlSection)
    if (!sectionValid) {
      setLocation(`/settings/${activeSection}`, { replace: true })
      return
    }
    if (parts.length > 2) {
      setLocation(`/settings/${urlSection}`, { replace: true })
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlSection, urlSubsection, activeSection])

  const handleSelectSection = (id: string) => {
    lastSection = id
    setActiveSection(id)
    setLocation(`/settings/${id}`, { replace: true })
  }

  const ActiveComponent = sections.find((s) => s.id === activeSection)?.component

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
          {ActiveComponent && <ActiveComponent />}
        </div>
      </div>
    </SettingsRouteContext.Provider>
  )
}

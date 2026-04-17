import { useEffect, useState } from 'react'
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

  const urlSection = location.startsWith('/settings/')
    ? location.slice('/settings/'.length)
    : null

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

  const handleSelectSection = (id: string) => {
    lastSection = id
    setActiveSection(id)
    setLocation(`/settings/${id}`, { replace: true })
  }

  const ActiveComponent = sections.find((s) => s.id === activeSection)?.component

  return (
    <div className="flex h-full">
      <SettingsSidebar activeSection={activeSection} onSelectSection={handleSelectSection} />
      <div className="flex-1 overflow-y-auto p-6">
        {ActiveComponent && <ActiveComponent />}
      </div>
    </div>
  )
}

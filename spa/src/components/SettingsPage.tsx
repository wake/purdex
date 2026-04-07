import { useState } from 'react'
import type { PaneRendererProps } from '../lib/pane-registry'
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
  const sections = getSettingsSections()
  const [activeSection, setActiveSection] = useState(
    () => {
      if (lastSection && sections.some((s) => s.id === lastSection)) return lastSection
      return sections.find((s) => s.component)?.id ?? ''
    },
  )

  const handleSelectSection = (id: string) => {
    lastSection = id
    setActiveSection(id)
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

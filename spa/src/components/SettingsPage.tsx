import { useState } from 'react'
import type { PaneRendererProps } from '../lib/pane-registry'
import { getSettingsSections } from '../lib/settings-section-registry'
import { SettingsSidebar } from './settings/SettingsSidebar'

// Persists across unmount/remount (keepAliveCount=0 destroys component on tab switch)
let lastSection: string | null = null

/** @internal test-only — must co-locate to access module-scoped variable */
// eslint-disable-next-line react-refresh/only-export-components
export function resetLastSection() { lastSection = null }

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function SettingsPage(_props: PaneRendererProps) {
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

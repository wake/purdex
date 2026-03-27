import { useState } from 'react'
import type { PaneRendererProps } from '../lib/pane-registry'
import { getSettingsSections } from '../lib/settings-section-registry'
import { SettingsSidebar } from './settings/SettingsSidebar'

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function SettingsPage(_props: PaneRendererProps) {
  const sections = getSettingsSections()
  const [activeSection, setActiveSection] = useState(
    () => sections.find((s) => s.component)?.id ?? '',
  )

  const ActiveComponent = sections.find((s) => s.id === activeSection)?.component

  return (
    <div className="flex h-full">
      <SettingsSidebar activeSection={activeSection} onSelectSection={setActiveSection} />
      <div className="flex-1 overflow-y-auto p-6">
        {ActiveComponent && <ActiveComponent />}
      </div>
    </div>
  )
}

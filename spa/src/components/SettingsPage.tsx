import { useLocation } from 'wouter'
import type { PaneRendererProps } from '../lib/pane-registry'
import { getSettingsSections } from '../lib/settings-section-registry'
import { SettingsSidebar } from './settings/SettingsSidebar'

function parseSectionFromPath(path: string): string {
  const segment = path.replace(/^\/settings\/?/, '').split('/')[0]
  const validIds = getSettingsSections().filter((s) => s.component).map((s) => s.id)
  if (validIds.includes(segment)) return segment
  return validIds[0] ?? ''
}

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function SettingsPage(_props: PaneRendererProps) {
  const [location, setLocation] = useLocation()
  const activeSection = parseSectionFromPath(location)
  const sections = getSettingsSections()

  const handleSelectSection = (section: string) => {
    setLocation(`/settings/${section}`)
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

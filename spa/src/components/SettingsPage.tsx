import { useLocation } from 'wouter'
import type { PaneRendererProps } from '../lib/pane-registry'
import { SettingsSidebar, type SettingsSection } from './settings/SettingsSidebar'
import { AppearanceSection } from './settings/AppearanceSection'
import { TerminalSection } from './settings/TerminalSection'

const VALID_SECTIONS: SettingsSection[] = ['appearance', 'terminal']

function parseSectionFromPath(path: string): SettingsSection {
  const segment = path.replace(/^\/settings\/?/, '').split('/')[0]
  if (VALID_SECTIONS.includes(segment as SettingsSection)) return segment as SettingsSection
  return 'appearance'
}

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function SettingsPage(_props: PaneRendererProps) {
  const [location, setLocation] = useLocation()
  const activeSection = parseSectionFromPath(location)

  const handleSelectSection = (section: SettingsSection) => {
    setLocation(`/settings/${section}`)
  }

  return (
    <div className="flex h-full">
      <SettingsSidebar activeSection={activeSection} onSelectSection={handleSelectSection} />
      <div className="flex-1 overflow-y-auto p-6">
        {activeSection === 'appearance' && <AppearanceSection />}
        {activeSection === 'terminal' && <TerminalSection />}
      </div>
    </div>
  )
}

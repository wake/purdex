export type SettingsSection = 'appearance' | 'terminal'

interface SidebarItem {
  id: string
  label: string
  enabled: boolean
}

const SECTIONS: SidebarItem[] = [
  { id: 'appearance', label: 'Appearance', enabled: true },
  { id: 'terminal', label: 'Terminal', enabled: true },
  { id: 'workspace', label: 'Workspace', enabled: false },
  { id: 'sync', label: 'Sync', enabled: false },
]

interface Props {
  activeSection: string
  onSelectSection: (section: SettingsSection) => void
}

export function SettingsSidebar({ activeSection, onSelectSection }: Props) {
  const reservedStart = SECTIONS.findIndex((s) => !s.enabled)

  return (
    <div className="w-48 border-r border-gray-800 bg-[#0a0a1a] py-3 flex-shrink-0">
      <div className="px-4 mb-2 text-[10px] text-gray-600 uppercase tracking-wider">Settings</div>
      {SECTIONS.map((item, i) => {
        const isActive = item.id === activeSection
        const showDivider = i === reservedStart && reservedStart > 0

        return (
          <div key={item.id}>
            {showDivider && <div className="mx-3 my-2 border-t border-gray-800" />}
            <button
              data-section={item.id}
              data-active={isActive ? 'true' : undefined}
              onClick={() => {
                if (item.enabled) onSelectSection(item.id as SettingsSection)
              }}
              className={`w-full text-left px-4 py-2 text-sm flex items-center transition-colors ${
                !item.enabled
                  ? 'text-gray-600 cursor-not-allowed'
                  : isActive
                    ? 'bg-[#1e1e3e] text-gray-200 border-l-2 border-[#7a6aaa]'
                    : 'text-gray-400 cursor-pointer hover:bg-white/5'
              }`}
            >
              <span>{item.label}</span>
              {!item.enabled && (
                <span className="text-[10px] text-gray-600 ml-auto">coming soon</span>
              )}
            </button>
          </div>
        )
      })}
    </div>
  )
}

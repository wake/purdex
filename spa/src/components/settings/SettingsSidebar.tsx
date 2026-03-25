import { getSettingsSections } from '../../lib/settings-section-registry'

interface Props {
  activeSection: string
  onSelectSection: (section: string) => void
}

export function SettingsSidebar({ activeSection, onSelectSection }: Props) {
  const sections = getSettingsSections()
  const reservedStart = sections.findIndex((s) => !s.component)

  return (
    <div className="w-48 border-r border-border-subtle bg-surface-primary py-3 flex-shrink-0">
      <div className="px-4 mb-2 text-[10px] text-text-muted uppercase tracking-wider">Settings</div>
      {sections.map((item, i) => {
        const isActive = item.id === activeSection
        const enabled = !!item.component
        const showDivider = i === reservedStart && reservedStart > 0

        return (
          <div key={item.id}>
            {showDivider && <div className="mx-3 my-2 border-t border-border-subtle" />}
            <button
              data-section={item.id}
              data-active={isActive ? 'true' : undefined}
              onClick={() => {
                if (enabled) onSelectSection(item.id)
              }}
              className={`w-full text-left px-4 py-2 text-sm flex items-center transition-colors ${
                !enabled
                  ? 'text-text-muted cursor-not-allowed'
                  : isActive
                    ? 'bg-surface-elevated text-text-primary border-l-2 border-border-active'
                    : 'text-text-secondary cursor-pointer hover:bg-white/5'
              }`}
            >
              <span>{item.label}</span>
              {!enabled && (
                <span className="text-[10px] text-text-muted ml-auto">coming soon</span>
              )}
            </button>
          </div>
        )
      })}
    </div>
  )
}

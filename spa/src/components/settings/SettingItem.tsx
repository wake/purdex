interface SettingItemProps {
  label: string
  description?: string
  disabled?: boolean
  /** Title and description on top, children below at full width (for long, wrapping content). */
  stacked?: boolean
  children: React.ReactNode
}

export function SettingItem({ label, description, disabled, stacked, children }: SettingItemProps) {
  return (
    <div
      className={`flex ${stacked ? 'flex-col' : 'items-start justify-between'} py-3 ${disabled ? 'pointer-events-none' : ''}`}
      {...(stacked ? { 'data-layout': 'stacked' } : {})}
    >
      <div className={`flex flex-col gap-0.5 ${stacked ? '' : 'mr-4'}`}>
        <span className="text-sm text-text-primary">{label}</span>
        {description && <span className="text-xs text-text-secondary">{description}</span>}
      </div>
      <div className={`${stacked ? 'mt-2 w-full min-w-0' : 'flex-shrink-0'} ${disabled ? 'opacity-50' : ''}`} {...(disabled ? { inert: true } : {})}>{children}</div>
    </div>
  )
}

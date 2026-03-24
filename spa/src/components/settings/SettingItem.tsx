interface SettingItemProps {
  label: string
  description?: string
  disabled?: boolean
  children: React.ReactNode
}

export function SettingItem({ label, description, disabled, children }: SettingItemProps) {
  return (
    <div className={`flex items-center justify-between py-3 ${disabled ? 'pointer-events-none' : ''}`}>
      <div className="flex flex-col gap-0.5 mr-4">
        <span className="text-sm text-gray-300">{label}</span>
        {description && <span className="text-xs text-gray-400">{description}</span>}
      </div>
      <div className={`flex-shrink-0 ${disabled ? 'opacity-50' : ''}`} {...(disabled ? { inert: '' } : {})}>{children}</div>
    </div>
  )
}

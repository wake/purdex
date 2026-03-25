import { SettingItem } from './SettingItem'

export function AppearanceSection() {
  return (
    <div>
      <h2 className="text-lg text-text-primary">Appearance</h2>
      <p className="text-xs text-text-secondary mb-6">Visual preferences for the application</p>

      <SettingItem label="Theme" description="Application color scheme" disabled>
        <div className="flex gap-2">
          <button className="px-4 py-1.5 rounded-md border text-xs bg-surface-elevated border-border-active text-text-primary">
            Dark
          </button>
          <button className="px-4 py-1.5 rounded-md border text-xs bg-transparent border-border-default text-text-muted">
            Light
          </button>
        </div>
      </SettingItem>

      <SettingItem label="Language" description="Interface language" disabled>
        <select
          disabled
          className="bg-surface-input border border-border-default rounded-md text-text-secondary text-xs px-3 py-1.5 w-40"
          defaultValue="zh-TW"
        >
          <option value="zh-TW">繁體中文</option>
          <option value="en">English</option>
        </select>
      </SettingItem>
    </div>
  )
}

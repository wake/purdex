import { SettingItem } from './SettingItem'

export function AppearanceSection() {
  return (
    <div>
      <h2 className="text-lg text-gray-200">Appearance</h2>
      <p className="text-xs text-gray-400 mb-6">Visual preferences for the application</p>

      <SettingItem label="Theme" description="Application color scheme" disabled>
        <div className="flex gap-2">
          <button className="px-4 py-1.5 rounded-md border text-xs bg-[#1e1e3e] border-[#7a6aaa] text-gray-200">
            Dark
          </button>
          <button className="px-4 py-1.5 rounded-md border text-xs bg-transparent border-[#404040] text-gray-500">
            Light
          </button>
        </div>
      </SettingItem>

      <SettingItem label="Language" description="Interface language" disabled>
        <select
          disabled
          className="bg-[#2a2a2a] border border-[#404040] rounded-md text-gray-400 text-xs px-3 py-1.5 w-40"
          defaultValue="zh-TW"
        >
          <option value="zh-TW">繁體中文</option>
          <option value="en">English</option>
        </select>
      </SettingItem>
    </div>
  )
}

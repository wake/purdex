import { useUISettingsStore, type TerminalRenderer } from '../../stores/useUISettingsStore'
import { SettingItem } from './SettingItem'

const RENDERER_LABELS: Record<TerminalRenderer, string> = { webgl: 'WebGL', dom: 'DOM' }

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value))
}

export function TerminalSection() {
  const renderer = useUISettingsStore((s) => s.terminalRenderer)
  const setRenderer = useUISettingsStore((s) => s.setTerminalRenderer)
  const bumpVersion = useUISettingsStore((s) => s.bumpTerminalSettingsVersion)

  const keepAliveCount = useUISettingsStore((s) => s.keepAliveCount)
  const setKeepAliveCount = useUISettingsStore((s) => s.setKeepAliveCount)
  const keepAlivePinned = useUISettingsStore((s) => s.keepAlivePinned)
  const setKeepAlivePinned = useUISettingsStore((s) => s.setKeepAlivePinned)

  const revealDelay = useUISettingsStore((s) => s.terminalRevealDelay)
  const setRevealDelay = useUISettingsStore((s) => s.setTerminalRevealDelay)

  const handleRenderer = (r: TerminalRenderer) => {
    setRenderer(r)
    bumpVersion()
  }

  const renderers: TerminalRenderer[] = ['webgl', 'dom']

  return (
    <div>
      <h2 className="text-lg text-gray-200">Terminal</h2>
      <p className="text-xs text-gray-500 mb-6">Terminal rendering and connection settings</p>

      <SettingItem label="Renderer" description="WebGL is faster but limited to ~16 instances. DOM has no limit.">
        <div className="flex">
          {renderers.map((r) => (
            <button
              key={r}
              onClick={() => handleRenderer(r)}
              className={`px-4 py-1.5 text-xs border transition-colors cursor-pointer ${
                r === renderer
                  ? 'bg-[#1e1e3e] border-[#7a6aaa] text-gray-200'
                  : 'bg-transparent border-[#404040] text-gray-500 hover:text-gray-300 hover:border-gray-600'
              } ${r === renderers[0] ? 'rounded-l-md' : ''} ${r === renderers[renderers.length - 1] ? 'rounded-r-md' : ''}`}
            >
              {RENDERER_LABELS[r]}
            </button>
          ))}
        </div>
      </SettingItem>

      <SettingItem label="Keep-alive Count" description="Number of background tabs to keep connected (0 = active only)">
        <input
          type="number"
          aria-label="Keep-alive Count"
          min={0}
          max={10}
          step={1}
          value={keepAliveCount}
          onChange={(e) => setKeepAliveCount(clamp(Number(e.target.value) || 0, 0, 10))}
          className="bg-[#2a2a2a] border border-[#404040] rounded-md text-gray-200 text-xs px-3 py-1.5 w-20 hover:border-gray-500 focus:border-[#7a6aaa] focus:outline-none"
        />
      </SettingItem>

      <SettingItem label="Keep-alive Pinned" description="Always keep pinned tabs connected">
        <button
          role="switch"
          aria-label="Keep-alive Pinned"
          aria-checked={keepAlivePinned}
          onClick={() => setKeepAlivePinned(!keepAlivePinned)}
          className={`w-9 h-5 rounded-full relative transition-all duration-150 cursor-pointer ${
            keepAlivePinned ? 'bg-[#7a6aaa]' : 'bg-gray-700'
          }`}
        >
          <span
            className={`absolute top-0.5 w-4 h-4 rounded-full transition-all duration-150 ${
              keepAlivePinned ? 'left-[18px] bg-white' : 'left-0.5 bg-gray-400'
            }`}
          />
        </button>
      </SettingItem>

      <SettingItem label="Reveal Delay" description="Delay before showing terminal content after connection (ms)">
        <input
          type="number"
          aria-label="Reveal Delay"
          min={0}
          max={2000}
          step={50}
          value={revealDelay}
          onChange={(e) => setRevealDelay(clamp(Number(e.target.value) || 0, 0, 2000))}
          className="bg-[#2a2a2a] border border-[#404040] rounded-md text-gray-200 text-xs px-3 py-1.5 w-20 hover:border-gray-500 focus:border-[#7a6aaa] focus:outline-none"
        />
      </SettingItem>
    </div>
  )
}

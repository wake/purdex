import { useUISettingsStore, type TerminalRenderer } from '../../stores/useUISettingsStore'
import { SettingItem } from './SettingItem'
import { SegmentControl } from './SegmentControl'
import { ToggleSwitch } from './ToggleSwitch'

const RENDERER_OPTIONS = [
  { value: 'webgl' as TerminalRenderer, label: 'WebGL' },
  { value: 'dom' as TerminalRenderer, label: 'DOM' },
]

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value))
}

export function TerminalSection() {
  const renderer = useUISettingsStore((s) => s.terminalRenderer)

  const keepAliveCount = useUISettingsStore((s) => s.keepAliveCount)
  const setKeepAliveCount = useUISettingsStore((s) => s.setKeepAliveCount)
  const keepAlivePinned = useUISettingsStore((s) => s.keepAlivePinned)
  const setKeepAlivePinned = useUISettingsStore((s) => s.setKeepAlivePinned)

  const revealDelay = useUISettingsStore((s) => s.terminalRevealDelay)
  const setRevealDelay = useUISettingsStore((s) => s.setTerminalRevealDelay)

  const handleRenderer = (r: TerminalRenderer) => {
    // Atomic: update renderer + bump version in single set() to avoid intermediate state
    useUISettingsStore.setState((s) => ({
      terminalRenderer: r,
      terminalSettingsVersion: s.terminalSettingsVersion + 1,
    }))
  }

  return (
    <div>
      <h2 className="text-lg text-text-primary">Terminal</h2>
      <p className="text-xs text-text-secondary mb-6">Terminal rendering and connection settings</p>

      <SettingItem label="Renderer" description="WebGL is faster but limited to ~16 instances. DOM has no limit.">
        <SegmentControl options={RENDERER_OPTIONS} value={renderer} onChange={handleRenderer} />
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
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-20 hover:border-text-muted focus:border-border-active focus:outline-none"
        />
      </SettingItem>

      <SettingItem label="Keep-alive Pinned" description="Always keep pinned tabs connected">
        <ToggleSwitch label="Keep-alive Pinned" checked={keepAlivePinned} onChange={setKeepAlivePinned} />
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
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-20 hover:border-text-muted focus:border-border-active focus:outline-none"
        />
      </SettingItem>
    </div>
  )
}

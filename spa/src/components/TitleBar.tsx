import { Columns, Rows, GridFour, Square, SidebarSimple, SquareHalfBottom } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import { useLayoutStore } from '../stores/useLayoutStore'
import type { LayoutPattern, SidebarRegion } from '../types/tab'

interface Props {
  title: string
}

const patterns: { pattern: LayoutPattern; icon: typeof Square; label: string }[] = [
  { pattern: 'single', icon: Square, label: 'Single pane' },
  { pattern: 'split-h', icon: Columns, label: 'Split horizontal' },
  { pattern: 'split-v', icon: Rows, label: 'Split vertical' },
  { pattern: 'grid-4', icon: GridFour, label: 'Grid' },
]

const regionToggles: { region: SidebarRegion; icon: typeof SidebarSimple; label: string; mirror?: boolean }[] = [
  { region: 'primary-sidebar', icon: SidebarSimple, label: 'Primary Sidebar' },
  { region: 'primary-panel', icon: SquareHalfBottom, label: 'Primary Panel' },
  { region: 'secondary-panel', icon: SquareHalfBottom, label: 'Secondary Panel', mirror: true },
  { region: 'secondary-sidebar', icon: SidebarSimple, label: 'Secondary Sidebar', mirror: true },
]

export function TitleBar({ title }: Props) {
  const activeTabId = useTabStore((s) => s.activeTabId)
  const regions = useLayoutStore((s) => s.regions)
  const toggleVisibility = useLayoutStore((s) => s.toggleVisibility)

  const handlePattern = (pattern: LayoutPattern) => {
    if (!activeTabId) return
    useTabStore.getState().applyLayout(activeTabId, pattern)
  }

  const visibleToggles = regionToggles

  return (
    <div
      className="shrink-0 relative flex items-center bg-surface-secondary border-b border-border-subtle px-2"
      style={{ height: 30, WebkitAppRegion: 'drag' } as React.CSSProperties}
    >
      {/* Title — absolute positioned for true center across full window width */}
      <div className="absolute inset-0 flex items-center justify-center pointer-events-none select-none">
        <span className="text-xs text-text-muted truncate px-20">{title}</span>
      </div>
      <div className="flex-1" />
      <div
        data-testid="layout-buttons"
        className="shrink-0 flex items-center gap-0.5"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        {/* Region toggles */}
        {visibleToggles.map(({ region, icon: Icon, label, mirror }) => {
          const isVisible = regions[region].mode !== 'hidden'
          return (
            <button
              key={region}
              className={`p-1 rounded transition-colors ${
                isVisible
                  ? 'text-accent-base bg-accent-base/10 hover:bg-accent-base/20'
                  : 'text-text-muted hover:text-text-primary hover:bg-surface-hover'
              }`}
              title={label}
              onClick={() => toggleVisibility(region)}
              style={mirror ? { transform: 'scaleX(-1)' } : undefined}
            >
              <Icon size={14} />
            </button>
          )
        })}
        {/* Separator */}
        <div className="w-px h-3.5 bg-border-subtle mx-0.5" />
        {/* Layout pattern buttons */}
        {patterns.map(({ pattern, icon: Icon, label }) => (
          <button
            key={pattern}
            disabled={!activeTabId}
            className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover disabled:opacity-40 disabled:pointer-events-none"
            title={label}
            onClick={() => handlePattern(pattern)}
          >
            <Icon size={14} />
          </button>
        ))}
      </div>
    </div>
  )
}

import { Columns, Rows, Square } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import type { LayoutPattern } from '../types/tab'
import { CollapseButton } from '../features/workspace/components/CollapseButton'

interface Props { title: string }

const patterns: { pattern: LayoutPattern; icon: typeof Square; label: string }[] = [
  { pattern: 'single', icon: Square, label: 'Single pane' },
  { pattern: 'split-h', icon: Columns, label: 'Split horizontal' },
  { pattern: 'split-v', icon: Rows, label: 'Split vertical' },
]

export function TitleBar({ title }: Props) {
  const activeTabId = useTabStore((s) => s.activeTabId)

  const handlePattern = (pattern: LayoutPattern) => {
    if (!activeTabId) return
    useTabStore.getState().applyLayout(activeTabId, pattern)
  }

  return (
    <div
      className="shrink-0 relative flex items-center bg-surface-secondary border-b border-border-subtle px-2"
      style={{ height: 36, WebkitAppRegion: 'drag' } as React.CSSProperties}
    >
      {/* macOS traffic-light reserve (titleBarStyle='hiddenInset' draws them at x=12, y=12). */}
      <div className="w-[72px] shrink-0" aria-hidden="true" />
      <div
        data-testid="sidebar-toggle"
        className="shrink-0 flex items-center translate-y-[2.5px]"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        <CollapseButton variant="topbar" />
      </div>

      <div className="absolute inset-0 flex items-center justify-center pointer-events-none select-none px-2 gap-2">
        <span className="text-xs text-text-secondary truncate max-w-[calc(100%-27rem)]">{title}</span>
      </div>

      <div className="flex-1" />
      <div
        data-testid="layout-buttons"
        className="shrink-0 flex items-center gap-0.5 translate-y-[2.5px]"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        {patterns.map(({ pattern, icon: Icon, label }) => (
          <button
            key={pattern}
            disabled={!activeTabId}
            className="p-1 rounded cursor-pointer text-text-secondary hover:text-text-primary hover:bg-surface-hover disabled:opacity-40 disabled:pointer-events-none"
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

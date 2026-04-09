import { Columns, Rows, GridFour, Square } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import type { LayoutPattern } from '../types/tab'

interface Props {
  title: string
}

const patterns: { pattern: LayoutPattern; icon: typeof Square; label: string }[] = [
  { pattern: 'single', icon: Square, label: 'Single pane' },
  { pattern: 'split-h', icon: Columns, label: 'Split horizontal' },
  { pattern: 'split-v', icon: Rows, label: 'Split vertical' },
  { pattern: 'grid-4', icon: GridFour, label: 'Grid' },
]

export function TitleBar({ title }: Props) {
  const activeTabId = useTabStore((s) => s.activeTabId)

  const handlePattern = (pattern: LayoutPattern) => {
    if (!activeTabId) return
    useTabStore.getState().applyLayout(activeTabId, pattern)
  }

  return (
    <div
      className="shrink-0 flex items-center bg-surface-secondary border-b border-border-subtle px-2"
      style={{ height: 30, WebkitAppRegion: 'drag' } as React.CSSProperties}
    >
      <div className="shrink-0" style={{ width: 70 }} />
      <div className="flex-1 text-center text-xs text-text-muted truncate select-none">{title}</div>
      <div
        data-testid="layout-buttons"
        className="shrink-0 flex items-center gap-0.5"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
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

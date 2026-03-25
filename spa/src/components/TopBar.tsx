// @deprecated Phase 1 — TopBar 功能已被 TabBar 取代。確認無其他引用後可刪除。
// spa/src/components/TopBar.tsx
import { Terminal, Lightning } from '@phosphor-icons/react'

interface Props {
  sessionName: string
  mode: string
  onModeChange: (mode: string) => void
}

export default function TopBar({ sessionName, mode, onModeChange }: Props) {
  return (
    <div className="h-10 bg-surface-input border-b border-border-default flex items-center px-3 gap-3 relative">
      <span className="text-sm text-text-primary font-medium truncate">{sessionName}</span>
      <div className="flex-1" />

      <div className="flex items-center gap-1" data-testid="mode-switch">
        {/* Term */}
        <button
          onClick={() => onModeChange('term')}
          data-testid="mode-btn-term"
          className={`flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer transition-colors ${
            mode === 'term' ? 'bg-border-default text-text-primary' : 'text-text-muted hover:text-text-secondary hover:bg-surface-hover'
          }`}
        >
          <Terminal size={14} weight={mode === 'term' ? 'fill' : 'regular'} />
          <span>term</span>
        </button>

        {/* Stream */}
        <button
          onClick={() => onModeChange('stream')}
          data-testid="mode-btn-stream"
          className={`flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer transition-colors ${
            mode === 'stream' ? 'bg-border-default text-text-primary' : 'text-text-muted hover:text-text-secondary hover:bg-surface-hover'
          }`}
        >
          <Lightning size={14} weight={mode === 'stream' ? 'fill' : 'regular'} />
          <span>stream</span>
        </button>
      </div>
    </div>
  )
}

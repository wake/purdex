import { X, ArrowSquareOut } from '@phosphor-icons/react'

interface Props {
  title: string
  onClose: () => void
  onDetach?: () => void
}

export function PaneHeader({ title, onClose, onDetach }: Props) {
  return (
    <div className="shrink-0 flex items-center h-6 px-2 bg-surface-secondary border-b border-border-subtle">
      <span className="flex-1 text-xs text-text-muted truncate">{title}</span>
      <div className="flex items-center gap-0.5">
        {onDetach && (
          <button
            className="p-0.5 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
            title="Detach to tab"
            onClick={onDetach}
          >
            <ArrowSquareOut size={12} />
          </button>
        )}
        <button
          className="p-0.5 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
          title="Close pane"
          onClick={onClose}
        >
          <X size={12} />
        </button>
      </div>
    </div>
  )
}

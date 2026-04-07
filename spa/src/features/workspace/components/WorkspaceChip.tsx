import { CaretDown } from '@phosphor-icons/react'

interface Props {
  name: string | null
  color: string | null
  onClick: () => void
  onContextMenu: (e: React.MouseEvent) => void
}

export function WorkspaceChip({ name, color, onClick, onContextMenu }: Props) {
  if (!name) return null
  return (
    <button onClick={onClick} onContextMenu={onContextMenu}
      className="flex items-center gap-1.5 px-2 py-1 rounded-md text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors flex-shrink-0">
      <span data-testid="workspace-color-dot" className="w-2.5 h-2.5 rounded-full flex-shrink-0" style={{ backgroundColor: color ?? '#888' }} />
      <span className="truncate max-w-24">{name}</span>
      <CaretDown size={10} className="flex-shrink-0 opacity-60" />
    </button>
  )
}

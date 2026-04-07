import { CaretDown } from '@phosphor-icons/react'
import { WorkspaceIcon } from './WorkspaceIcon'

interface Props {
  name: string | null
  color: string | null
  icon: string | undefined
  onClick: () => void
  onContextMenu: (e: React.MouseEvent) => void
}

export function WorkspaceChip({ name, color, icon, onClick, onContextMenu }: Props) {
  if (!name) return null
  const c = color ?? '#888'
  return (
    <div className="flex items-center flex-shrink-0">
      <button
        onClick={onClick}
        onContextMenu={onContextMenu}
        className="flex items-center gap-1.5 px-2 py-1 rounded-md cursor-pointer transition-colors hover:bg-surface-hover"
      >
        {/* Icon square */}
        <div
          data-testid="workspace-chip-icon"
          className="w-5 h-5 rounded flex items-center justify-center"
          style={{ backgroundColor: c + '99', color: c }}
        >
          <WorkspaceIcon icon={icon} name={name} size={12} />
        </div>
        {/* Name */}
        <span className="truncate max-w-28 text-[13px] font-semibold" style={{ color: c }}>
          {name}
        </span>
        {/* Chevron */}
        <CaretDown size={10} className="flex-shrink-0 opacity-30" />
      </button>
      {/* Separator */}
      <div data-testid="workspace-chip-separator" className="w-px h-5.5 bg-border-default mx-2 flex-shrink-0" />
    </div>
  )
}

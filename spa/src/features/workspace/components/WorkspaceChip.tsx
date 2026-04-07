import { CaretRight } from '@phosphor-icons/react'
import type { IconWeight } from '../../../types/tab'
import { WorkspaceIcon } from './WorkspaceIcon'
import { workspaceColorStyle } from '../lib/workspace-colors'

interface Props {
  name: string | null
  color: string | null
  icon: string | undefined
  iconWeight?: IconWeight
  onClick: () => void
  onContextMenu: (e: React.MouseEvent) => void
}

export function WorkspaceChip({ name, color, icon, iconWeight, onClick, onContextMenu }: Props) {
  if (!name) return null
  const cs = workspaceColorStyle(color ?? '#888')
  return (
    <div className="flex items-center flex-shrink-0">
      <button
        onClick={onClick}
        onContextMenu={onContextMenu}
        className="flex items-center gap-2 px-2 py-1 rounded-md cursor-pointer transition-colors hover:bg-surface-hover"
      >
        {/* Icon — no background, bare */}
        <span data-testid="workspace-chip-icon" style={{ color: cs.fg }}>
          <WorkspaceIcon icon={icon} name={name} size={16} weight={iconWeight} />
        </span>
        {/* Name */}
        <span className="truncate max-w-28 text-[14px] font-bold" style={{ color: cs.fg }}>
          {name}
        </span>
      </button>
      {/* Breadcrumb separator */}
      <span data-testid="workspace-chip-separator" className="mx-1 flex-shrink-0" style={{ color: cs.fg }}>
        <CaretRight size={12} weight="bold" />
      </span>
    </div>
  )
}

import { Plus } from '@phosphor-icons/react'

export function WorkspaceEmptyState() {
  return (
    <div className="flex-1 flex flex-col items-center justify-center text-text-secondary gap-3">
      <div className="text-sm">No tabs in this workspace</div>
      <div className="flex items-center gap-1.5 text-xs text-text-tertiary">
        <Plus size={12} />
        <span>Press + to create a tab</span>
      </div>
    </div>
  )
}

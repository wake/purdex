// spa/src/features/workspace/components/GroupDropSection.tsx — a manual group or 「未分組」 as a drop target in the sidebar's DndContext (task groups spec §6.1; TG-3e). Lit while a row is over its header or body; a row
// of the group under the pointer wins over the section (smaller box), and that is a drop on the row's group too (lib/task-groups/drop-resolve.ts).
import type { ReactNode } from 'react'
import { useDroppable } from '@dnd-kit/core'
import type { DropTarget } from '../../../lib/task-groups/drag'
import type { GroupDropData } from '../lib/group-drag'

interface Props {
  id: string
  target: DropTarget
  testId: string
  className?: string
  collapsed?: boolean
  children: ReactNode
}

export function GroupDropSection({ id, target, testId, className = '', collapsed, children }: Props) {
  const data: GroupDropData = { type: 'group-drop', target }
  const { setNodeRef, isOver } = useDroppable({ id: `group-drop:${id}`, data })
  return (
    <section ref={setNodeRef} data-testid={testId} data-collapsed={collapsed === undefined ? undefined : String(collapsed)} className={`flex flex-col ${className}${isOver ? ' rounded-md ring-1 ring-border-active' : ''}`}>
      {children}
    </section>
  )
}

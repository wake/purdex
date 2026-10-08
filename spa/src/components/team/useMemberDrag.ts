// spa/src/components/team/useMemberDrag.ts — drag members within one team (beads and panel rows).
//
// Native HTML5 drag, so it nests inside the sidebar's dnd-kit context without fighting it. A drop only
// counts on another member of the same team; anywhere else the drag ends with no change (it "bounces").
import { useCallback, useState } from 'react'
import { moveInOrder } from './team-display'

let dragging: { teamKey: string; sessionId: string } | null = null

export interface MemberDragProps {
  draggable: true
  onDragStart: (e: React.DragEvent) => void
  onDragEnd: () => void
  onDragOver: (e: React.DragEvent) => void
  onDragLeave: () => void
  onDrop: (e: React.DragEvent) => void
}

export function useMemberDrag(teamKey: string, order: string[], onReorder: (ids: string[]) => void, axis: 'x' | 'y') {
  const [over, setOver] = useState<{ id: string; after: boolean } | null>(null)
  const [draggingId, setDraggingId] = useState<string | null>(null)

  const sideOf = useCallback((e: React.DragEvent) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    return axis === 'x' ? e.clientX >= r.left + r.width / 2 : e.clientY >= r.top + r.height / 2
  }, [axis])

  const propsFor = useCallback((sessionId: string): MemberDragProps => ({
    draggable: true,
    onDragStart: (e) => {
      dragging = { teamKey, sessionId }
      setDraggingId(sessionId)
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData('text/plain', sessionId)
    },
    onDragEnd: () => { dragging = null; setDraggingId(null); setOver(null) },
    onDragOver: (e) => {
      if (!dragging || dragging.teamKey !== teamKey || dragging.sessionId === sessionId) return
      e.preventDefault()
      setOver({ id: sessionId, after: sideOf(e) })
    },
    onDragLeave: () => setOver((o) => (o?.id === sessionId ? null : o)),
    onDrop: (e) => {
      if (!dragging || dragging.teamKey !== teamKey || dragging.sessionId === sessionId) return
      e.preventDefault()
      onReorder(moveInOrder(order, dragging.sessionId, sessionId, sideOf(e)))
      setOver(null)
    },
  }), [teamKey, order, onReorder, sideOf])

  return { propsFor, over, draggingId }
}

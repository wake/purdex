// spa/src/hooks/usePaneFileDrop.tsx — files dropped anywhere on a session pane (deck / chat) go to the pane's input as attachments, and the
// whole pane lights up while a file is over it (the input's own highlight, widened). The input still takes a drop on itself; this only
// takes what lands elsewhere (the input's handler has already called preventDefault by the time the drop bubbles here).
import { useEffect, useRef, useState, type DragEvent, type RefObject } from 'react'
import { dropTargetIn } from '../lib/conversations/pane-drop'

export function usePaneFileDrop(root: RefObject<HTMLElement | null>) {
  const [dragging, setDragging] = useState(false)
  const depth = useRef(0)
  const end = () => { depth.current = 0; setDragging(false) }
  useEffect(() => {
    if (!dragging) return
    const off = (e: Event) => { if (e.type !== 'keydown' || (e as KeyboardEvent).key === 'Escape') end() }
    window.addEventListener('keydown', off)
    window.addEventListener('dragend', off)
    window.addEventListener('drop', off)
    return () => { window.removeEventListener('keydown', off); window.removeEventListener('dragend', off); window.removeEventListener('drop', off) }
  }, [dragging])
  const takes = (e: DragEvent) => e.dataTransfer.types.includes('Files') && dropTargetIn(root.current) !== null
  const handlers = {
    onDragEnter: (e: DragEvent) => { if (takes(e)) { depth.current++; setDragging(true) } },
    onDragLeave: (e: DragEvent) => { if (takes(e) && --depth.current <= 0) end() },
    onDragOver: (e: DragEvent) => { if (takes(e)) e.preventDefault() },
    onDrop: (e: DragEvent) => {
      end()
      if (e.defaultPrevented) return // the input took it
      const attach = dropTargetIn(root.current)
      const files = [...e.dataTransfer.files]
      if (!attach || files.length === 0) return
      e.preventDefault()
      attach(files)
    },
  }
  const overlay = dragging
    ? <div data-testid="pane-drop-overlay" className="pointer-events-none absolute inset-0 z-10 border-2 border-dashed border-accent bg-accent/10" />
    : null
  return { handlers, overlay }
}

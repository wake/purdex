import { useCallback, useEffect, useRef } from 'react'

interface Props {
  direction: 'h' | 'v'
  onResize: (deltaPx: number) => void
  /** Fires once when the drag ends (mouseup or window blur), e.g. to commit a draft size. Not on unmount. */
  onResizeEnd?: () => void
  testId?: string
}

export function PaneSplitter({ direction, onResize, onResizeEnd, testId }: Props) {
  const startPos = useRef(0)
  const onResizeRef = useRef(onResize)
  const onResizeEndRef = useRef(onResizeEnd)
  // Ends the drag in progress: detaches its listeners and restores the body styles; `commit` also fires onResizeEnd.
  // Null while no drag is in progress.
  const endDragRef = useRef<((commit: boolean) => void) | null>(null)
  useEffect(() => {
    onResizeRef.current = onResize
    onResizeEndRef.current = onResizeEnd
  })
  // Unmount mid-drag cleans up without committing: the owner may be going away with it.
  useEffect(() => () => endDragRef.current?.(false), [])

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    endDragRef.current?.(true)
    startPos.current = direction === 'h' ? e.clientX : e.clientY

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const current = direction === 'h' ? moveEvent.clientX : moveEvent.clientY
      const delta = current - startPos.current
      onResizeRef.current(delta)
      startPos.current = current
    }

    // A window blur ends the drag like a mouseup: the mouseup may never reach this document, and the draft is
    // already on screen, so committing it keeps the screen and the owner's store in step.
    const handleEnd = () => endDrag(true)

    const endDrag = (commit: boolean) => {
      document.removeEventListener('mousemove', handleMouseMove)
      document.removeEventListener('mouseup', handleEnd)
      window.removeEventListener('blur', handleEnd)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      endDragRef.current = null
      if (commit) onResizeEndRef.current?.()
    }

    document.addEventListener('mousemove', handleMouseMove)
    document.addEventListener('mouseup', handleEnd)
    window.addEventListener('blur', handleEnd)
    endDragRef.current = endDrag
    document.body.style.cursor = direction === 'h' ? 'col-resize' : 'row-resize'
    document.body.style.userSelect = 'none'
  }, [direction])

  return (
    <div
      className={`shrink-0 group relative ${
        direction === 'h'
          ? 'w-1 cursor-col-resize'
          : 'h-1 cursor-row-resize'
      }`}
      onMouseDown={handleMouseDown}
      data-testid={testId}
    >
      {/* Visible bar */}
      <div className={`absolute ${
        direction === 'h'
          ? 'inset-y-0 left-1/2 -translate-x-1/2 w-[1px] group-hover:w-[3px] group-active:w-[3px]'
          : 'inset-x-0 top-1/2 -translate-y-1/2 h-[1px] group-hover:h-[3px] group-active:h-[3px]'
      } bg-border-subtle group-hover:bg-accent-base/50 group-active:bg-accent-base/70 transition-all`} />
      {/* Invisible hit area */}
      <div className={`absolute ${
        direction === 'h'
          ? 'inset-y-0 -left-1 -right-1'
          : 'inset-x-0 -top-1 -bottom-1'
      }`} />
    </div>
  )
}

import { useCallback, useEffect, useRef } from 'react'

interface Props {
  onResize: (delta: number) => void
  resizeEdge: 'left' | 'right'
  /** Fired once on mouseup. Useful for committing throttled / ephemeral state. */
  onResizeEnd?: () => void
}

export function RegionResize({ onResize, resizeEdge, onResizeEnd }: Props) {
  const startX = useRef(0)
  const onResizeRef = useRef(onResize)
  const onResizeEndRef = useRef(onResizeEnd)
  useEffect(() => {
    onResizeRef.current = onResize
    onResizeEndRef.current = onResizeEnd
  })

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    startX.current = e.clientX

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const rawDelta = moveEvent.clientX - startX.current
      const delta = resizeEdge === 'left' ? -rawDelta : rawDelta
      onResizeRef.current(delta)
      startX.current = moveEvent.clientX
    }

    const handleMouseUp = () => {
      document.removeEventListener('mousemove', handleMouseMove)
      document.removeEventListener('mouseup', handleMouseUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      onResizeEndRef.current?.()
    }

    document.addEventListener('mousemove', handleMouseMove)
    document.addEventListener('mouseup', handleMouseUp)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
  }, [resizeEdge])

  return (
    <div className="relative w-px shrink-0 bg-border-subtle">
      <div
        data-testid="resize-hit"
        className="absolute inset-y-0 -left-[5px] -right-[5px] cursor-col-resize hover:bg-accent-base/40 active:bg-accent-base/60"
        onMouseDown={handleMouseDown}
      />
    </div>
  )
}

import { useCallback, useEffect, useRef } from 'react'

interface Props {
  onResize: (delta: number) => void
  resizeEdge: 'left' | 'right'
}

export function RegionResize({ onResize, resizeEdge }: Props) {
  const startX = useRef(0)
  const onResizeRef = useRef(onResize)
  useEffect(() => { onResizeRef.current = onResize })

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
    }

    document.addEventListener('mousemove', handleMouseMove)
    document.addEventListener('mouseup', handleMouseUp)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
  }, [resizeEdge])

  return (
    <div
      className="w-1 shrink-0 cursor-col-resize hover:bg-accent-base/30 active:bg-accent-base/50 transition-colors"
      onMouseDown={handleMouseDown}
    />
  )
}

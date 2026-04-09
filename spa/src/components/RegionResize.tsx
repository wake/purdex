import { useCallback, useRef } from 'react'

interface Props {
  onResize: (delta: number) => void
  side: 'left' | 'right'
}

export function RegionResize({ onResize, side }: Props) {
  const startX = useRef(0)

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    startX.current = e.clientX

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const rawDelta = moveEvent.clientX - startX.current
      const delta = side === 'left' ? -rawDelta : rawDelta
      onResize(delta)
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
  }, [onResize, side])

  return (
    <div
      className="w-1 shrink-0 cursor-col-resize hover:bg-accent-base/30 active:bg-accent-base/50 transition-colors"
      onMouseDown={handleMouseDown}
    />
  )
}

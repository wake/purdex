import { useCallback, useEffect, useRef } from 'react'

interface Props {
  direction: 'h' | 'v'
  onResize: (deltaPx: number) => void
}

export function PaneSplitter({ direction, onResize }: Props) {
  const startPos = useRef(0)
  const onResizeRef = useRef(onResize)
  useEffect(() => { onResizeRef.current = onResize })

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    startPos.current = direction === 'h' ? e.clientX : e.clientY

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const current = direction === 'h' ? moveEvent.clientX : moveEvent.clientY
      const delta = current - startPos.current
      onResizeRef.current(delta)
      startPos.current = current
    }

    const handleMouseUp = () => {
      document.removeEventListener('mousemove', handleMouseMove)
      document.removeEventListener('mouseup', handleMouseUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }

    document.addEventListener('mousemove', handleMouseMove)
    document.addEventListener('mouseup', handleMouseUp)
    document.body.style.cursor = direction === 'h' ? 'col-resize' : 'row-resize'
    document.body.style.userSelect = 'none'
  }, [direction])

  return (
    <div
      className={`shrink-0 ${direction === 'h' ? 'w-1 cursor-col-resize' : 'h-1 cursor-row-resize'} hover:bg-accent-base/30 active:bg-accent-base/50 transition-colors`}
      onMouseDown={handleMouseDown}
    />
  )
}

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
      className={`shrink-0 group relative ${
        direction === 'h'
          ? 'w-1 cursor-col-resize'
          : 'h-1 cursor-row-resize'
      }`}
      onMouseDown={handleMouseDown}
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

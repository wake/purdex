import { useCallback, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

export type HoverTooltipPlacement = 'top' | 'right'

interface Props {
  children: ReactNode
  placement?: HoverTooltipPlacement
  'data-testid'?: string
}

const HOVER_TOOLTIP_OFFSET = 8
const HOVER_TOOLTIP_DELAY_MS = 800

export function HoverTooltip({ children, placement = 'right', 'data-testid': testId }: Props) {
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const [visible, setVisible] = useState(false)
  const [style, setStyle] = useState<CSSProperties>({ top: 0, left: 0 })
  const showTimerRef = useRef<ReturnType<typeof window.setTimeout> | null>(null)

  const clearShowTimer = useCallback(() => {
    if (!showTimerRef.current) return
    window.clearTimeout(showTimerRef.current)
    showTimerRef.current = null
  }, [])

  const updatePosition = useCallback(() => {
    if (!anchor) return
    const rect = anchor.getBoundingClientRect()
    if (placement === 'top') {
      setStyle({
        left: rect.left + rect.width / 2,
        top: rect.top - HOVER_TOOLTIP_OFFSET,
        transform: 'translate(-50%, -100%)',
      })
      return
    }
    setStyle({
      left: rect.right + HOVER_TOOLTIP_OFFSET,
      top: rect.top + rect.height / 2,
      transform: 'translateY(-50%)',
    })
  }, [anchor, placement])

  useLayoutEffect(() => {
    if (!anchor) return
    const show = () => {
      clearShowTimer()
      showTimerRef.current = window.setTimeout(() => {
        updatePosition()
        setVisible(true)
        showTimerRef.current = null
      }, HOVER_TOOLTIP_DELAY_MS)
    }
    const hide = () => {
      clearShowTimer()
      setVisible(false)
    }
    anchor.addEventListener('mouseenter', show)
    anchor.addEventListener('mouseleave', hide)
    anchor.addEventListener('focusin', show)
    anchor.addEventListener('focusout', hide)
    return () => {
      clearShowTimer()
      anchor.removeEventListener('mouseenter', show)
      anchor.removeEventListener('mouseleave', hide)
      anchor.removeEventListener('focusin', show)
      anchor.removeEventListener('focusout', hide)
    }
  }, [anchor, clearShowTimer, updatePosition])

  useLayoutEffect(() => {
    if (!visible) return
    window.addEventListener('resize', updatePosition)
    window.addEventListener('scroll', updatePosition, true)
    return () => {
      window.removeEventListener('resize', updatePosition)
      window.removeEventListener('scroll', updatePosition, true)
    }
  }, [visible, updatePosition])

  const setMarkerRef = useCallback((node: HTMLSpanElement | null) => {
    setAnchor(node?.parentElement ?? null)
  }, [])
  const marker = <span ref={setMarkerRef} hidden />
  if (typeof document === 'undefined') return marker

  return (
    <>
      {marker}
      {createPortal(
        <span
          role="tooltip"
          data-testid={testId}
          data-placement={placement}
          className={`pointer-events-none fixed whitespace-nowrap rounded bg-surface-secondary border border-border-default px-2 py-1 text-xs text-text-primary shadow-lg transition-opacity z-50 ${visible ? 'opacity-100' : 'opacity-0'}`}
          style={style}
        >
          {children}
        </span>,
        document.body,
      )}
    </>
  )
}

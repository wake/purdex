import { useState, useRef, useCallback, useEffect, type ReactNode } from 'react'
import { X, ArrowSquareOut, ArrowsLeftRight } from '@phosphor-icons/react'
import { useClickOutside } from '../hooks/useClickOutside'

interface SwapTarget {
  id: string
  label: string
}

interface Props {
  title: string
  onClose: () => void
  onDetach?: () => void
  onSwap?: (targetPaneId: string) => void
  swapTargets?: SwapTarget[]
  extraActions?: ReactNode
}

export function PaneHeader({ title, onClose, onDetach, onSwap, swapTargets, extraActions }: Props) {
  const [showSwapMenu, setShowSwapMenu] = useState(false)
  const swapMenuRef = useRef<HTMLDivElement>(null)
  const closeSwapMenu = useCallback(() => setShowSwapMenu(false), [])

  useClickOutside(swapMenuRef, closeSwapMenu)

  useEffect(() => {
    if (!showSwapMenu) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeSwapMenu()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [showSwapMenu, closeSwapMenu])

  return (
    <div className="shrink-0 flex items-center h-7 px-2 bg-surface-secondary border-b border-border-default">
      <span className="flex-1 text-xs text-text-muted truncate font-medium">{title}</span>
      <div className="flex items-center gap-0.5">
        {extraActions}
        {onSwap && swapTargets && swapTargets.length > 0 && (
          <div className="relative" ref={swapMenuRef}>
            <button
              className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
              title="Swap with..."
              onClick={() => setShowSwapMenu(!showSwapMenu)}
            >
              <ArrowsLeftRight size={12} />
            </button>
            {showSwapMenu && (
              <div className="absolute right-0 top-full mt-1 z-50 bg-surface-elevated border border-border-default rounded shadow-lg py-1 min-w-[120px]">
                {swapTargets.map((target) => (
                  <button
                    key={target.id}
                    className="w-full text-left px-3 py-1 text-xs hover:bg-surface-hover transition-colors"
                    onClick={() => { onSwap(target.id); setShowSwapMenu(false) }}
                  >
                    {target.label}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
        {onDetach && (
          <button
            className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
            title="Detach to tab"
            onClick={onDetach}
          >
            <ArrowSquareOut size={12} />
          </button>
        )}
        <button
          className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
          title="Close pane"
          onClick={onClose}
        >
          <X size={12} />
        </button>
      </div>
    </div>
  )
}

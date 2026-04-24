import { useEffect, useLayoutEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { Check, FilePlus, Stack } from '@phosphor-icons/react'
import { useClickOutside } from '../../hooks/useClickOutside'
import { useI18nStore } from '../../stores/useI18nStore'

interface BreadcrumbPopoverProps {
  buffers: string[]
  currentBufferKey: string
  onSwitch: (fullPath: string) => void
  onManage: () => void
  onDismiss: () => void
  anchorRect: DOMRect
  onNewBuffer?: () => void
}

const POPOVER_WIDTH = 240
const POPOVER_MAX_HEIGHT = 320
const PADDING = 4
const Z_INDEX = 100

/**
 * BreadcrumbPopover — quick-switch popover for inapp buffers (spec §4.8).
 *
 * Lists all /buffer/* entries with the current one marked, plus a "Manage
 * buffers..." link. The popover only fires its callbacks — the dirty-guard
 * and the actual `setPaneContent` / `openSingletonTab` calls live in the
 * caller (EditorPane). This file must NEVER import `useEditorStore` or
 * invoke the editor-store pane-rebinding action directly (spec §4.8
 * invariant — the rebind auto-fires from EditorPane's own useEffect).
 */
export function BreadcrumbPopover({
  buffers,
  currentBufferKey,
  onSwitch,
  onManage,
  onDismiss,
  anchorRect,
  onNewBuffer,
}: BreadcrumbPopoverProps) {
  const t = useI18nStore((s) => s.t)
  const containerRef = useRef<HTMLDivElement>(null)

  useClickOutside(containerRef, onDismiss)

  // Dismiss on Escape (document-level; popover may not have focus).
  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        event.preventDefault()
        onDismiss()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onDismiss])

  // Position below the anchor rect, clamped to the viewport.
  useLayoutEffect(() => {
    const el = containerRef.current
    if (!el) return
    let left = anchorRect.left
    left = Math.max(PADDING, Math.min(left, window.innerWidth - POPOVER_WIDTH - PADDING))
    const popoverHeight = el.offsetHeight
    let top = anchorRect.bottom + PADDING
    if (top + popoverHeight > window.innerHeight - PADDING) {
      top = anchorRect.top - PADDING - popoverHeight
    }
    if (top < PADDING) {
      top = PADDING
    }
    el.style.left = `${left}px`
    el.style.top = `${top}px`
  }, [anchorRect])

  const content = (
    <div
      ref={containerRef}
      role="dialog"
      aria-label="Buffer quick switch"
      className="fixed bg-surface-elevated border border-border-default rounded-lg shadow-xl overflow-hidden flex flex-col"
      style={{ width: POPOVER_WIDTH, maxHeight: POPOVER_MAX_HEIGHT, zIndex: Z_INDEX }}
    >
      {buffers.length === 0 ? (
        <div className="p-3 text-xs text-text-muted">
          <p>{t('editor.buffers.empty')}</p>
          {onNewBuffer && (
            <button
              type="button"
              className="mt-2 w-full flex items-center gap-1.5 px-2 py-1 rounded hover:bg-surface-hover text-text-primary"
              onClick={() => {
                onNewBuffer()
                onDismiss()
              }}
              data-testid="breadcrumb-popover-new-buffer"
            >
              <FilePlus size={14} />
              <span>{t('editor.buffers.new')}</span>
            </button>
          )}
        </div>
      ) : (
        <ul className="flex-1 min-h-0 overflow-y-auto py-1">
          {buffers.map((name) => {
            const fullPath = `/buffer/${name}`
            const isCurrent = fullPath === currentBufferKey
            return (
              <li key={name}>
                <button
                  type="button"
                  aria-current={isCurrent ? 'true' : undefined}
                  onClick={() => {
                    if (!isCurrent) onSwitch(fullPath)
                    onDismiss()
                  }}
                  className={`w-full flex items-center gap-1.5 px-3 py-1 text-xs text-left truncate ${
                    isCurrent
                      ? 'text-text-primary font-medium'
                      : 'text-text-secondary hover:bg-surface-hover'
                  }`}
                >
                  <span className="shrink-0 w-3.5 h-3.5 flex items-center justify-center">
                    {isCurrent ? <Check size={12} /> : null}
                  </span>
                  <span className="truncate">{name}</span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
      <div className="border-t border-border-subtle">
        <button
          type="button"
          data-testid="breadcrumb-popover-manage"
          onClick={() => {
            onManage()
            onDismiss()
          }}
          className="w-full flex items-center gap-1.5 px-3 py-1.5 text-xs text-text-secondary hover:bg-surface-hover"
        >
          <Stack size={14} />
          <span>{t('editor.buffers.tab_title')}</span>
        </button>
      </div>
    </div>
  )

  return createPortal(content, document.body)
}

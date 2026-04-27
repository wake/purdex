import { useEffect, useMemo, useRef, type CSSProperties, type KeyboardEvent } from 'react'
import { X } from '@phosphor-icons/react'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  open: boolean
  /**
   * Anchor for positioning. `{x, y}` → fixed positioning at viewport coords.
   * HTMLElement → positioned next to that element via getBoundingClientRect.
   * `null` allowed during transition; popover renders centered as fallback.
   */
  anchor: { x: number; y: number } | HTMLElement | null
  onSelect: (hostId: string) => void
  onCancel: () => void
}

/**
 * Reusable host picker popover. Caller-controlled open/anchor; emits
 * `onSelect(hostId)` or `onCancel()`. Used by:
 *   - WORKSPACE_ACTIONS slot when inferWorkspaceHostId returns null
 *   - (Future) multi-host workspace binding default-launcher selection (spec §3.5)
 *
 * a11y:
 *   - role="listbox" on container, role="option" on each host row
 *   - Enter on focused option → onSelect; Esc → onCancel; Arrow Up/Down moves
 *     focus; Tab wraps within popover (focus trap).
 *   - Caller is responsible for restoring focus to its trigger after
 *     onSelect/onCancel returns.
 */
export function HostPickerPopover({ open, anchor, onSelect, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const hosts = useHostStore((s) => s.hosts)
  const runtime = useHostStore((s) => s.runtime)
  const containerRef = useRef<HTMLDivElement>(null)

  const items = useMemo(
    () =>
      hostOrder
        .map((id) => hosts[id])
        .filter((h): h is NonNullable<typeof h> => !!h)
        .map((h) => ({
          id: h.id,
          name: h.name,
          online: runtime[h.id]?.status === 'connected',
        })),
    [hostOrder, hosts, runtime],
  )

  // Auto-focus first item on open.
  useEffect(() => {
    if (!open) return
    const first = containerRef.current?.querySelector<HTMLElement>('[role="option"]')
    first?.focus()
  }, [open])

  // Esc → onCancel (document-level so it works regardless of focus position).
  useEffect(() => {
    if (!open) return
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onCancel()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onCancel])

  if (!open) return null

  const positionStyle: CSSProperties = (() => {
    if (anchor && typeof anchor === 'object' && 'getBoundingClientRect' in anchor) {
      const r = anchor.getBoundingClientRect()
      return { position: 'fixed', top: r.bottom + 4, left: r.left }
    }
    if (anchor && typeof anchor === 'object' && 'x' in anchor && 'y' in anchor) {
      return { position: 'fixed', top: anchor.y, left: anchor.x }
    }
    return { position: 'fixed', top: '50%', left: '50%', transform: 'translate(-50%, -50%)' }
  })()

  function handleItemKey(
    e: KeyboardEvent<HTMLDivElement>,
    hostId: string,
    index: number,
  ) {
    if (e.key === 'Enter') {
      e.preventDefault()
      onSelect(hostId)
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      const next = (index + 1) % items.length
      ;(containerRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[next])?.focus()
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      const prev = (index - 1 + items.length) % items.length
      ;(containerRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[prev])?.focus()
      return
    }
    if (e.key === 'Tab') {
      e.preventDefault()
      const len = items.length
      if (len === 0) return
      const dir = e.shiftKey ? -1 : 1
      const next = (index + dir + len) % len
      ;(containerRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[next])?.focus()
    }
  }

  return (
    <div
      ref={containerRef}
      role="listbox"
      aria-label={t('quick_commands.host_picker.label')}
      style={positionStyle}
      className="z-50 min-w-[200px] rounded-md border border-border-default bg-surface-secondary shadow-lg py-1"
    >
      {items.length === 0 ? (
        // codex round-1 B2 — empty state must offer an explicit close button
        // (mouse users / a11y; Esc-only is not sufficient).
        <div className="px-3 py-2 text-xs text-text-muted flex items-center justify-between gap-2">
          <span>{t('quick_commands.host_picker.empty')}</span>
          <button
            type="button"
            onClick={onCancel}
            aria-label={t('quick_commands.host_picker.close')}
            className="p-0.5 text-text-muted hover:text-text-primary cursor-pointer"
          >
            <X size={12} />
          </button>
        </div>
      ) : (
        items.map((item, index) => (
          <div
            key={item.id}
            role="option"
            tabIndex={0}
            aria-selected={false}
            onClick={() => onSelect(item.id)}
            onKeyDown={(e) => handleItemKey(e, item.id, index)}
            className="flex items-center justify-between gap-2 px-3 py-1.5 text-xs text-text-primary cursor-pointer hover:bg-surface-hover focus:bg-surface-hover focus:outline-none"
          >
            <span className="truncate">{item.name}</span>
            <span
              className={
                item.online
                  ? 'text-[10px] text-text-secondary bg-surface-primary px-1.5 py-0.5 rounded'
                  : 'text-[10px] text-text-muted bg-surface-primary px-1.5 py-0.5 rounded'
              }
            >
              {item.online
                ? t('quick_commands.host_picker.online')
                : t('quick_commands.host_picker.offline')}
            </span>
          </div>
        ))
      )}
    </div>
  )
}

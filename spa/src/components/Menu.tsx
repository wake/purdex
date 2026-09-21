import { useEffect, useLayoutEffect, useRef, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { Check, CircleNotch } from '@phosphor-icons/react'
import { getPlatformCapabilities } from '../lib/platform'
import { TITLE_BAR_HEIGHT } from './FloatingPanel'

export interface MenuItem {
  id: string
  label: string
  /** Leading slot, before the label. */
  icon?: ReactNode
  /** Secondary text after the label, muted. */
  hint?: string
  /** Trailing slot at the far right (a status dot, a shortcut). */
  trailing?: ReactNode
  /** Native tooltip — for a label that may be truncated. */
  title?: string
  /** Defined (true OR false) makes the item a `menuitemradio`; `true` is the chosen one and where focus lands on open. */
  checked?: boolean
  /** Shown, skipped by the keyboard, never activated. */
  disabled?: boolean
  /** Work for this item is under way: a spinner, `aria-busy`, and it cannot be activated again. */
  busy?: boolean
  /** Activating does not close the menu — the owner closes it when its work has ended. */
  keepOpen?: boolean
  onSelect: () => void
  testId?: string
}

export type MenuEntry = MenuItem | { divider: true }

/** Where the menu opens: under the trigger, or beside it (a trigger in a bar at the window's left edge). */
export type MenuPlacement = 'bottom-start' | 'right-start'

export interface MenuProps {
  /** The element that opens the menu. It owns `aria-haspopup="menu"` and `aria-expanded`; focus returns to it on close. */
  trigger: RefObject<HTMLElement | null>
  open: boolean
  onClose: () => void
  items: MenuEntry[]
  /** The menu's accessible name. */
  label: string
  placement?: MenuPlacement
  testId?: string
}

const PADDING = 4
const Z_INDEX = 100
/** See `FloatingPanel.tsx`: the IME's own Escape is not ours. */
const isImeEscape = (e: KeyboardEvent) => e.isComposing || e.keyCode === 229
const isItem = (e: MenuEntry): e is MenuItem => !('divider' in e)
const isAvailable = (e: MenuItem) => !e.disabled && !e.busy

/**
 * The menu primitive: a portal on `document.body` (so no `overflow-hidden` ancestor clips it — the 44 px narrow
 * bar is one), anchored to `trigger` and kept inside the viewport, `role="menu"` with the WAI-ARIA keyboard:
 * ↑ / ↓ (wrapping), Home / End, Enter / Space, Escape, Tab. Focus moves in on open and back to the trigger on
 * close. No type-ahead, no submenus.
 *
 * It is `FloatingPanel`'s sibling, not its child: that one is a titled, draggable `role="dialog"` — a header, a
 * × button and a drag handle a menu must not have — so what is shared is its conventions (portal, imperative
 * position, the Electron title-bar inset, the IME Escape guard, "restore focus only if it is still ours").
 */
export function Menu(props: MenuProps) {
  // A fresh instance per opening: every effect below is a mount / unmount effect.
  if (!props.open) return null
  return <MenuPopup {...props} />
}

function MenuPopup({ trigger, onClose, items, label, placement = 'bottom-start', testId = 'menu' }: MenuProps) {
  const menuRef = useRef<HTMLDivElement>(null)
  // Under Electron the top 36 px is the OS drag region: a menu there could not be clicked (see `FloatingPanel`).
  const topInset = getPlatformCapabilities().isElectron ? TITLE_BAR_HEIGHT : PADDING

  // Imperative, like `FloatingPanel`: the position depends on the menu's own measured size.
  const place = () => {
    const el = menuRef.current
    if (!el) return
    const a = trigger.current?.getBoundingClientRect()
    const { width, height } = el.getBoundingClientRect()
    const vw = window.innerWidth
    const vh = window.innerHeight
    let left = PADDING
    let top = topInset
    if (a && placement === 'right-start') {
      left = a.right + PADDING
      top = a.top
      if (left + width > vw - PADDING) left = a.left - PADDING - width
    } else if (a) {
      left = a.left
      top = a.bottom + PADDING
      if (top + height > vh - PADDING) top = a.top - PADDING - height
    }
    left = Math.max(PADDING, Math.min(left, vw - width - PADDING))
    top = Math.max(topInset, Math.min(top, vh - height - PADDING))
    el.style.left = `${left}px`
    el.style.top = `${top}px`
    el.style.maxHeight = `${Math.max(0, vh - top - PADDING)}px`
  }

  // Before paint, and again whenever the entries change (a status arriving can change the height).
  useLayoutEffect(() => {
    place()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, placement])

  useEffect(() => {
    const onReflow = () => place()
    window.addEventListener('resize', onReflow)
    document.addEventListener('scroll', onReflow, { capture: true })
    return () => {
      window.removeEventListener('resize', onReflow)
      document.removeEventListener('scroll', onReflow, { capture: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const itemEls = () => Array.from(menuRef.current?.querySelectorAll<HTMLElement>('[data-menu-item]') ?? [])
  const availableEls = () => itemEls().filter((el) => el.dataset.menuItem === 'available')

  // Focus in on open: the chosen item, else the first that can be activated, else the menu itself (so Escape
  // and Tab still have a target). Back to the trigger on close — unless something else took focus on purpose.
  useEffect(() => {
    const menu = menuRef.current
    const triggerEl = trigger.current
    const available = availableEls()
    ;(available.find((el) => el.getAttribute('aria-checked') === 'true') ?? available[0] ?? menu)?.focus()
    return () => {
      const active = document.activeElement
      const stillOurs = active === null || active === document.body || (menu?.contains(active) ?? false)
      if (stillOurs && triggerEl?.isConnected) triggerEl.focus()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const onMouseDown = (e: MouseEvent) => {
      const target = e.target as Node
      if (menuRef.current?.contains(target)) return
      // The trigger's own click toggles the menu: closing here would let that click re-open it.
      if (trigger.current?.contains(target)) return
      onClose()
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || isImeEscape(e)) return
      e.preventDefault()
      onClose()
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [trigger, onClose])

  const activate = (item: MenuItem) => {
    if (!isAvailable(item)) return
    item.onSelect()
    if (!item.keepOpen) onClose()
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Tab') {
      // Not swallowed: focus goes back to the trigger as the menu unmounts, and the browser tabs on from there.
      onClose()
      return
    }
    const available = availableEls()
    const at = available.indexOf(document.activeElement as HTMLElement)
    let next: HTMLElement | undefined
    if (e.key === 'ArrowDown') next = available[(at + 1) % available.length]
    else if (e.key === 'ArrowUp') next = available[(at <= 0 ? available.length : at) - 1]
    else if (e.key === 'Home') next = available[0]
    else if (e.key === 'End') next = available[available.length - 1]
    else if (e.key === 'Enter' || e.key === ' ') {
      // Swallowed, so the button's own keyboard click does not activate a second time.
      e.preventDefault()
      const id = (document.activeElement as HTMLElement | null)?.dataset.menuId
      const item = items.filter(isItem).find((i) => i.id === id)
      if (item) activate(item)
      return
    } else return
    e.preventDefault()
    next?.focus()
  }

  const content = (
    <div
      ref={menuRef}
      role="menu"
      aria-label={label}
      aria-orientation="vertical"
      tabIndex={-1}
      data-testid={testId}
      onKeyDown={onKeyDown}
      className="bg-surface-elevated border border-border-default rounded-lg shadow-xl py-1 min-w-[180px] max-w-[280px] overflow-y-auto text-xs text-text-primary focus:outline-none"
      style={{ position: 'fixed', left: PADDING, top: topInset, zIndex: Z_INDEX, WebkitAppRegion: 'no-drag' } as React.CSSProperties}
    >
      {items.map((entry, i) => {
        if (!isItem(entry)) return <div key={`divider-${i}`} role="separator" className="my-1 h-px bg-border-default" />
        const available = isAvailable(entry)
        const radio = entry.checked !== undefined
        return (
          <button
            key={entry.id}
            type="button"
            role={radio ? 'menuitemradio' : 'menuitem'}
            aria-checked={radio ? entry.checked : undefined}
            // `aria-disabled`, not `disabled`: the item stays in the accessibility tree and keeps focus if it
            // turns unavailable while focused. `activate` is what refuses.
            aria-disabled={entry.disabled ? true : undefined}
            aria-busy={entry.busy ? true : undefined}
            tabIndex={-1}
            title={entry.title}
            data-menu-item={available ? 'available' : 'unavailable'}
            data-menu-id={entry.id}
            data-testid={entry.testId}
            onClick={() => activate(entry)}
            className={`w-full flex items-center gap-2 px-3 py-1.5 text-left transition-colors focus:outline-none ${
              available ? 'cursor-pointer hover:bg-surface-hover focus:bg-surface-hover' : 'cursor-default'
            } ${entry.disabled ? 'text-text-muted' : ''}`}
          >
            {radio && (
              <span className="w-3.5 shrink-0 flex items-center justify-center">
                {entry.checked && <Check size={14} />}
              </span>
            )}
            {entry.icon && <span className="shrink-0 flex items-center">{entry.icon}</span>}
            <span className="flex-1 min-w-0 truncate">{entry.label}</span>
            {entry.hint && <span className="shrink-0 text-text-muted">{entry.hint}</span>}
            {entry.busy
              ? <CircleNotch size={12} className="shrink-0 animate-spin text-text-secondary" />
              : entry.trailing && <span className="shrink-0 flex items-center">{entry.trailing}</span>}
          </button>
        )
      })}
    </div>
  )
  return createPortal(content, document.body)
}

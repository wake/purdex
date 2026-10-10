// spa/src/components/deck/SessionSplit.tsx — the layout shell of a session pane: the chat on the left, an optional right panel
// (SessionRightPanel, D10) on the right. When the pane is too narrow to keep the chat at CHAT_MIN_W next to the panel, the panel
// becomes an OVERLAY on the right edge (scrim behind it; scrim click, the panel's ✕ or Esc closes it). Pure layout: the caller
// hands the chat as `children`, the panel as `panel`, and whether it is open.
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { panelDocks } from './split-layout'

const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),textarea:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])'
function focusables(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => !el.closest('[inert]'))
}

const validWidth = (w: number | null | undefined): number | null => (typeof w === 'number' && Number.isFinite(w) && w > 0 ? w : null)

/** The container's width: read synchronously when the element attaches (so the FIRST paint is already right), then followed by a ResizeObserver. */
function useWidth(override?: number): [number | null, (el: HTMLDivElement | null) => void, React.RefObject<HTMLDivElement | null>] {
  const [w, setW] = useState<number | null>(null)
  const node = useRef<HTMLDivElement | null>(null)
  const attach = useCallback((el: HTMLDivElement | null) => {
    node.current = el
    if (el) setW(validWidth(el.getBoundingClientRect().width))
  }, [])
  useLayoutEffect(() => {
    const el = node.current
    if (override !== undefined || !el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver((entries) => setW(validWidth(entries[entries.length - 1]?.contentRect.width)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [override])
  return [override !== undefined ? validWidth(override) : w, attach, node]
}

interface Props {
  children: ReactNode
  /** The right panel (e.g. `<SessionRightPanel …/>`); drawn only while `open`. */
  panel: ReactNode
  open: boolean
  /** Closes the panel (scrim click and Esc in overlay mode). */
  onClose: () => void
  /** Test hook: use this container width instead of measuring. */
  widthOverride?: number
}

export function SessionSplit({ children, panel, open, onClose, widthOverride }: Props) {
  const t = useI18nStore((s) => s.t)
  const [width, attach] = useWidth(widthOverride)
  const measured = width !== null
  const overlay = open && measured && !panelDocks(width)

  const dialog = useRef<HTMLDivElement>(null)
  const lastFocus = useRef<HTMLElement | null>(null)

  // Overlay is modal: focus goes into the panel, and back to where it was when the overlay ends.
  useEffect(() => {
    if (!overlay) return
    const opener = lastFocus.current
    const d = dialog.current
    if (d) (focusables(d)[0] ?? d).focus()
    return () => { if (opener && opener.isConnected) opener.focus() }
  }, [overlay])

  const trapTab = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'Tab') return
    const d = dialog.current
    if (!d) return
    const list = focusables(d)
    if (list.length === 0) { e.preventDefault(); return }
    const first = list[0], last = list[list.length - 1]
    const at = document.activeElement
    if (e.shiftKey && (at === first || at === d)) { e.preventDefault(); last.focus() }
    else if (!e.shiftKey && (at === last || at === d)) { e.preventDefault(); first.focus() }
  }

  // Overlay only: Esc closes — unless a text field has it.
  useEffect(() => {
    if (!overlay) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      const el = e.target as HTMLElement | null
      if (el && (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT' || el.isContentEditable)) return
      onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [overlay, onClose])

  return (
    <div ref={attach} data-testid="session-split" data-mode={!open ? 'closed' : !measured ? 'measuring' : overlay ? 'overlay' : 'docked'} className="relative flex h-full min-w-0"
      onFocusCapture={(e) => { if (!overlay) lastFocus.current = e.target as HTMLElement }}>
      <div data-testid="split-chat" inert={overlay} aria-hidden={overlay ? true : undefined} className="flex min-w-0 flex-1 flex-col">{children}</div>
      {open && measured && !overlay && panel}
      {overlay && (
        <div data-testid="split-overlay" className="absolute inset-0 z-10">
          <button type="button" data-testid="split-scrim" tabIndex={-1} aria-label={t('split.close')} onClick={onClose}
            className="absolute inset-0 cursor-default bg-black/40" />
          {/* full width so the panel's own 42 % / 320–640 rule resolves against the pane; clicks outside the panel fall through to the scrim */}
          <div ref={dialog} role="dialog" aria-modal="true" aria-label={t('split.panel')} tabIndex={-1} onKeyDown={trapTab}
            className="pointer-events-none relative flex h-full w-full justify-end outline-none *:pointer-events-auto *:shadow-2xl">{panel}</div>
        </div>
      )}
    </div>
  )
}

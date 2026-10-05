// spa/src/components/ConfirmDialog.tsx — the small modal shell shared by the
// nex confirm steps ("Hand to nex", "Take back to terminal"): backdrop, title
// + body, Cancel / Confirm. Presentational: the caller owns whatever the
// confirm does. While `busy`, Escape, the backdrop and both buttons are inert
// and the confirm button shows a spinner. `children` render under the body
// for the caller's own controls (a checkbox, a warning line).
//
// Focus (shell polish spec §4): the dialog takes focus onto its panel when it
// opens — not onto a button, so a stray Enter or keystroke neither confirms
// nor cancels, and none of it reaches the pane behind (a title bar or status
// bar button that opened it does not take focus on a mouse press, so focus
// would otherwise still be in the pane). Tab / Shift+Tab stay inside while it
// is up. On close, focus goes back to whatever had it when the dialog opened —
// only while the dialog still holds it, as `FloatingPanel` does.
import { useEffect, useRef, type ReactNode } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'

// What Tab can land on inside the panel (the same list `FloatingPanel` uses); disabled controls are dropped at use.
const FOCUSABLE_SELECTOR = 'input, button, [tabindex]:not([tabindex="-1"]), select, textarea'

/** The panel's Tab stops, in document order. */
function tabStops(panel: HTMLElement): HTMLElement[] {
  return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
    .filter((el) => !(el as HTMLButtonElement).disabled && el.tabIndex >= 0)
}

export interface ConfirmDialogProps {
  /** `${testIdPrefix}-dialog` (the backdrop) / `-panel` (takes focus on open) / `-cancel` / `-confirm`. */
  testIdPrefix: string
  title: string
  body: string
  confirmLabel: string
  busy?: boolean
  /** The confirm button is inert (for example until a choice in `children` is complete). Cancel stays live. */
  confirmDisabled?: boolean
  onCancel: () => void
  onConfirm: () => void
  children?: ReactNode
}

export function ConfirmDialog({ testIdPrefix, title, body, confirmLabel, busy = false, confirmDisabled = false, onCancel, onConfirm, children }: ConfirmDialogProps) {
  const t = useI18nStore((s) => s.t)
  const panelRef = useRef<HTMLDivElement>(null)

  // Take focus on open; on close, give it back to what had it — only if the dialog still holds it. By the time this
  // cleanup runs the dialog is out of the DOM, so focus that was inside it has fallen to body (or nowhere): that counts
  // as still held. Anything else focused means something took it on purpose meanwhile (a tab switch that cancelled
  // the dialog and focused the new tab's pane), and is left alone. The element remembered may be gone by then too.
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null
    const panel = panelRef.current
    panel?.focus()
    return () => {
      const active = document.activeElement
      const stillOwnsFocus = active === null || active === document.body || (panel?.contains(active) ?? false)
      if (!stillOwnsFocus) return
      if (previouslyFocused?.isConnected) previouslyFocused.focus()
    }
  }, [])

  // Tab is the dialog's while it is up: it moves through the panel's controls and wraps at either end; from the panel
  // itself (or from anywhere outside the dialog — e.g. body, once a focused button went disabled) Tab enters at the
  // first control and Shift+Tab at the last. Nothing to land on (all disabled while busy) → focus stays on the panel.
  // Every step is done here rather than left to the browser, so no step can fall out of the dialog. Ctrl / Cmd / Alt +
  // Tab is not focus navigation (Ctrl+Tab switches tabs) and is left alone.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Tab' || e.ctrlKey || e.metaKey || e.altKey) return
      const panel = panelRef.current
      if (!panel) return
      e.preventDefault()
      const stops = tabStops(panel)
      if (stops.length === 0) {
        panel.focus()
        return
      }
      const at = stops.indexOf(document.activeElement as HTMLElement)
      const next = at === -1
        ? (e.shiftKey ? stops.length - 1 : 0)
        : (at + (e.shiftKey ? stops.length - 1 : 1)) % stops.length
      stops[next].focus()
    }
    document.addEventListener('keydown', onKey, { capture: true })
    return () => document.removeEventListener('keydown', onKey, { capture: true })
  }, [])

  // Escape is the dialog's while it is up — busy or not — also when it was opened from inside a FloatingPanel: taken
  // in the CAPTURE phase (the panel mounted first, so its bubble listener on `document` would run first) and marked
  // handled (`preventDefault`), which FloatingPanel leaves alone. One Escape closes one thing: the topmost.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      if (!busy) onCancel()
    }
    document.addEventListener('keydown', onKey, { capture: true })
    return () => document.removeEventListener('keydown', onKey, { capture: true })
  }, [busy, onCancel])

  const titleId = `${testIdPrefix}-dialog-title`
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      data-testid={`${testIdPrefix}-dialog`}
      // The backdrop also covers the Electron title bar, a window drag region that would otherwise swallow clicks there.
      style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      onClick={() => { if (!busy) onCancel() }}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        data-testid={`${testIdPrefix}-panel`}
        className="w-[420px] rounded-lg border border-border-default bg-surface-primary shadow-lg outline-none"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="border-b border-border-subtle px-4 py-3">
          <h3 id={titleId} className="text-sm font-medium text-text-primary">{title}</h3>
          <p className="mt-1 text-xs text-text-muted">{body}</p>
          {children}
        </div>
        <div className="flex justify-end gap-2 px-4 py-3">
          <button
            data-testid={`${testIdPrefix}-cancel`}
            onClick={onCancel}
            disabled={busy}
            className="px-3 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover cursor-pointer disabled:opacity-50 disabled:cursor-default"
          >
            {t('common.cancel')}
          </button>
          <button
            data-testid={`${testIdPrefix}-confirm`}
            onClick={onConfirm}
            disabled={busy || confirmDisabled}
            className="px-3 py-1 rounded-md text-xs bg-accent text-white cursor-pointer disabled:opacity-50 disabled:cursor-default flex items-center gap-1.5"
          >
            {busy && <ArrowsClockwise size={12} className="animate-spin" />}
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}

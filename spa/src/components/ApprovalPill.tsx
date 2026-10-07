// spa/src/components/ApprovalPill.tsx — what the approval dialog becomes when it is minimized (lead-team spec U22 (b)):
// a pill in this window's bottom-right corner, `● 待核准 N · m:ss` — N open requests across hosts, the countdown of the
// nearest deadline. A click restores the dialog. Rendered by ApprovalDialogHost only while `minimized`, so it is an
// app-level overlay, never inside a tab.
//
// It is not modal and never expands by itself: a request it has not shown yet raises `data-flash` by one and runs one
// background flash, nothing more. A close never flashes. The deadlines keep running in the daemon; this only shows them.
import { useEffect, useRef, useState } from 'react'
import { Circle } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { selectNearestDeadline, selectOpenCount, useApprovalStore } from '../stores/useApprovalStore'
import { formatCountdown } from '../lib/team/approval-format'

const FLASH_MS = 600

/** One background flash in the warning colour, back to the pill's own background (Web Animations; absent in jsdom). */
function flash(el: HTMLElement): void {
  if (typeof el.animate !== 'function') return
  const style = getComputedStyle(el)
  const warn = style.getPropertyValue('--color-status-warning').trim()
  el.animate([{ backgroundColor: warn }, { backgroundColor: style.backgroundColor }], { duration: FLASH_MS, easing: 'ease-out' })
}

export function ApprovalPill() {
  const t = useI18nStore((s) => s.t)
  const count = useApprovalStore(selectOpenCount)
  const nearest = useApprovalStore(selectNearestDeadline)
  const [now, setNow] = useState(() => Date.now())
  const [flashes, setFlashes] = useState(0)
  const ref = useRef<HTMLButtonElement>(null)
  // The request keys this pill has shown. What was open when it appeared is not new.
  const shown = useRef<Set<string>>(new Set())

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  useEffect(() => {
    shown.current = new Set(Object.keys(useApprovalStore.getState().entries))
    return useApprovalStore.subscribe((s, prev) => {
      if (s.entries === prev.entries) return
      const keys = Object.keys(s.entries)
      const fresh = keys.some((k) => !shown.current.has(k))
      shown.current = new Set(keys)
      if (!fresh) return
      setFlashes((n) => n + 1)
      if (ref.current) flash(ref.current)
    })
  }, [])

  if (nearest === null) return null
  return (
    <button
      ref={ref}
      type="button"
      data-testid="approval-pill"
      data-flash={flashes}
      aria-label={t('approval.pill.restore')}
      // A mouse click restores without taking the keyboard from the pane it is in: the dialog records that element
      // as where focus goes back to on the next 縮小.
      onMouseDown={(e) => e.preventDefault()}
      onClick={() => useApprovalStore.getState().setMinimized(false)}
      // Above the 24 px status bar (StatusBar `h-6`), clear of the bottom-centre undo toast.
      className="fixed bottom-8 right-3 z-50 flex items-center gap-1.5 rounded-full border border-border-default bg-surface-primary px-3 py-1 text-xs text-text-primary shadow-lg cursor-pointer hover:bg-surface-hover"
      style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
    >
      <Circle size={8} weight="fill" aria-hidden="true" className="text-status-warning" />
      <span>{t('approval.pill.label', { count, countdown: formatCountdown(nearest - now) })}</span>
    </button>
  )
}

// spa/src/components/execution/ExecutionHeader.tsx — the one-row strip above
// a worker (worker pane spec §4.7): state, the worker's name (the cwd
// basename), the cost, the two lease-backed actions, and "Take to terminal"
// (P-C.3 spec §4.4 for the session-bound path, exec-to-terminal spec §4.2 for
// every other claude execution). Observers, lease and SSE live in the dock
// (§4.6, `room/WorkerDock`); the turn count is gone. Pure presentation;
// ExecutionView owns the network and decides when the control is offered.
// The cost is an anchor button with a one-line hover summary (P-B4 spec §4.2
// H1–H2); `cost === null` means history is not loaded yet. The button is
// `aria-describedby` the tooltip, and both render the total through the same
// `formatUsd` so they can never disagree on a value. Click toggles the
// `CostPanel` (H3): the header owns the open flag and the anchor ref, and
// `FloatingPanel` handles Escape / outside-click through that ref.
// The name toggles `WorkerInfoPanel` (provider, profile, full cwd, session).
// Narrow (the root is a `@container`): at `@max-md` the header is only name +
// state + an overflow trigger (spec §4.7); the cost, the two lease-backed
// actions and "Take to terminal" (with its separator) hide, and the trigger
// opens a `FloatingPanel` carrying them; the cost panel then anchors to that
// trigger.
import { useEffect, useId, useRef, useState } from 'react'
import { ArrowUUpLeft, CurrencyDollar, DotsThree, Prohibit, Power } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { HoverTooltip } from '../HoverTooltip'
import { FloatingPanel } from '../FloatingPanel'
import CostPanel from './CostPanel'
import WorkerInfoPanel from './WorkerInfoPanel'
import type { ExecutionSummary } from '../../lib/nex/types'
import type { CostSummary } from '../../lib/nex/cost-summary'
import { formatTokens, formatUsd } from '../../lib/nex/format-cost'
import { formatDuration } from '../../lib/nex/format-duration'

export interface ExecutionHeaderProps {
  summary: ExecutionSummary | null
  /** `costSummary(messages)` once history is loaded, else null (`$…`, disabled). */
  cost: CostSummary | null
  /** Forwarded to `CostPanel` (its quota row, P-B4 P5). */
  hostId: string
  onInterrupt: () => void
  onTerminate: () => void
  /** Gates interrupt/terminate (terminal execution, or a take-back in flight). Take-back has its own flag. */
  busy: boolean
  /** Present when the execution can be taken to a terminal (ExecutionView decides). */
  onTakeBack?: () => void
  takeBackBusy?: boolean
}

const STATE_DOT: Record<string, string> = {
  running: 'bg-status-success', idle: 'bg-text-muted', queued: 'bg-status-warning',
  failed: 'bg-status-error', rejected: 'bg-status-error', terminated: 'bg-status-error',
}

const ACTION = 'flex items-center gap-1 px-2 py-0.5 rounded hover:bg-surface-hover disabled:opacity-40'
const MENU_ITEM = 'flex items-center gap-2 w-full px-2 py-1 rounded text-left hover:bg-surface-hover disabled:opacity-40'

export const TERMINATE_CONFIRM_MS = 4000

/** No layout box: `display:none` (the other side of `@md`), or not rendered. */
const boxless = (el: HTMLElement | null) => {
  const r = el?.getBoundingClientRect()
  return !r || (r.width === 0 && r.height === 0)
}

export default function ExecutionHeader({ summary, cost, hostId, onInterrupt, onTerminate, busy, onTakeBack, takeBackBusy = false }: ExecutionHeaderProps) {
  const t = useI18nStore((s) => s.t)
  const [confirming, setConfirming] = useState(false)
  const [costOpen, setCostOpen] = useState(false)
  /** Which element the cost panel hangs from: the inline button, or the overflow trigger at narrow widths. */
  const [costFromOverflow, setCostFromOverflow] = useState(false)
  const [overflowOpen, setOverflowOpen] = useState(false)
  const [infoOpen, setInfoOpen] = useState(false)
  const nameRef = useRef<HTMLButtonElement>(null)
  const costRef = useRef<HTMLButtonElement>(null)
  const overflowRef = useRef<HTMLButtonElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const costTipId = useId()
  // Crossing `@md` (a pane split, a window resize) turns the anchor of an open
  // panel `display:none`; FloatingPanel's next reflow would then read an
  // all-zero rect and pin the panel to the top-left. Watch the header's own
  // box and close any panel whose anchor has lost its box — asking the anchor
  // directly keeps the breakpoint in CSS instead of a copied pixel value.
  // The hidden anchor of a panel closed that way. FloatingPanel's unmount hands
  // focus back to it, which a browser can't do for a `display:none` element
  // (focus falls to <body>); the effect below then moves focus to a trigger
  // that is visible on this side of the breakpoint. It runs after the panel's
  // unmount cleanup (React runs every passive destroy before any create), so
  // the panel's restore can't take focus back afterwards.
  const refocusFrom = useRef<HTMLElement | null>(null)
  useEffect(() => {
    const root = rootRef.current
    if (!root || (!costOpen && !overflowOpen)) return
    const ro = new ResizeObserver(() => {
      const costAnchor = costFromOverflow ? overflowRef.current : costRef.current
      if (costOpen && boxless(costAnchor)) { refocusFrom.current = costAnchor; setCostOpen(false) }
      if (overflowOpen && boxless(overflowRef.current)) { refocusFrom.current = overflowRef.current; setOverflowOpen(false) }
    })
    ro.observe(root)
    return () => ro.disconnect()
  }, [costOpen, overflowOpen, costFromOverflow])
  useEffect(() => {
    const hidden = refocusFrom.current
    if (!hidden) return
    refocusFrom.current = null
    // Only when focus went nowhere useful (body, or the hidden anchor itself);
    // focus the user moved elsewhere while the panel was open stays put.
    const active = document.activeElement
    if (active && active !== document.body && active !== hidden) return
    const target = [costRef.current, overflowRef.current, nameRef.current]
      .find((el) => el && !el.disabled && !boxless(el))
    target?.focus()
  }, [costOpen, overflowOpen])
  useEffect(() => {
    if (!confirming) return
    const id = setTimeout(() => setConfirming(false), TERMINATE_CONFIRM_MS)
    return () => clearTimeout(id)
  }, [confirming])

  const state = summary?.state ?? '…'
  const cwdBase = summary?.cwd ? summary.cwd.split('/').filter(Boolean).pop() ?? summary.cwd : ''
  // H2: the tooltip counts what it summed (`turns.length`, not summary.turn_count).
  const costLine = cost ? t('execution.cost.summary', {
    turns: cost.turns.length, usd: formatUsd(cost.totalUsd), out: formatTokens(cost.tokens.output),
    api: formatDuration(cost.apiMs), wall: formatDuration(cost.durationMs),
  }) : ''
  const costLabel = cost ? formatUsd(cost.totalUsd, 2) : t('execution.cost.loading')
  const terminateClick = () => {
    if (confirming) { setConfirming(false); onTerminate(); return true }
    setConfirming(true)
    return false
  }

  return (
    <div ref={rootRef} className="@container flex items-center gap-2 px-4 py-2 border-b border-border-default text-xs text-text-muted">
      <span className={`shrink-0 w-2 h-2 rounded-full ${STATE_DOT[state] ?? 'bg-text-muted'}`} />
      <span data-testid="execution-state" className="shrink-0 text-text-primary font-medium">{state}</span>
      {cwdBase && (
        <button type="button" data-testid="worker-name" ref={nameRef} title={summary?.cwd}
          aria-expanded={infoOpen} onClick={() => setInfoOpen((v) => !v)}
          className="truncate min-w-0 font-medium text-text-primary hover:underline">{cwdBase}</button>
      )}
      <div className="flex-1" />
      <div data-testid="header-wide-actions" className="flex items-center gap-2 shrink-0 @max-md:hidden">
        <button type="button" data-testid="execution-cost" disabled={!cost} ref={costRef}
          aria-describedby={cost && !costOpen ? costTipId : undefined}
          aria-expanded={cost ? costOpen : undefined}
          onClick={() => { setCostFromOverflow(false); setCostOpen((v) => !v) }}
          className="relative tabular-nums hover:underline disabled:no-underline disabled:cursor-default">
          {costLabel}
          {cost && !costOpen && <HoverTooltip id={costTipId} placement="top">{costLine}</HoverTooltip>}
        </button>
        <button type="button" disabled={busy} onClick={onInterrupt} className={ACTION}>
          <Prohibit size={12} /> {t('execution.interrupt')}
        </button>
        <button type="button" disabled={busy} onClick={terminateClick} className={`${ACTION} text-status-error`}>
          <Power size={12} /> {confirming ? t('execution.terminate_confirm') : t('execution.terminate')}
        </button>
      </div>
      <button type="button" data-testid="header-overflow" ref={overflowRef}
        aria-label={t('execution.more_actions')} aria-expanded={overflowOpen}
        onClick={() => setOverflowOpen((v) => !v)}
        className={`hidden @max-md:flex shrink-0 ${ACTION}`}>
        <DotsThree size={14} />
      </button>
      {onTakeBack && (
        <div className="flex items-center gap-2 shrink-0 @max-md:hidden">
          <span className="shrink-0 w-px h-4 bg-border-subtle" />
          <button type="button" data-testid="take-back" disabled={takeBackBusy} onClick={onTakeBack} className={`shrink-0 ${ACTION}`}>
            <ArrowUUpLeft size={12} /> {t('takeback.button')}
          </button>
        </div>
      )}
      {overflowOpen && (
        <FloatingPanel title={t('execution.more_actions')} anchorRef={overflowRef} onClose={() => setOverflowOpen(false)}
          width={200} testId="header-overflow-panel">
          <div className="flex flex-col gap-0.5 text-xs text-text-primary">
            <button type="button" data-testid="overflow-cost" disabled={!cost} className={`${MENU_ITEM} tabular-nums`}
              onClick={() => { setOverflowOpen(false); setCostFromOverflow(true); setCostOpen(true) }}>
              <CurrencyDollar size={12} /> {t('execution.cost.title')} <span className="flex-1" /> {costLabel}
            </button>
            <button type="button" data-testid="overflow-interrupt" disabled={busy} className={MENU_ITEM}
              onClick={() => { setOverflowOpen(false); onInterrupt() }}>
              <Prohibit size={12} /> {t('execution.interrupt')}
            </button>
            <button type="button" data-testid="overflow-terminate" disabled={busy} className={`${MENU_ITEM} text-status-error`}
              onClick={() => { if (terminateClick()) setOverflowOpen(false) }}>
              <Power size={12} /> {confirming ? t('execution.terminate_confirm') : t('execution.terminate')}
            </button>
            {onTakeBack && (
              <>
                <div className="my-0.5 h-px bg-border-subtle" />
                <button type="button" data-testid="overflow-take-back" disabled={takeBackBusy} className={MENU_ITEM}
                  onClick={() => { setOverflowOpen(false); onTakeBack() }}>
                  <ArrowUUpLeft size={12} /> {t('takeback.button')}
                </button>
              </>
            )}
          </div>
        </FloatingPanel>
      )}
      {summary && infoOpen && <WorkerInfoPanel summary={summary} anchorRef={nameRef} onClose={() => setInfoOpen(false)} />}
      {cost && costOpen && (
        <CostPanel summary={cost} hostId={hostId} anchorRef={costFromOverflow ? overflowRef : costRef} onClose={() => setCostOpen(false)} />
      )}
    </div>
  )
}

// spa/src/components/deck/SessionRightPanel.tsx — the pane's right panel (U3 plan D10): inside the pane, on the right, 42 %
// wide (320–640 px), Esc or ✕ closes it. Whether it is open, what it shows and its scroll live in `panel-memory` (the pane
// unmounts with its tab). Whoever draws it hands the current `turns`; callers open it with `openPanel(paneKey, …)`.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from 'react'
import { ArrowLeft, X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { closePanel, openPanel, panelBack, PANEL_WIDTH_STYLE, readPanel, setPanelScroll, usePanel } from '../../lib/conversations/panel-memory'
import { panelTitle, resolvePanel, type PanelTurn } from '../../lib/conversations/panel-resolve'
import type { StepItem } from '../../lib/conversations/types'
import { PanelBody } from './PanelBody'

interface Props {
  paneKey: string
  /** The conversation the panel belongs to (host + session id); a change drops a panel opened under the old one. */
  binding: string
  turns: PanelTurn[]
  /** REQUIRED. True only for the focused pane: Esc closes the focused pane's panel and no other (a split view has several). */
  active: boolean
}

export function SessionRightPanel({ paneKey, binding, turns, active }: Props) {
  const t = useI18nStore((s) => s.t)
  const state = usePanel(paneKey, binding)
  const scroller = useRef<HTMLDivElement>(null)
  const view = useMemo(() => (state ? resolvePanel(state.content, turns) : null), [state, turns])
  const open = state !== undefined
  const turnId = state?.content.turnId

  // A panel left over from another conversation (the session changed: /clear, relay, rebuild) is not shown and is dropped.
  useEffect(() => {
    const raw = readPanel(paneKey)
    if (raw && raw.binding !== binding) closePanel(paneKey)
  }, [paneKey, binding])

  // Steps opened from inside the panel belong to the turn the panel is on.
  const actions = useMemo(() => ({
    onShowAll: (s: StepItem) => { if (turnId) openPanel(paneKey, binding, { kind: 'output', turnId, stepId: s.id }, { keepBack: true }) },
    onOpenSubagent: (s: StepItem) => { if (turnId) openPanel(paneKey, binding, { kind: 'subagent', turnId, stepId: s.id }, { keepBack: true }) },
  }), [paneKey, binding, turnId])

  // Esc closes — unless a text field has it (the input's own Esc is not ours to take).
  useEffect(() => {
    if (!open || !active) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      const el = e.target as HTMLElement | null
      if (el && (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT' || el.isContentEditable)) return
      closePanel(paneKey)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, active, paneKey])

  // Back where the reader left it: the memory's scroll, applied once the content is in.
  useLayoutEffect(() => {
    if (scroller.current && state) scroller.current.scrollTop = state.scrollTop
    // Only when the shown content changes: a live update of the same content must not yank the scroll.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state?.content, view !== null])

  const onScroll = useCallback(() => { if (scroller.current) setPanelScroll(paneKey, scroller.current.scrollTop) }, [paneKey])

  if (!state) return null
  return (
    <aside data-testid="session-right-panel" aria-label={view ? panelTitle(view, t) : t('panel.gone')}
      style={PANEL_WIDTH_STYLE} className="flex h-full shrink-0 flex-col border-l border-border-subtle bg-surface-primary">
      <div className="flex items-center gap-2 border-b border-border-subtle px-3 py-2 text-sm">
        {state.back && (
          <button type="button" data-testid="panel-back" aria-label={t('panel.back')} onClick={() => panelBack(paneKey)} className="cursor-pointer text-text-muted hover:text-text-primary">
            <ArrowLeft size={16} />
          </button>
        )}
        <span data-testid="panel-title" className="min-w-0 flex-1 truncate text-text-primary">{view ? panelTitle(view, t) : t('panel.gone')}</span>
        <button type="button" data-testid="panel-close" aria-label={t('panel.close')} onClick={() => closePanel(paneKey)} className="cursor-pointer text-text-muted hover:text-text-primary">
          <X size={16} />
        </button>
      </div>
      <div ref={scroller} data-testid="panel-scroll" onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto p-3">
        {view ? <PanelBody view={view} actions={actions} /> : <div data-testid="panel-gone" className="text-sm text-text-muted">{t('panel.gone')}</div>}
      </div>
    </aside>
  )
}

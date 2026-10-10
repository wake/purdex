// spa/src/lib/conversations/panel-memory.ts — the pane's right panel state (U3 plan D10): whether it is open, what it shows,
// and how far it was scrolled. A tab-hosted view unmounts when the tab is switched away (CLAUDE.md), so this lives here,
// keyed by pane, and the panel reads it back on mount. Content is held by REFERENCE (turn / step ids), never a copy of the
// items: the conversation keeps changing under an open panel and the panel shows the current steps. Memory only.
import { useSyncExternalStore } from 'react'

export type PanelContent =
  /** One chain (run) of a turn: the chat work-row click. `firstStepId` names the run, `turnId` its turn. */
  | { kind: 'chain'; turnId: string; firstStepId: string }
  /** A step's whole output / diff (「顯示全部」). The step is looked up in `turnId` only: a step id alone is not unique. */
  | { kind: 'output'; turnId: string; stepId: string }
  /** A subagent step's own steps (same lookup rule). */
  | { kind: 'subagent'; turnId: string; stepId: string }

export interface PanelState {
  /**
   * Which conversation the references belong to (the caller's `hostId` + session id, re-derived when the pane's session
   * changes: /clear, relay, rebuild). A state whose binding is not the live one is never shown and is dropped.
   */
  binding: string
  content: PanelContent
  /** Where the panel came from when it was opened from inside another panel view: ‹ goes back there. */
  back?: PanelContent
  scrollTop: number
}

/** The conversation a pane's memories (panel, scroll) belong to: host + session id, as the store keys it. */
export const conversationBinding = (hostId: string, sessionId: string | null | undefined): string => `${hostId}\0${sessionId ?? ''}`

export const PANEL_MIN_PX = 320
export const PANEL_MAX_PX = 640
export const PANEL_FRACTION = 0.42

/** The panel's width for a pane `containerPx` wide: 42 %, never under 320 nor over 640. */
export function panelWidth(containerPx: number): number {
  return Math.min(PANEL_MAX_PX, Math.max(PANEL_MIN_PX, Math.round(containerPx * PANEL_FRACTION)))
}
/** The same rule as CSS (the pane's width is the CSS's to know): 42 % of the flex row, clamped by min / max. */
export const PANEL_WIDTH_STYLE = { width: `${PANEL_FRACTION * 100}%`, minWidth: `${PANEL_MIN_PX}px`, maxWidth: `${PANEL_MAX_PX}px` } as const

const states = new Map<string, PanelState>()
const listeners = new Map<string, Set<() => void>>()

function emit(paneId: string): void {
  listeners.get(paneId)?.forEach((fn) => fn())
}

export function readPanel(paneId: string): PanelState | undefined {
  return states.get(paneId)
}

/** Opens (or replaces) the panel's content. Replacing from an open panel remembers where it came from. */
export function openPanel(paneId: string, binding: string, content: PanelContent, opts: { keepBack?: boolean } = {}): void {
  const prev = states.get(paneId)
  const back = opts.keepBack && prev && prev.binding === binding ? prev.content : undefined
  states.set(paneId, { binding, content, back, scrollTop: 0 })
  emit(paneId)
}

export function closePanel(paneId: string): void {
  if (states.delete(paneId)) emit(paneId)
}

/** ‹ in the header: back to the content the current one was opened from. */
export function panelBack(paneId: string): void {
  const prev = states.get(paneId)
  if (!prev?.back) return
  states.set(paneId, { binding: prev.binding, content: prev.back, scrollTop: 0 })
  emit(paneId)
}

/** Scrolling does not notify: nothing draws from it, the next mount reads it. */
export function setPanelScroll(paneId: string, scrollTop: number): void {
  const prev = states.get(paneId)
  if (prev) prev.scrollTop = scrollTop
}

export function forgetPanel(paneId: string): void {
  closePanel(paneId)
}

export function clearAllPanels(): void {
  const ids = [...states.keys()]
  states.clear()
  ids.forEach(emit)
}

function subscribe(paneId: string, fn: () => void): () => void {
  let set = listeners.get(paneId)
  if (!set) { set = new Set(); listeners.set(paneId, set) }
  set.add(fn)
  return () => {
    set.delete(fn)
    if (set.size === 0) listeners.delete(paneId)
  }
}

/** The pane's panel state, live. `openPanel` / `closePanel` replace the object, so identity comparison is enough. */
export function usePanel(paneId: string, binding: string): PanelState | undefined {
  return useSyncExternalStore((fn) => subscribe(paneId, fn), () => {
    const s = states.get(paneId)
    return s && s.binding === binding ? s : undefined
  })
}

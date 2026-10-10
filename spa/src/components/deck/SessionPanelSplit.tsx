// spa/src/components/deck/SessionPanelSplit.tsx — a session view (deck or chat) with the pane's right panel (U3 plan D10): the
// view on the left, the panel docked beside it or, in a narrow pane, laid over it (SessionSplit). The ONE place the two are put
// together, so the deck and the chat cannot grow two layouts. Whether the panel is open lives in `panel-memory`.
import { useCallback, useEffect, type ReactNode } from 'react'
import { closePanel, readPanel, usePanel } from '../../lib/conversations/panel-memory'
import type { PanelTurn } from '../../lib/conversations/panel-resolve'
import { SessionRightPanel } from './SessionRightPanel'
import { SessionSplit } from './SessionSplit'

interface Props {
  paneKey: string
  /** The conversation the panel belongs to (`conversationBinding`). */
  binding: string
  turns: PanelTurn[]
  /** True only for the focused pane: Esc closes the focused pane's panel and no other. */
  active: boolean
  children: ReactNode
  /** Test hook: the container width instead of a measurement. */
  widthOverride?: number
}

export function SessionPanelSplit({ paneKey, binding, turns, active, children, widthOverride }: Props) {
  const state = usePanel(paneKey, binding)
  // A panel left over from another conversation (/clear, relay, rebuild) is never shown; it is dropped here, where it is seen
  // whether or not a panel is mounted (SessionSplit mounts it only while open).
  useEffect(() => {
    const raw = readPanel(paneKey)
    if (raw && raw.binding !== binding) closePanel(paneKey)
  }, [paneKey, binding])
  const onClose = useCallback(() => closePanel(paneKey), [paneKey])
  return (
    <SessionSplit open={state !== undefined} onClose={onClose} escActive={active} widthOverride={widthOverride}
      panel={<SessionRightPanel paneKey={paneKey} binding={binding} turns={turns} active={active} />}>
      {children}
    </SessionSplit>
  )
}

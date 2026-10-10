// spa/src/components/deck/split-layout.ts — when the right panel may dock beside the chat (SessionSplit).
import { panelWidth } from '../../lib/conversations/panel-memory'

/** The chat never gets narrower than this beside a docked panel. */
export const CHAT_MIN_W = 360

/** Docked iff the chat keeps CHAT_MIN_W next to the panel at this container width. Unknown width (not measured yet) docks. */
export function panelDocks(containerPx: number | null): boolean {
  if (containerPx === null || containerPx <= 0) return true
  return containerPx >= CHAT_MIN_W + panelWidth(containerPx)
}

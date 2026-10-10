// spa/src/components/deck/split-layout.ts — when the right panel may dock beside the chat (SessionSplit).
import { panelWidth } from '../../lib/conversations/panel-memory'

/** The chat never gets narrower than this beside a docked panel. */
export const CHAT_MIN_W = 360

/** Docked iff the chat keeps CHAT_MIN_W next to the panel at this container width. No valid width (null, 0, negative, NaN: not measured yet) never docks, so the chat can never be squeezed under CHAT_MIN_W. */
export function panelDocks(containerPx: number | null): boolean {
  if (containerPx === null || !Number.isFinite(containerPx) || containerPx <= 0) return false
  return containerPx >= CHAT_MIN_W + panelWidth(containerPx)
}

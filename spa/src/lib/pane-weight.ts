import type { PaneLayout } from '../types/tab'
import { collectLeaves } from './pane-tree'

// "Heavy" panes are the memory/GPU-heavy renderers that keepAliveCount is meant
// to bound: tmux terminals (xterm / WebGL contexts) and browser panes (Electron
// WebContentsView). Every other pane kind (editor, previews, settings, history,
// new-tab, dashboard, …) is cheap.
const HEAVY_PANE_KINDS = new Set<string>(['tmux-session', 'browser'])

/**
 * A tab is "light" when it contains NO heavy pane. Light tabs are kept alive
 * across tab switches regardless of keepAliveCount, so their state (editor
 * scroll/mode, form inputs, …) is preserved and they never remount. keepAliveCount
 * only bounds heavy tabs.
 */
export function isLightTab(layout: PaneLayout): boolean {
  return !collectLeaves(layout).some((pane) => HEAVY_PANE_KINDS.has(pane.content.kind))
}

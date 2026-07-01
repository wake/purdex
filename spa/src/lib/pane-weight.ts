import type { PaneLayout } from '../types/tab'
import { collectLeaves } from './pane-tree'

// Explicit ALLOWLIST of "light" pane kinds — self-contained renderers that load
// once and do NO background work (no polling, streams, or fetch loops) while
// hidden, so they are safe to keep mounted across tab switches (preserving editor
// scroll/mode, etc.). Anything NOT listed is heavy and bound by keepAliveCount —
// an allowlist so new/unknown kinds default to the conservative (bounded) side.
//
// Deliberately EXCLUDED (all bounded):
//   - tmux-session / browser: GPU / WebContents memory.
//   - memory-monitor: polls metrics while mounted.
//   - hosts / editor-buffers: their own fetching.
//   - settings / new-tab: host EXTENSIBLE child components (settings sections,
//     new-tab provider cards) that can run daemon checks / streams / fetches —
//     unbounded background work if kept alive. Only self-contained, statically
//     rendered kinds qualify below.
const LIGHT_PANE_KINDS = new Set<string>([
  'editor',
  'image-preview',
  'pdf-preview',
  'dashboard',
  'history',
])

/**
 * A tab is "light" only when EVERY pane is a known-light kind. Light tabs are
 * kept alive across tab switches regardless of keepAliveCount, so they never
 * remount and their state is preserved. Any heavy (or unknown) pane makes the
 * whole tab heavy → bound by keepAliveCount.
 */
export function isLightTab(layout: PaneLayout): boolean {
  return collectLeaves(layout).every((pane) => LIGHT_PANE_KINDS.has(pane.content.kind))
}

import type { PaneLayout } from '../types/tab'
import { collectLeaves } from './pane-tree'

// Explicit ALLOWLIST of "light" pane kinds — cheap, static content that does no
// background work while hidden, so it is safe to keep mounted across tab switches
// (preserving editor scroll/mode, form inputs, …). Anything NOT listed here is
// treated as heavy and bound by keepAliveCount — an allowlist so a new/unknown
// pane kind defaults to the conservative (bounded) side. Deliberately excludes:
// tmux-session / browser (GPU/WebContents memory) and memory-monitor / hosts /
// editor-buffers (background polling / fetching that must not run unbounded for
// hidden tabs).
const LIGHT_PANE_KINDS = new Set<string>([
  'editor',
  'image-preview',
  'pdf-preview',
  'new-tab',
  'dashboard',
  'history',
  'settings',
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

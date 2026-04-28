import { createRoot, type Root } from 'react-dom/client'
import { FileNotFoundPopup } from '../../components/editor/popups/FileNotFoundPopup'
import type { PopupSpec } from './open-file'

/**
 * Singleton popup mount service for the P5 file-not-found UX.
 *
 * Why a service rather than React-tree state: callers (terminal-link openers,
 * FileTreeView entry click) live outside the React render tree and need to
 * fire the popup imperatively without lifting state through every component.
 * The service is intentionally HMR-safe and aborts any in-flight callbacks
 * on close so a user-cancelled fs.search round-trip can't re-mount a popup
 * the user just dismissed (attack review #5).
 */

let root: Root | undefined
let host: HTMLDivElement | undefined
let currentToken: AbortController | undefined

export interface ShowCallbacks {
  /** Resolved cwd of the active session (Layer 2 root); null when not available. */
  sessionCwd: string | null
  /** Resolved workspace projectPath (Layer 3 root); null when not configured. */
  projectPath: string | null
  /** Open a candidate path; service hides popup before invoking. */
  onOpenPath: (path: string) => void
  /** Trigger Layer 2 fs.search; receives the original spec + AbortSignal. */
  onSearchSessionCwd: (spec: PopupSpec, signal: AbortSignal) => void
  /** Trigger Layer 3 fs.search; receives the original spec + AbortSignal. */
  onSearchWorkspace: (spec: PopupSpec, signal: AbortSignal) => void
}

/**
 * Mount (or replace) the popup with `spec`. Returns an AbortController whose
 * signal is aborted whenever the popup is hidden or replaced — async
 * callbacks (e.g. an in-flight fs.search) MUST check `signal.aborted` after
 * await before re-rendering.
 */
export function showFileNotFoundPopup(spec: PopupSpec, cb: ShowCallbacks): AbortController {
  // Replace any current popup (also aborts the previous token).
  hideFileNotFoundPopup()

  host = document.createElement('div')
  host.dataset.pdxPopupHost = 'file-not-found'
  document.body.appendChild(host)
  root = createRoot(host)
  currentToken = new AbortController()
  const tok = currentToken

  root.render(
    <FileNotFoundPopup
      spec={spec}
      sessionCwd={cb.sessionCwd}
      projectPath={cb.projectPath}
      onClose={hideFileNotFoundPopup}
      onOpenPath={(p) => {
        // Hide first so the popup unmounts before the caller re-renders any
        // editor tabs etc.; otherwise a synchronous tab-open could race with
        // the popup's lingering ref.
        hideFileNotFoundPopup()
        cb.onOpenPath(p)
      }}
      onSearchSessionCwd={() => cb.onSearchSessionCwd(spec, tok.signal)}
      onSearchWorkspace={() => cb.onSearchWorkspace(spec, tok.signal)}
    />,
  )
  return currentToken
}

/**
 * Tear down the popup. Idempotent — safe to call multiple times. Aborts the
 * current token so awaiting callbacks observe `signal.aborted = true`.
 */
export function hideFileNotFoundPopup(): void {
  currentToken?.abort()
  root?.unmount()
  host?.remove()
  root = undefined
  host = undefined
  currentToken = undefined
}

/** Test-only alias for `hideFileNotFoundPopup` — semantic clarity in cleanup. */
export function disposeForTests(): void {
  hideFileNotFoundPopup()
}

// HMR-dispose so a hot-reload round-trip can't leave a zombie root + host
// pinned to module-level identifiers (attack review #8).
if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    hideFileNotFoundPopup()
  })
}

// Web keydown → action manifest. Mirrors the app-level subset of the Electron
// keybindings (electron/keybindings.ts) meaningful in a plain browser.
// Excludes Electron-only (new-window, browser-pane nav) AND browser-reserved
// combos (Cmd/Ctrl+T/W/Shift+T) which the browser never delivers to the page.
// `global` shortcuts fire regardless of focus (Cmd+Alt+* nav — no text-edit
// conflict); non-global fire only when focus is not an editable target.
export interface WebShortcut {
  primary: boolean
  alt?: boolean
  shift?: boolean
  key: string // single letters lowercased; named keys as-is (ArrowLeft, ',', digits)
  action: string
  global?: boolean
}

export const WEB_SHORTCUTS: readonly WebShortcut[] = [
  // Tab index (Cmd/Ctrl+digit — may be browser-reserved; non-global, focus-guarded)
  { primary: true, key: '1', action: 'switch-tab-1' },
  { primary: true, key: '2', action: 'switch-tab-2' },
  { primary: true, key: '3', action: 'switch-tab-3' },
  { primary: true, key: '4', action: 'switch-tab-4' },
  { primary: true, key: '5', action: 'switch-tab-5' },
  { primary: true, key: '6', action: 'switch-tab-6' },
  { primary: true, key: '7', action: 'switch-tab-7' },
  { primary: true, key: '8', action: 'switch-tab-8' },
  { primary: true, key: '9', action: 'switch-tab-last' },
  // Tab nav (Cmd+Alt+Left/Right — global, safe in editable/terminal)
  { primary: true, alt: true, key: 'ArrowLeft', action: 'prev-tab', global: true },
  { primary: true, alt: true, key: 'ArrowRight', action: 'next-tab', global: true },
  // App / views (non-global — Cmd+Y is Redo in inputs, so must not fire while editing)
  { primary: true, key: ',', action: 'open-settings' },
  { primary: true, shift: true, key: 'h', action: 'open-hosts' },
  { primary: true, key: 'y', action: 'open-history' },
  // Workspace nav (Cmd+Alt+* — global)
  { primary: true, alt: true, key: '0', action: 'switch-workspace-home', global: true },
  { primary: true, alt: true, key: '1', action: 'switch-workspace-1', global: true },
  { primary: true, alt: true, key: '2', action: 'switch-workspace-2', global: true },
  { primary: true, alt: true, key: '3', action: 'switch-workspace-3', global: true },
  { primary: true, alt: true, key: '4', action: 'switch-workspace-4', global: true },
  { primary: true, alt: true, key: '5', action: 'switch-workspace-5', global: true },
  { primary: true, alt: true, key: '6', action: 'switch-workspace-6', global: true },
  { primary: true, alt: true, key: '7', action: 'switch-workspace-7', global: true },
  { primary: true, alt: true, key: '8', action: 'switch-workspace-8', global: true },
  { primary: true, alt: true, key: '9', action: 'switch-workspace-9', global: true },
  { primary: true, alt: true, key: 'ArrowUp', action: 'prev-workspace', global: true },
  { primary: true, alt: true, key: 'ArrowDown', action: 'next-workspace', global: true },
]

function normalizeKey(key: string): string {
  return key.length === 1 && /[a-zA-Z]/.test(key) ? key.toLowerCase() : key
}

export function matchWebShortcut(
  e: Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>,
): WebShortcut | null {
  const primary = e.metaKey || e.ctrlKey
  if (!primary) return null
  const key = normalizeKey(e.key)
  for (const s of WEB_SHORTCUTS) {
    if (s.primary !== primary) continue
    if (!!s.alt !== e.altKey) continue
    if (!!s.shift !== e.shiftKey) continue
    if (normalizeKey(s.key) !== key) continue
    return s
  }
  return null
}

// Editable target = text input surface. xterm.js focuses a hidden
// <textarea class="xterm-helper-textarea">, so this also detects terminal focus.
export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || !!target.isContentEditable
}

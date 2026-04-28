import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { terminalLinkRegistry } from './registry'
import { urlMatcher } from './matchers/url'
import {
  createFilePathMatcher,
  ABS_RE,
  TILDE_RE,
  REL_RE,
  BARE_RE,
} from './matchers/file-path'
import { createUrlOpener } from './openers/url'
import type { UrlOpenerDeps } from './openers/url'
import { createFilePathOpener } from './openers/file-path'
import type { FilePathOpenerDeps } from './openers/file-path'

export interface BuiltinTerminalLinksDeps {
  urlOpener: UrlOpenerDeps
  filePathOpener: FilePathOpenerDeps
  /**
   * Bootstrap-time gate for ALL four file-path matchers (absolute /
   * tilde / relative_slash / bare). File-path detection only makes
   * sense when there is a file opener that can act on the click —
   * P1 gates file openers on Editor module enabled, so when Editor
   * is disabled at bootstrap a click would always fall through to
   * silent no-op (a clickable-looking link that does nothing). To
   * keep the runtime consistent with the available capability, all
   * four matchers register together. Reload-required, mirroring how
   * P1 handles file openers and other Editor-owned contributions.
   *
   * Defaults to true so existing callers and tests stay green. URL
   * matchers are independent of the file-opening capability and
   * always register.
   */
  fileMatchersEnabled?: boolean
}

// Invariant：此 flag 與 terminalLinkRegistry 的狀態必須同步。
// 清空 registry 請透過 __resetBuiltinTerminalLinks()，勿直接呼叫 terminalLinkRegistry.clear()
// 否則 flag 仍為 true，後續 registerBuiltinTerminalLinks() 會被跳過。
let registered = false

export function registerBuiltinTerminalLinks(deps: BuiltinTerminalLinksDeps): void {
  if (registered) return
  registered = true

  const fileMatchersEnabled = deps.fileMatchersEnabled !== false

  terminalLinkRegistry.registerMatcher(urlMatcher)
  if (fileMatchersEnabled) {
    terminalLinkRegistry.registerMatcher(createFilePathMatcher({
      id: 'builtin:file-path-absolute',
      regex: ABS_RE,
      isEnabled: () => useUISettingsStore.getState().linkDetectAbsolute,
    }))
    terminalLinkRegistry.registerMatcher(createFilePathMatcher({
      id: 'builtin:file-path-tilde',
      regex: TILDE_RE,
      isEnabled: () => useUISettingsStore.getState().linkDetectTilde,
    }))
    terminalLinkRegistry.registerMatcher(createFilePathMatcher({
      id: 'builtin:file-path-relative-slash',
      regex: REL_RE,
      isEnabled: () => useUISettingsStore.getState().linkDetectRelativeSlash,
    }))
    terminalLinkRegistry.registerMatcher(createFilePathMatcher({
      id: 'builtin:file-path-bare',
      regex: BARE_RE,
      isEnabled: () => useUISettingsStore.getState().linkDetectBareFilename,
    }))
  }

  terminalLinkRegistry.registerOpener(createUrlOpener(deps.urlOpener))
  terminalLinkRegistry.registerOpener(createFilePathOpener(deps.filePathOpener))
}

/** @internal 僅供測試使用；同時清空 registry 以避免 flag 與內容不同步 */
export function __resetBuiltinTerminalLinks(): void {
  registered = false
  terminalLinkRegistry.clear()
}

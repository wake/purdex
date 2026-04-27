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
   * Bootstrap-time gate for the three Editor-owned file-path matchers
   * (absolute / tilde / relative_slash). When the Editor module is
   * disabled, the migrated settings UI under Editor → File path link
   * detection is hidden (P3); registering the matchers anyway would
   * leave the runtime detecting paths that the user has no way to
   * reach the toggles for. Reload-required, mirroring how P1 handles
   * file openers and other Editor-owned contributions.
   *
   * Defaults to true so existing callers and tests stay green. The
   * bare-filename matcher is terminal-only and always registers.
   */
  editorFilePathMatchersEnabled?: boolean
}

// Invariant：此 flag 與 terminalLinkRegistry 的狀態必須同步。
// 清空 registry 請透過 __resetBuiltinTerminalLinks()，勿直接呼叫 terminalLinkRegistry.clear()
// 否則 flag 仍為 true，後續 registerBuiltinTerminalLinks() 會被跳過。
let registered = false

export function registerBuiltinTerminalLinks(deps: BuiltinTerminalLinksDeps): void {
  if (registered) return
  registered = true

  const editorMatchersEnabled = deps.editorFilePathMatchersEnabled !== false

  terminalLinkRegistry.registerMatcher(urlMatcher)
  if (editorMatchersEnabled) {
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
  }
  terminalLinkRegistry.registerMatcher(createFilePathMatcher({
    id: 'builtin:file-path-bare',
    regex: BARE_RE,
    isEnabled: () => useUISettingsStore.getState().linkDetectBareFilename,
  }))

  terminalLinkRegistry.registerOpener(createUrlOpener(deps.urlOpener))
  terminalLinkRegistry.registerOpener(createFilePathOpener(deps.filePathOpener))
}

/** @internal 僅供測試使用；同時清空 registry 以避免 flag 與內容不同步 */
export function __resetBuiltinTerminalLinks(): void {
  registered = false
  terminalLinkRegistry.clear()
}

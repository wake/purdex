import type { FileInfo } from '../../types/fs'
import type { PaneContent } from '../../types/tab'
import type { FileOpener } from '../file-opener-registry'
import { terminalLinkRegistry } from './registry'
import { urlMatcher } from './matchers/url'
import { filePathMatcher } from './matchers/file-path'
import { createUrlOpener } from './openers/url'
import { createFilePathOpener } from './openers/file-path'

export interface BuiltinTerminalLinksDeps {
  isElectron: boolean
  openBrowserTab: (url: string) => void
  openMiniWindow: (url: string) => void
  getDefaultFileOpener: (file: FileInfo) => FileOpener | null
  openSingletonTab: (content: PaneContent) => string
  insertTab: (tabId: string, wsId: string) => void
  getActiveWorkspaceId: () => string | null
}

// Invariant：此 flag 與 terminalLinkRegistry 的狀態必須同步。
// 清空 registry 請透過 __resetBuiltinTerminalLinks()，勿直接呼叫 terminalLinkRegistry.clear()
// 否則 flag 仍為 true，後續 registerBuiltinTerminalLinks() 會被跳過。
let registered = false

export function registerBuiltinTerminalLinks(deps: BuiltinTerminalLinksDeps): void {
  if (registered) return
  registered = true

  terminalLinkRegistry.registerMatcher(urlMatcher)
  terminalLinkRegistry.registerMatcher(filePathMatcher)

  terminalLinkRegistry.registerOpener(createUrlOpener({
    isElectron: deps.isElectron,
    openBrowserTab: deps.openBrowserTab,
    openMiniWindow: deps.openMiniWindow,
  }))
  terminalLinkRegistry.registerOpener(createFilePathOpener({
    getDefaultOpener: deps.getDefaultFileOpener,
    openSingletonTab: deps.openSingletonTab,
    insertTab: deps.insertTab,
    getActiveWorkspaceId: deps.getActiveWorkspaceId,
  }))
}

/** @internal 僅供測試使用；同時清空 registry 以避免 flag 與內容不同步 */
export function __resetBuiltinTerminalLinks(): void {
  registered = false
  terminalLinkRegistry.clear()
}

import type { LinkOpener } from '../types'
import type { FileInfo, FileSource } from '../../../types/fs'
import type { PaneContent } from '../../../types/tab'
import type { FileOpener } from '../../file-opener-registry'

export interface FilePathOpenerDeps {
  getDefaultOpener(file: FileInfo): FileOpener | null
  openSingletonTab(content: PaneContent): string
  insertTab(tabId: string, workspaceId: string): void
  getActiveWorkspaceId(): string | null
}

function buildFileInfo(path: string): FileInfo {
  const name = path.split('/').pop() ?? path
  const extension = name.includes('.') ? name.split('.').pop()! : ''
  return { name, path, extension, size: 0, isDirectory: false }
}

export function createFilePathOpener(deps: FilePathOpenerDeps): LinkOpener {
  return {
    id: 'builtin:file-path',
    // priority 0 = builtin default，讓第三方 opener 以更高 priority 覆寫
    priority: 0,
    canOpen: (token) =>
      token.type === 'file' &&
      typeof (token.meta as { path?: unknown } | undefined)?.path === 'string',
    open: (token, ctx) => {
      // 不依賴呼叫方一定先走 canOpen，自行檢查 meta.path
      const path = (token.meta as { path?: unknown } | undefined)?.path
      if (typeof path !== 'string') return
      if (!ctx.hostId) return
      const file = buildFileInfo(path)
      const opener = deps.getDefaultOpener(file)
      if (!opener) return
      const source: FileSource = { type: 'daemon', hostId: ctx.hostId }
      const content = opener.createContent(source, file)
      const wsId = deps.getActiveWorkspaceId()
      if (!wsId) return
      const tabId = deps.openSingletonTab(content)
      deps.insertTab(tabId, wsId)
    },
  }
}

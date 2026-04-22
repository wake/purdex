import type { LinkOpener } from '../types'
import type { FileInfo, FileSource } from '../../../types/fs'
import type { PaneContent } from '../../../types/tab'
import type { FileOpener } from '../../file-opener-registry'
import { resolveEditorHomePath } from '../../editor-home-resolver'

export interface FilePathOpenerDeps {
  getDefaultOpener(file: FileInfo): FileOpener | null
  openSingletonTab(content: PaneContent): string
  insertTab(tabId: string, workspaceId: string): void
  getActiveWorkspaceId(): string | null
  fetchPaneCwd(hostId: string, sessionCode: string, signal?: AbortSignal): Promise<string>
  fetchPaneHome(hostId: string, sessionCode: string, signal?: AbortSignal): Promise<string>
}

function buildFileInfo(path: string): FileInfo {
  const name = path.split('/').pop() ?? path
  const extension = name.includes('.') ? name.split('.').pop()! : ''
  return { name, path, extension, size: 0, isDirectory: false }
}

// Collapse `.` and `..` segments in an absolute path. Used by the tilde
// branch so that `~/./foo` and `~/../foo` resolve to single canonical paths,
// avoiding duplicate editor tabs for the same physical file.
function normalizeAbsPath(abs: string): string {
  const parts = abs.split('/')
  const stack: string[] = []
  for (const part of parts) {
    if (part === '' || part === '.') continue
    if (part === '..') {
      if (stack.length > 0) stack.pop()
      continue
    }
    stack.push(part)
  }
  return '/' + stack.join('/')
}

/**
 * Resolve a relative path against `cwd`, normalizing `.` and `..` segments.
 * Returns `null` if the resolved path escapes `cwd`'s parent directory
 * (i.e. moves more than one level above `cwd`), so the caller can reject
 * path-traversal attempts like `../../../etc/passwd`.
 *
 * Design: allow at most one `..` out of the fetched cwd (common in relative
 * editor paths like `../App.tsx`), but reject deep traversals that escape
 * the cwd's parent entirely.
 */
export function resolveCwdPath(cwd: string, rel: string): string | null {
  const base = cwd.replace(/\/+$/, '')  // strip trailing slashes
  const joined = `${base}/${rel}`
  const parts = joined.split('/')
  const stack: string[] = []
  for (const part of parts) {
    if (part === '' || part === '.') continue
    if (part === '..') {
      if (stack.length > 0) stack.pop()
      continue
    }
    stack.push(part)
  }
  const normalized = '/' + stack.join('/')

  // Boundary: normalized must be within cwd's parent directory.
  // This allows one level of ".." escape (e.g. ../App.tsx from a subdir)
  // but rejects deep traversals that land outside the parent tree.
  const parentIdx = base.lastIndexOf('/')
  // parentIdx === 0 means cwd is /foo — parent is root "/"
  // parentIdx < 0 should not happen since cwd must start with /
  const baseParent = parentIdx <= 0 ? '' : base.slice(0, parentIdx)
  // normalized must equal baseParent, or be nested under it
  if (baseParent === '') {
    // parent is root: any absolute path is within root
    return normalized
  }
  if (normalized !== baseParent && !normalized.startsWith(baseParent + '/')) {
    return null
  }
  return normalized
}

export function createFilePathOpener(deps: FilePathOpenerDeps): LinkOpener {
  return {
    id: 'builtin:file-path',
    priority: 0,
    canOpen: (token) =>
      token.type === 'file' &&
      typeof (token.meta as { path?: unknown } | undefined)?.path === 'string',
    open: async (token, ctx) => {
      const rawPath = (token.meta as { path?: unknown } | undefined)?.path
      if (typeof rawPath !== 'string') return
      if (!ctx.hostId) return

      let path = rawPath
      if (path.startsWith('~/')) {
        if (!ctx.sessionCode) return
        const controller = new AbortController()
        const timeoutId = setTimeout(() => controller.abort(), 5000)
        let home: string | null = null
        try {
          home = await resolveEditorHomePath(
            {
              hostId: ctx.hostId,
              sessionCode: ctx.sessionCode,
              workspaceId: ctx.workspaceId,
              signal: controller.signal,
            },
            { fetchPaneHome: deps.fetchPaneHome },
          )
        } catch (err) {
          const reason = (err as Error)?.name === 'AbortError' ? 'timeout' : (err as Error)?.message ?? String(err)
          console.warn(`[file-path] home resolve failed (${reason}) for host=${ctx.hostId} session=${ctx.sessionCode} path=${rawPath}; opening as new buffer`)
        } finally {
          clearTimeout(timeoutId)
        }
        if (home && home.startsWith('/')) {
          path = normalizeAbsPath(home.replace(/\/+$/, '') + path.slice(1))
        }
        // else: leave path as rawPath (~/foo) so Editor opens a blank buffer
      } else if (!path.startsWith('/')) {
        // relative / bare: 即時向 tmux pane 查 cwd，normalize 後驗證不越界
        if (!ctx.sessionCode) return
        const controller = new AbortController()
        const timeoutId = setTimeout(() => controller.abort(), 5000)
        let cwd: string
        try {
          cwd = await deps.fetchPaneCwd(ctx.hostId, ctx.sessionCode, controller.signal)
        } catch (err) {
          clearTimeout(timeoutId)
          const reason = (err as Error)?.name === 'AbortError' ? 'timeout' : (err as Error)?.message ?? String(err)
          console.warn(`[file-path] cwd fetch failed (${reason}) for host=${ctx.hostId} session=${ctx.sessionCode} rel=${rawPath}`)
          return
        }
        clearTimeout(timeoutId)
        if (!cwd || !cwd.startsWith('/')) {
          console.warn(`[file-path] cwd not absolute, skipping: host=${ctx.hostId} cwd=${cwd}`)
          return
        }
        const resolved = resolveCwdPath(cwd, path)
        if (resolved === null) {
          console.warn(`[file-path] rejected path escaping cwd: ${path} vs ${cwd}`)
          return
        }
        path = resolved
      }

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

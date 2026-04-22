import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useWorkspaceSettingsStore } from '../stores/useWorkspaceSettingsStore'

export interface EditorHomeResolverCtx {
  hostId?: string
  sessionCode?: string
  workspaceId?: string
  signal?: AbortSignal
}

export interface EditorHomeResolverDeps {
  fetchPaneHome(hostId: string, sessionCode: string, signal?: AbortSignal): Promise<string>
}

function readAbsoluteHomePath(payload: Record<string, unknown> | undefined): string | null {
  if (!payload) return null
  const value = payload.homePath
  if (typeof value !== 'string') return null
  if (!value.startsWith('/')) return null
  return value
}

/**
 * Resolve the "home" path Editor should use when opening a tilde-prefixed
 * terminal link. Layered fallback: workspace settings → host settings →
 * pane shell (`fetchPaneHome`). Returns null when no layer yields a value
 * or the request has been aborted.
 *
 * Callers must pass the link-source `workspaceId` (from
 * `LinkContext.workspaceId`), not active workspace id. `workspaceId` may be
 * undefined for panes not attached to any workspace — the workspace layer
 * is skipped in that case.
 */
export async function resolveEditorHomePath(
  ctx: EditorHomeResolverCtx,
  deps: EditorHomeResolverDeps,
): Promise<string | null> {
  if (ctx.signal?.aborted) return null
  if (!ctx.hostId) return null

  if (ctx.workspaceId) {
    const payload = useWorkspaceSettingsStore.getState().get(ctx.workspaceId, 'editor')
    const ws = readAbsoluteHomePath(payload)
    if (ws) return ws
  }

  const hostPayload = useHostSettingsStore.getState().get(ctx.hostId, 'editor')
  const host = readAbsoluteHomePath(hostPayload)
  if (host) return host

  if (!ctx.sessionCode) return null
  if (ctx.signal?.aborted) return null
  const shell = await deps.fetchPaneHome(ctx.hostId, ctx.sessionCode, ctx.signal)
  if (shell && shell.startsWith('/')) return shell
  return null
}

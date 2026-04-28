import { useHostStore } from '../../stores/useHostStore'

/**
 * Capability-only search-root references.
 *
 * The daemon resolves the capability into an actual absolute path; client
 * code MUST NOT supply absolute paths directly — the daemon's server-side
 * allowlist (P5 spec §"fs.search root allowlist") rejects `kind:"absolute"`.
 *
 * `workspace-projectPath` is currently a v6 soft-degrade: the daemon accepts
 * the schema but responds 501 (no workspace registry yet). Layer-3 callers
 * MUST treat 501 as "layer-3 not available" rather than as an error.
 */
export type SearchRootCapability =
  | { kind: 'session-cwd'; sessionCode: string }
  | { kind: 'workspace-projectPath'; workspaceId: string }

export interface SearchMatch {
  path: string
  modTime: string
  sizeBytes: number
  root: string
}

export interface SearchResponse {
  matches: SearchMatch[]
  partial: boolean
  warnings?: string[]
}

export interface SearchLimits {
  maxResults?: number
  maxDepth?: number
  timeoutMs?: number
}

/** Error thrown when daemon's fs.search returns a non-2xx response. */
export class FsSearchError extends Error {
  readonly status: number
  constructor(status: number, statusText: string, body?: string) {
    super(`fs.search failed: ${status} ${statusText}${body ? ` — ${body}` : ''}`)
    this.status = status
    this.name = 'FsSearchError'
  }
}

/**
 * Call `POST {hostBaseUrl}/api/fs/search` with capability roots and a basename
 * query. Returns matches sorted by `modTime` desc.
 *
 * The body uses the daemon's "mode envelope" shape:
 *   { mode: "basename", query: { basename }, roots, limits?, filters? }
 * leaving room for future `mode: "fuzzy" | "content"` extensions.
 */
export async function fsSearchByCapability(
  hostId: string,
  basename: string,
  roots: SearchRootCapability[],
  limits?: SearchLimits,
  signal?: AbortSignal,
): Promise<SearchMatch[]> {
  const state = useHostStore.getState()
  const base = state.getDaemonBase(hostId)
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...state.getAuthHeaders(hostId),
  }
  const body: Record<string, unknown> = {
    mode: 'basename',
    query: { basename },
    roots,
  }
  if (limits && Object.keys(limits).length > 0) {
    body.limits = limits
  }
  // R2-M1: thread the popup's AbortSignal so dismissing the popup actually
  // tears down the in-flight HTTP request (and the daemon-side WalkDir).
  // Without this, closing the popup left the request running until the
  // daemon completed its scan, even though the SPA-side `if (signal.aborted)
  // return` already prevented the result from being merged.
  const res = await fetch(`${base}/api/fs/search`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) {
    let text = ''
    try {
      text = await res.text()
    } catch {
      // ignore — body is best-effort context for the error message
    }
    throw new FsSearchError(res.status, res.statusText, text)
  }
  const json = (await res.json()) as SearchResponse
  // Sort matches by modTime DESC (newest first). Strings are ISO-8601, so
  // string comparison is sufficient.
  const matches = [...(json.matches ?? [])].sort((a, b) => {
    if (a.modTime === b.modTime) return 0
    return a.modTime < b.modTime ? 1 : -1
  })
  return matches
}

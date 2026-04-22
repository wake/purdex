/**
 * Link range within a terminal buffer line.
 *
 * Two different coordinate systems use this type:
 * - Matcher output (`LinkMatcher.provide` return): `startCol` / `endCol` are
 *   **JS UTF-16 string offsets** into the line text (0-indexed, end-exclusive).
 *   Matchers operate on the plain string from `translateToString` and don't
 *   see cell widths.
 * - `LinkToken.range` dispatched to openers: `startCol` / `endCol` are
 *   **terminal cell columns** (0-indexed, end-exclusive). The xterm provider
 *   converts JS offsets to cell columns via `buildJsOffsetToCol` so CJK/emoji
 *   wide chars are accounted for.
 *
 * Keep this in mind if you read `token.range` from an opener: values reflect
 * terminal cells, not JS offsets.
 */
export interface LinkRange {
  startCol: number
  endCol: number
}

export interface LinkToken {
  type: string
  text: string
  range: LinkRange  // cell columns (post-conversion); see LinkRange doc
  meta?: Record<string, unknown>
}

/**
 * Context injected when a terminal link is dispatched to an opener.
 *
 * - `workspaceId` must be the workspace that **owns the link-source pane**,
 *   not `activeWorkspaceId`. For terminal link usage it is resolved by
 *   `SessionPaneContent` via `useWorkspaceStore.findWorkspaceByTab(tabId)`
 *   and passed into `TerminalView` as a prop. Panes not attached to any
 *   workspace (standalone tabs) leave this `undefined`.
 */
export interface LinkContext {
  hostId?: string
  sessionCode?: string
  workspaceId?: string
}

export interface LinkMatcher {
  id: string
  type: string
  provide(line: string): Array<{
    text: string
    range: LinkRange
    meta?: Record<string, unknown>
  }>
}

export interface LinkOpener {
  id: string
  priority?: number  // higher runs first; default 0
  canOpen(token: LinkToken): boolean
  open(token: LinkToken, ctx: LinkContext, event: MouseEvent): void | Promise<void>
}

export interface TerminalLinkRegistry {
  registerMatcher(m: LinkMatcher): () => void
  registerOpener(o: LinkOpener): () => void
  getMatchers(): readonly LinkMatcher[]
  dispatch(token: LinkToken, ctx: LinkContext, event: MouseEvent): boolean
  clear(): void
}

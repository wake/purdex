export interface LinkRange {
  startCol: number  // 0-indexed inclusive
  endCol: number    // 0-indexed exclusive
}

export interface LinkToken {
  type: string
  text: string
  range: LinkRange
  meta?: Record<string, unknown>
}

export interface LinkContext {
  hostId?: string
  sessionCode?: string
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

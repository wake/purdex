import type { LinkMatcher, LinkOpener, LinkToken, LinkContext, TerminalLinkRegistry } from './types'

export function createRegistry(): TerminalLinkRegistry {
  const matchers: LinkMatcher[] = []
  const openers: LinkOpener[] = []

  return {
    registerMatcher(m) {
      matchers.push(m)
      return () => {
        const i = matchers.indexOf(m)
        if (i >= 0) matchers.splice(i, 1)
      }
    },
    registerOpener(o) {
      openers.push(o)
      return () => {
        const i = openers.indexOf(o)
        if (i >= 0) openers.splice(i, 1)
      }
    },
    getMatchers() {
      return matchers
    },
    dispatch(token, ctx, event) {
      const sorted = [...openers].sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0))
      for (const o of sorted) {
        if (o.canOpen(token)) {
          void o.open(token, ctx, event)
          return true
        }
      }
      return false
    },
    clear() {
      matchers.length = 0
      openers.length = 0
    },
  }
}

// 全域 singleton — 供 boot 時註冊內建 matcher/opener
export const terminalLinkRegistry: TerminalLinkRegistry = createRegistry()

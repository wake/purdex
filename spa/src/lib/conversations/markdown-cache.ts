// spa/src/lib/conversations/markdown-cache.ts — the rendered markdown of finished agent text, kept across a deck's remounts
// (#2469). The deck is tab-hosted and unmounts on a tab switch; coming back used to parse and highlight every message again.
// A finished item's markdown never changes, so the React tree react-markdown produced for it is reused as it is. Memory only,
// bounded, least recently used out first. An item still streaming is never put here (its text changes every few ms).
import type { ReactNode } from 'react'

export const MARKDOWN_CACHE_LIMIT = 500

const cache = new Map<string, { content: string; node: ReactNode }>()

/** The rendered `content` of item `key`: the kept one when the text is the same, else `render()`'s, which is then kept. */
export function cachedMarkdown(key: string, content: string, render: () => ReactNode): ReactNode {
  const hit = cache.get(key)
  if (hit && hit.content === content) {
    cache.delete(key)
    cache.set(key, hit)
    return hit.node
  }
  const node = render()
  cache.delete(key)
  cache.set(key, { content, node })
  if (cache.size > MARKDOWN_CACHE_LIMIT) cache.delete(cache.keys().next().value as string)
  return node
}

export function markdownCacheSize(): number {
  return cache.size
}

export function clearMarkdownCache(): void {
  cache.clear()
}

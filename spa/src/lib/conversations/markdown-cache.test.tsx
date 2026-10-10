import { describe, it, expect, beforeEach, vi } from 'vitest'
import { cachedMarkdown, clearMarkdownCache, markdownCacheSize, MARKDOWN_CACHE_LIMIT } from './markdown-cache'

beforeEach(() => clearMarkdownCache())

describe('markdown cache', () => {
  it('hands back the first render for the same id and the same text', () => {
    const render = vi.fn(() => 'node')
    expect(cachedMarkdown('a', 'text', render)).toBe('node')
    expect(cachedMarkdown('a', 'text', render)).toBe('node')
    expect(render).toHaveBeenCalledTimes(1)
  })

  it('renders again when the text under the same id changed', () => {
    const render = vi.fn(() => ({}))
    const first = cachedMarkdown('a', 'one', render)
    const second = cachedMarkdown('a', 'two', render)
    expect(second).not.toBe(first)
    expect(render).toHaveBeenCalledTimes(2)
    // the newer text is what is kept
    expect(cachedMarkdown('a', 'two', render)).toBe(second)
    expect(render).toHaveBeenCalledTimes(2)
  })

  it('keeps at most the limit, dropping the least recently used first', () => {
    for (let i = 0; i < MARKDOWN_CACHE_LIMIT; i++) cachedMarkdown(`k${i}`, 'x', () => i)
    expect(markdownCacheSize()).toBe(MARKDOWN_CACHE_LIMIT)
    // touching k0 makes k1 the oldest
    cachedMarkdown('k0', 'x', () => 'never')
    cachedMarkdown('new', 'x', () => 'new')
    expect(markdownCacheSize()).toBe(MARKDOWN_CACHE_LIMIT)
    const render = vi.fn(() => 'again')
    cachedMarkdown('k1', 'x', render)
    expect(render).toHaveBeenCalledTimes(1) // k1 was evicted
    const kept = vi.fn(() => 'again')
    cachedMarkdown('k0', 'x', kept)
    expect(kept).not.toHaveBeenCalled() // k0 survived (was touched)
  })
})

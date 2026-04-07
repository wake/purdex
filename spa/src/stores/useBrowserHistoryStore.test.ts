import { describe, it, expect, beforeEach } from 'vitest'
import { useBrowserHistoryStore } from './useBrowserHistoryStore'

describe('useBrowserHistoryStore', () => {
  beforeEach(() => {
    useBrowserHistoryStore.setState({ urls: [] })
  })

  it('addUrl adds to head', () => {
    useBrowserHistoryStore.getState().addUrl('https://example.com')
    expect(useBrowserHistoryStore.getState().urls[0]).toBe('https://example.com')
  })

  it('addUrl deduplicates (moves existing to head)', () => {
    useBrowserHistoryStore.getState().addUrl('https://a.com')
    useBrowserHistoryStore.getState().addUrl('https://b.com')
    useBrowserHistoryStore.getState().addUrl('https://a.com')
    const urls = useBrowserHistoryStore.getState().urls
    expect(urls).toEqual(['https://a.com', 'https://b.com'])
  })

  it('addUrl caps at 100 entries', () => {
    for (let i = 0; i < 110; i++) {
      useBrowserHistoryStore.getState().addUrl(`https://${i}.com`)
    }
    expect(useBrowserHistoryStore.getState().urls).toHaveLength(100)
    expect(useBrowserHistoryStore.getState().urls[0]).toBe('https://109.com')
  })
})

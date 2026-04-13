import { describe, it, expect, vi, beforeEach } from 'vitest'

let prefetchWeight: typeof import('./icon-path-cache').prefetchWeight
let getIconPath: typeof import('./icon-path-cache').getIconPath
let isWeightLoaded: typeof import('./icon-path-cache').isWeightLoaded

beforeEach(async () => {
  vi.restoreAllMocks()
  vi.resetModules()
  const mod = await import('./icon-path-cache')
  prefetchWeight = mod.prefetchWeight
  getIconPath = mod.getIconPath
  isWeightLoaded = mod.isWeightLoaded
})

describe('icon-path-cache', () => {
  it('fetches and caches weight data', async () => {
    const mockData = { Acorn: 'M0,0L10,10', Terminal: 'M5,5L20,20' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(mockData), { status: 200 }),
    )

    await prefetchWeight('bold')
    expect(fetch).toHaveBeenCalledWith('/icons/bold.json')
    expect(isWeightLoaded('bold')).toBe(true)
    expect(getIconPath('Acorn', 'bold')).toBe('M0,0L10,10')
    expect(getIconPath('Unknown', 'bold')).toBeNull()
  })

  it('returns null for uncached weight', () => {
    expect(isWeightLoaded('thin')).toBe(false)
    expect(getIconPath('Acorn', 'thin')).toBeNull()
  })

  it('does not fetch again if already cached', async () => {
    const mockData = { Acorn: 'M0,0' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(mockData), { status: 200 }),
    )

    await prefetchWeight('bold')
    await prefetchWeight('bold')
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('deduplicates concurrent fetches for the same weight', async () => {
    const mockData = { Acorn: 'M0,0' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(mockData), { status: 200 }),
    )

    await Promise.all([prefetchWeight('bold'), prefetchWeight('bold')])
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('throws on fetch failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('Not Found', { status: 404 }),
    )

    await expect(prefetchWeight('bold')).rejects.toThrow('Failed to fetch icon weight "bold": 404')
  })

  it('allows retry after fetch failure', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response('Error', { status: 500 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ Acorn: 'M0,0' }), { status: 200 }))

    await expect(prefetchWeight('bold')).rejects.toThrow()
    expect(isWeightLoaded('bold')).toBe(false)

    await prefetchWeight('bold')
    expect(isWeightLoaded('bold')).toBe(true)
  })
})

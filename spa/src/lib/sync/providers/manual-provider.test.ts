import { describe, it, expect } from 'vitest'
import { createManualProvider, ImportError } from './manual-provider'
import type { SyncBundle } from '../types'

const makeBundle = (): SyncBundle => ({
  version: 1,
  timestamp: 1000000,
  device: 'test-device',
  collections: {},
})

describe('ManualProvider', () => {
  it('has id "manual"', () => {
    const provider = createManualProvider()
    expect(provider.id).toBe('manual')
  })

  it('exportToBlob serializes bundle to JSON blob with correct type', () => {
    const provider = createManualProvider()
    const bundle = makeBundle()
    const blob = provider.exportToBlob(bundle)
    expect(blob).toBeInstanceOf(Blob)
    expect(blob.type).toBe('application/json')
    return blob.text().then(text => {
      const parsed = JSON.parse(text)
      expect(parsed).toEqual(bundle)
      // pretty-print check: should contain newlines
      expect(text).toContain('\n')
    })
  })

  it('importFromText parses JSON back to SyncBundle', () => {
    const provider = createManualProvider()
    const bundle = makeBundle()
    const text = JSON.stringify(bundle, null, 2)
    const result = provider.importFromText(text)
    expect(result).toEqual(bundle)
  })

  it('importFromText throws ImportError(invalid-json) on malformed JSON', () => {
    const provider = createManualProvider()
    try {
      provider.importFromText('not valid json{')
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(ImportError)
      expect((e as ImportError).code).toBe('invalid-json')
    }
  })

  it('importFromText throws ImportError(invalid-shape) when version missing', () => {
    const provider = createManualProvider()
    const bad = { timestamp: 1000000, device: 'x', collections: {} }
    try {
      provider.importFromText(JSON.stringify(bad))
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(ImportError)
      expect((e as ImportError).code).toBe('invalid-shape')
      expect((e as ImportError).message).toMatch(/version/)
    }
  })

  it('importFromText throws ImportError(invalid-shape) when version is not number', () => {
    const provider = createManualProvider()
    const bad = { version: 'one', timestamp: 1000000, device: 'x', collections: {} }
    try {
      provider.importFromText(JSON.stringify(bad))
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(ImportError)
      expect((e as ImportError).code).toBe('invalid-shape')
      expect((e as ImportError).message).toMatch(/version/)
    }
  })

  it('importFromText throws ImportError(invalid-shape) when timestamp is not number', () => {
    const provider = createManualProvider()
    const bad = { version: 1, timestamp: 'now', device: 'x', collections: {} }
    try {
      provider.importFromText(JSON.stringify(bad))
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(ImportError)
      expect((e as ImportError).code).toBe('invalid-shape')
      expect((e as ImportError).message).toMatch(/timestamp/)
    }
  })

  it('importFromText throws ImportError(invalid-shape) when device is not string', () => {
    const provider = createManualProvider()
    const bad = { version: 1, timestamp: 1000000, device: 42, collections: {} }
    try {
      provider.importFromText(JSON.stringify(bad))
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(ImportError)
      expect((e as ImportError).code).toBe('invalid-shape')
      expect((e as ImportError).message).toMatch(/device/)
    }
  })

  it('importFromText throws ImportError(invalid-shape) when collections is not object', () => {
    const provider = createManualProvider()
    const bad = { version: 1, timestamp: 1000000, device: 'x', collections: 'bad' }
    try {
      provider.importFromText(JSON.stringify(bad))
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(ImportError)
      expect((e as ImportError).code).toBe('invalid-shape')
      expect((e as ImportError).message).toMatch(/collections/)
    }
  })

  it('importFromText throws ImportError(too-large) when text exceeds 5 MB', () => {
    const provider = createManualProvider()
    // 5 MB + 1 char
    const huge = '"' + 'a'.repeat(5 * 1024 * 1024) + '"'
    try {
      provider.importFromText(huge)
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(ImportError)
      expect((e as ImportError).code).toBe('too-large')
    }
  })

  it('importFromText throws ImportError(too-deep) when object depth exceeds 32', () => {
    const provider = createManualProvider()
    // Build deep object: { a: { a: { ... } } } 40 levels rooted at collections
    const deepCollections: Record<string, unknown> = {}
    let cursor: Record<string, unknown> = deepCollections
    for (let i = 0; i < 40; i++) {
      const next: Record<string, unknown> = {}
      cursor['deep'] = next
      cursor = next
    }
    const root: SyncBundle = {
      version: 1,
      timestamp: 1,
      device: 'x',
      collections: deepCollections as SyncBundle['collections'],
    }
    try {
      provider.importFromText(JSON.stringify(root))
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(ImportError)
      expect((e as ImportError).code).toBe('too-deep')
    }
  })

  it('importFromText accepts 4 MB payload below size limit', () => {
    const provider = createManualProvider()
    const bundle: SyncBundle = {
      version: 1,
      timestamp: 1,
      device: 'x',
      collections: { big: { version: 1, data: { blob: 'a'.repeat(4 * 1024 * 1024) } } } as SyncBundle['collections'],
    }
    const text = JSON.stringify(bundle)
    expect(text.length).toBeLessThan(5 * 1024 * 1024)
    const result = provider.importFromText(text)
    expect(result.device).toBe('x')
  })

  it('listHistory returns empty array', async () => {
    const provider = createManualProvider()
    const result = await provider.listHistory(10)
    expect(result).toEqual([])
  })

  it('push is a no-op and does not throw', async () => {
    const provider = createManualProvider()
    await expect(provider.push(makeBundle())).resolves.toBeUndefined()
  })

  it('pull is a no-op and returns null', async () => {
    const provider = createManualProvider()
    await expect(provider.pull()).resolves.toBeNull()
  })

  it('pushChunks is a no-op and does not throw', async () => {
    const provider = createManualProvider()
    await expect(provider.pushChunks({})).resolves.toBeUndefined()
  })

  it('pullChunks is a no-op and returns empty object', async () => {
    const provider = createManualProvider()
    await expect(provider.pullChunks(['hash1'])).resolves.toEqual({})
  })
})

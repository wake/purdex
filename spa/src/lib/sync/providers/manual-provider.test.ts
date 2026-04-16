import { describe, it, expect } from 'vitest'
import { createManualProvider } from './manual-provider'
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

  it('importFromText throws on invalid JSON', () => {
    const provider = createManualProvider()
    expect(() => provider.importFromText('not valid json{')).toThrow()
  })

  it('importFromText throws on missing version field', () => {
    const provider = createManualProvider()
    const bad = { timestamp: 1000000, device: 'x', collections: {} }
    expect(() => provider.importFromText(JSON.stringify(bad))).toThrow(/version/)
  })

  it('importFromText throws when version is not a number', () => {
    const provider = createManualProvider()
    const bad = { version: 'one', timestamp: 1000000, device: 'x', collections: {} }
    expect(() => provider.importFromText(JSON.stringify(bad))).toThrow(/version/)
  })

  it('importFromText throws when timestamp is not a number', () => {
    const provider = createManualProvider()
    const bad = { version: 1, timestamp: 'now', device: 'x', collections: {} }
    expect(() => provider.importFromText(JSON.stringify(bad))).toThrow(/timestamp/)
  })

  it('importFromText throws when device is not a string', () => {
    const provider = createManualProvider()
    const bad = { version: 1, timestamp: 1000000, device: 42, collections: {} }
    expect(() => provider.importFromText(JSON.stringify(bad))).toThrow(/device/)
  })

  it('importFromText throws when collections is not an object', () => {
    const provider = createManualProvider()
    const bad = { version: 1, timestamp: 1000000, device: 'x', collections: 'bad' }
    expect(() => provider.importFromText(JSON.stringify(bad))).toThrow(/collections/)
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

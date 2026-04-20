import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it } from 'vitest'
import { openEditorDb } from '../db'
import { EditorContentRepository } from '../content-repository'

describe('EditorContentRepository', () => {
  beforeEach(async () => {
    indexedDB.deleteDatabase('purdex-editor')
  })

  it('increments version on successful write', async () => {
    const repo = new EditorContentRepository(await openEditorDb())
    await repo.createDocument('doc-1', 'hello', '/untitled.md')

    const first = await repo.readDocument('doc-1')
    await repo.writeDocument('doc-1', 'world', first!.version)

    const second = await repo.readDocument('doc-1')
    expect(second?.version).toBe(first!.version + 1)
  })

  it('preserves tombstone binding metadata for deleted documents', async () => {
    const repo = new EditorContentRepository(await openEditorDb())
    await repo.createDocument('doc-1', 'hello', '/untitled.md')
    await repo.markTombstone('doc-1', 'deleted')

    const record = await repo.readDocument('doc-1')

    expect(record?.basePath).toBe('/untitled.md')
    expect(record?.tombstone).toBe(true)
    expect(record?.bindingStatus).toBe('deleted')
  })

  it('rejects writes to tombstoned documents', async () => {
    const repo = new EditorContentRepository(await openEditorDb())
    await repo.createDocument('doc-1', 'hello', '/untitled.md')
    await repo.markTombstone('doc-1', 'deleted')

    await expect(repo.writeDocument('doc-1', 'oops', 1)).rejects.toThrow(/inactive|deleted|binding/i)
  })

  it('rejects stale compare-and-swap writes', async () => {
    const repo = new EditorContentRepository(await openEditorDb())
    await repo.createDocument('doc-1', 'hello', '/untitled.md')

    await expect(repo.writeDocument('doc-1', 'oops', 999)).rejects.toThrow(/version/i)
  })
})

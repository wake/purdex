import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it } from 'vitest'
import { openEditorDb } from '../db'
import { EditorTreeRepository } from '../tree-repository'

describe('EditorTreeRepository', () => {
  beforeEach(async () => {
    indexedDB.deleteDatabase('purdex-editor')
  })

  it('creates a top-level file node', async () => {
    const repo = new EditorTreeRepository(await openEditorDb())
    await repo.createFileNode('/untitled.md', 'doc-1')

    const node = await repo.getNodeByDocId('doc-1')

    expect(node?.path).toBe('/untitled.md')
  })

  it('rejects nested file creation when the parent folder does not exist', async () => {
    const repo = new EditorTreeRepository(await openEditorDb())

    await expect(repo.createFileNode('/notes/daily.md', 'doc-1')).rejects.toThrow(/parent|exist/i)
  })

  it('renames descendants segment-safely', async () => {
    const repo = new EditorTreeRepository(await openEditorDb())
    await repo.createFolderNode('/notes')
    await repo.createFolderNode('/notes-old')
    await repo.createFileNode('/notes/a.md', 'doc-a')
    await repo.createFileNode('/notes-old/b.md', 'doc-b')

    await repo.renameNode('/notes', '/journal')

    expect((await repo.getNodeByDocId('doc-a'))?.path).toBe('/journal/a.md')
    expect((await repo.getNodeByDocId('doc-b'))?.path).toBe('/notes-old/b.md')
  })

  it('detaches deleted nodes so the original path can be reused', async () => {
    const repo = new EditorTreeRepository(await openEditorDb())
    await repo.createFolderNode('/notes')
    await repo.createFileNode('/notes/a.md', 'doc-a')

    await repo.markDeleted('/notes/a.md')

    expect(await repo.getNodeByPath('/notes/a.md')).toBeUndefined()
    const deleted = await repo.getNodeByDocId('doc-a')
    expect(deleted?.state).toBe('deleted')
    expect(deleted?.path.startsWith('/.purdex-deleted/')).toBe(true)
  })
})

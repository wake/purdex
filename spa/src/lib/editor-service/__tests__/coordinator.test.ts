import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { InAppBackend } from '../../fs-backend-inapp'
import {
  createEditorCoordinator,
  getEditorCoordinator,
  resetEditorCoordinatorCache,
} from '../coordinator'

function resetEditorDb(): Promise<void> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase('purdex-editor')
    request.onsuccess = () => resolve()
    request.onerror = () => reject(request.error ?? new Error('deleteDatabase failed'))
  })
}

describe('EditorCoordinator', () => {
  beforeEach(async () => {
    resetEditorCoordinatorCache()
    await resetEditorDb()
  })

  it('creates untitled markdown files under root', async () => {
    const coordinator = await createEditorCoordinator()
    const doc = await coordinator.createFile('/untitled.md', '')

    expect(doc.path).toBe('/untitled.md')
  })

  it('rejects moving folder into its own descendant', async () => {
    const coordinator = await createEditorCoordinator()
    await coordinator.createFolder('/notes')

    await expect(coordinator.renameNode('/notes', '/notes/archive')).rejects.toThrow(/descendant/i)
  })

  it('rejects nested writes when the target parent folder does not exist', async () => {
    const coordinator = await createEditorCoordinator()
    const doc = await coordinator.createFile('/untitled.md', '')

    await expect(coordinator.createFile('/notes/daily.md', '')).rejects.toThrow(/parent|exist/i)
    await expect(coordinator.createFolder('/notes/archive')).rejects.toThrow(/parent|exist/i)
    await expect(coordinator.saveDocumentAs(doc.docId, '/notes/archive.md', '', 1)).rejects.toThrow(/parent|exist/i)
  })

  it('restores a deleted file on save when the original path is still available', async () => {
    const coordinator = await createEditorCoordinator()
    await coordinator.createFolder('/notes')
    const doc = await coordinator.createFile('/notes/a.md', 'one')

    await coordinator.deletePath('/notes/a.md')

    expect(await coordinator.resolvePath(doc.docId)).toBeNull()
    const restored = await coordinator.saveDocument(doc.docId, 'old write', 1)
    expect(restored.path).toBe('/notes/a.md')
    expect(await coordinator.resolvePath(doc.docId)).toBe('/notes/a.md')
  })

  it('allows reoccupying a deleted path with a new document while old doc save requires save-as', async () => {
    const coordinator = await createEditorCoordinator()
    await coordinator.createFolder('/notes')
    const first = await coordinator.createFile('/notes/a.md', 'one')

    await coordinator.deletePath('/notes/a.md')

    const second = await coordinator.createFile('/notes/a.md', 'two')
    expect(second.docId).not.toBe(first.docId)
    expect(await coordinator.resolvePath(second.docId)).toBe('/notes/a.md')
    expect(await coordinator.resolvePath(first.docId)).toBeNull()
    await expect(coordinator.saveDocument(first.docId, 'old write', 1)).rejects.toThrow(/save as|required|exists/i)
  })

  it('requires save-as when a folder delete leaves the parent path missing', async () => {
    const coordinator = await createEditorCoordinator()
    await coordinator.createFolder('/notes')
    const doc = await coordinator.createFile('/notes/a.md', 'one')

    await coordinator.deletePath('/notes')

    expect(await coordinator.resolvePath(doc.docId)).toBeNull()
    await expect(coordinator.saveDocument(doc.docId, 'old write', 1)).rejects.toThrow(/save as|required|parent/i)
  })

  it('does not restore a deleted file when save fails version validation', async () => {
    const coordinator = await createEditorCoordinator()
    await coordinator.createFolder('/notes')
    const doc = await coordinator.createFile('/notes/a.md', 'one')

    await coordinator.deletePath('/notes/a.md')

    await expect(coordinator.saveDocument(doc.docId, 'old write', 999)).rejects.toThrow(/version/i)
    expect(await coordinator.resolvePath(doc.docId)).toBeNull()
  })

  it('rolls back saveDocumentAs when the content write fails', async () => {
    const coordinator = await createEditorCoordinator()
    await coordinator.createFolder('/notes')
    await coordinator.createFolder('/journal')
    const first = await coordinator.createFile('/notes/a.md', 'one')

    await expect(
      coordinator.saveDocumentAs(first.docId, '/journal/a.md', 'updated', 999),
    ).rejects.toThrow(/version/i)
    expect(await coordinator.resolvePath(first.docId)).toBe('/notes/a.md')
  })

  it('recovers from an initial coordinator creation failure', async () => {
    let attempts = 0
    const originalOpen = indexedDB.open.bind(indexedDB)
    const openSpy = vi.spyOn(indexedDB, 'open').mockImplementation(((...args: unknown[]) => {
      attempts += 1
      if (attempts === 1) {
        throw new Error('initial open failed')
      }
      return originalOpen(...args as Parameters<typeof indexedDB.open>)
    }) as typeof indexedDB.open)

    try {
      resetEditorCoordinatorCache()
      const coordinator = await getEditorCoordinator()

      expect(coordinator).toBeTruthy()
      expect(attempts).toBe(2)
    } finally {
      openSpy.mockRestore()
    }
  })

  it('retries backend operations after a recoverable coordinator error', async () => {
    vi.resetModules()
    let attempts = 0
    vi.doMock('../coordinator', async () => {
      const actual = await vi.importActual<typeof import('../coordinator')>('../coordinator')
      const coordinator = {
        writeFile: async () => {
          attempts += 1
          if (attempts === 1) {
            const error = new Error('database closed')
            error.name = 'InvalidStateError'
            throw error
          }
        },
      } as never
      return {
        ...actual,
        getEditorCoordinator: vi.fn(async () => coordinator),
        isCoordinatorRecoverableError: actual.isCoordinatorRecoverableError,
        resetEditorCoordinatorCache: vi.fn(),
      }
    })

    try {
      const { InAppBackend: MockBackend } = await import('../../fs-backend-inapp')
      const backend = new MockBackend()
      await backend.write('/second.md', new TextEncoder().encode('two'))
      expect(attempts).toBe(2)
    } finally {
      vi.doUnmock('../coordinator')
      vi.resetModules()
    }
  })
})

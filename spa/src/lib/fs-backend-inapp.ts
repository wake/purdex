import type { FsBackend } from './fs-backend'
import {
  getEditorCoordinator,
  isCoordinatorRecoverableError,
  resetEditorCoordinatorCache,
} from './editor-service/coordinator'
import type { FileStat, FileEntry } from '../types/fs'

async function withCoordinatorRetry<T>(operation: (coordinator: Awaited<ReturnType<typeof getEditorCoordinator>>) => Promise<T>): Promise<T> {
  const coordinator = await getEditorCoordinator()
  try {
    return await operation(coordinator)
  } catch (error) {
    if (!isCoordinatorRecoverableError(error)) {
      throw error
    }
    resetEditorCoordinatorCache()
    const retryCoordinator = await getEditorCoordinator()
    return operation(retryCoordinator)
  }
}

export class InAppBackend implements FsBackend {
  id = 'inapp'
  label = 'In-App Storage'

  available(): boolean {
    return true
  }

  async read(path: string): Promise<Uint8Array> {
    return withCoordinatorRetry((coordinator) => coordinator.readFile(path))
  }

  async write(path: string, content: Uint8Array): Promise<void> {
    await withCoordinatorRetry((coordinator) => coordinator.writeFile(path, content))
  }

  async stat(path: string): Promise<FileStat> {
    return withCoordinatorRetry((coordinator) => coordinator.statPath(path))
  }

  async list(path: string): Promise<FileEntry[]> {
    return withCoordinatorRetry((coordinator) => coordinator.listPath(path))
  }

  async mkdir(path: string, _recursive?: boolean): Promise<void> { // eslint-disable-line @typescript-eslint/no-unused-vars
    await withCoordinatorRetry((coordinator) => coordinator.mkdir(path))
  }

  async delete(path: string, _recursive?: boolean): Promise<void> { // eslint-disable-line @typescript-eslint/no-unused-vars
    await withCoordinatorRetry((coordinator) => coordinator.deletePath(path))
  }

  async rename(from: string, to: string): Promise<void> {
    await withCoordinatorRetry((coordinator) => coordinator.renameNode(from, to))
  }
}

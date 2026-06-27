import type { FileSource, FileStat, FileEntry } from '../types/fs'

export interface FsBackend {
  id: string
  label: string
  available(): boolean

  read(path: string): Promise<Uint8Array>
  write(path: string, content: Uint8Array): Promise<void>
  stat(path: string): Promise<FileStat>
  list(path: string): Promise<FileEntry[]>
  mkdir(path: string, recursive?: boolean): Promise<void>
  delete(path: string, recursive?: boolean): Promise<void>
  rename(from: string, to: string): Promise<void>
  /**
   * Atomically reserve a unique empty file under `dir`. Loops candidate names
   * `<baseName>`, `<baseName>-1`, … forming each path as `dir/<name>.<ext>`
   * (`ext` is bare — no leading dot) and reserves the first free key in a way
   * that is safe against concurrent callers (the single serialization point
   * that fixes the double-new-file shared-key race, #854). Returns the reserved
   * path.
   */
  createUnique(dir: string, baseName: string, ext: 'md' | 'txt'): Promise<string>
}

const backends = new Map<string, FsBackend>()

export function registerFsBackend(sourceType: string, backend: FsBackend): void {
  backends.set(sourceType, backend)
}

export function getFsBackend(source: FileSource): FsBackend | undefined {
  return backends.get(source.type)
}

export function clearFsBackendRegistry(): void {
  backends.clear()
}

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

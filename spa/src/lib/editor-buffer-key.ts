import type { FileSource } from '../types/fs'

export function bufferKey(source: FileSource, filePath: string): string {
  if (source.type === 'daemon') return `daemon:${source.hostId}:${filePath}`
  return `${source.type}:${filePath}`
}

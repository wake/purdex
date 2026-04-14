import type { PaneContent } from '../types/tab'
import type { FileSource, FileInfo } from '../types/fs'

export interface FileOpener {
  id: string
  label: string
  icon: string
  match: (file: FileInfo) => boolean
  priority: 'default' | 'option'
  createContent: (source: FileSource, file: FileInfo) => PaneContent
}

const openers: FileOpener[] = []

export function registerFileOpener(opener: FileOpener): void {
  openers.push(opener)
}

export function getFileOpeners(file: FileInfo): FileOpener[] {
  return openers.filter((o) => o.match(file))
}

export function getDefaultOpener(file: FileInfo): FileOpener | null {
  const matching = getFileOpeners(file)
  return matching.find((o) => o.priority === 'default') ?? matching[0] ?? null
}

export function clearFileOpenerRegistry(): void {
  openers.length = 0
}

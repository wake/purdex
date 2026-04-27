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

export interface RegisteredOpener extends FileOpener {
  ownerModuleId: string
}

const openers = new Map<string, RegisteredOpener>()

const keyOf = (ownerModuleId: string, id: string) => `${ownerModuleId}:${id}`

export function registerFileOpener(spec: FileOpener & { ownerModuleId: string }): void {
  openers.set(keyOf(spec.ownerModuleId, spec.id), spec)
}

export function unregisterByOwner(ownerModuleId: string): void {
  const prefix = `${ownerModuleId}:`
  for (const key of [...openers.keys()]) {
    if (key.startsWith(prefix)) openers.delete(key)
  }
}

export function clearAllForHmr(): void {
  openers.clear()
}

// Transitional alias kept for older callers (e.g. tests that pre-date owner
// scoping). New code should call clearAllForHmr() directly.
export const clearFileOpenerRegistry = clearAllForHmr

export function getRegisteredOpeners(): RegisteredOpener[] {
  return [...openers.values()]
}

export function getFileOpeners(file: FileInfo): RegisteredOpener[] {
  return [...openers.values()].filter((o) => o.match(file))
}

export function getDefaultOpener(file: FileInfo): RegisteredOpener | null {
  const matching = getFileOpeners(file)
  return matching.find((o) => o.priority === 'default') ?? matching[0] ?? null
}

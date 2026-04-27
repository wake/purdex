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

// Nested map: ownerModuleId → (opener.id → opener). Storing owner and opener
// id in separate map levels avoids the cross-owner collisions that a flat
// `${owner}:${id}` key would allow when either id happens to contain ':'.
const openersByOwner = new Map<string, Map<string, RegisteredOpener>>()

export function registerFileOpener(spec: FileOpener & { ownerModuleId: string }): void {
  let bucket = openersByOwner.get(spec.ownerModuleId)
  if (!bucket) {
    bucket = new Map()
    openersByOwner.set(spec.ownerModuleId, bucket)
  }
  bucket.set(spec.id, spec)
}

export function unregisterByOwner(ownerModuleId: string): void {
  openersByOwner.delete(ownerModuleId)
}

export function clearAllForHmr(): void {
  openersByOwner.clear()
}

// Transitional alias kept for older callers (e.g. tests that pre-date owner
// scoping). New code should call clearAllForHmr() directly.
export const clearFileOpenerRegistry = clearAllForHmr

export function getRegisteredOpeners(): RegisteredOpener[] {
  const out: RegisteredOpener[] = []
  for (const bucket of openersByOwner.values()) {
    for (const opener of bucket.values()) out.push(opener)
  }
  return out
}

export function getFileOpeners(file: FileInfo): RegisteredOpener[] {
  return getRegisteredOpeners().filter((o) => o.match(file))
}

export function getDefaultOpener(file: FileInfo): RegisteredOpener | null {
  const matching = getFileOpeners(file)
  return matching.find((o) => o.priority === 'default') ?? matching[0] ?? null
}

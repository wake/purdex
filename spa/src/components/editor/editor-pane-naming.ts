// spa/src/components/editor/editor-pane-naming.ts
//
// Pure file-name / path helpers shared by EditorPane and its flow hooks. They
// live outside the component so the hooks can reuse them without importing
// EditorPane (which would close an import cycle).
import type { UntitledDocumentState } from '../../types/tab'

export function isUntitledPath(filePath: string): boolean {
  return filePath.startsWith('untitled:')
}

export function displayName(filePath: string, untitled?: UntitledDocumentState): string {
  return untitled?.name ?? fileName(filePath)
}

export function renamePath(filePath: string, nextName: string, untitled?: UntitledDocumentState): string {
  return untitled ? `untitled:${nextName}` : siblingPath(filePath, nextName)
}

export function fileName(filePath: string): string {
  return filePath.split('/').pop() ?? filePath
}

export function siblingPath(filePath: string, nextBaseName: string): string {
  const separatorIndex = filePath.lastIndexOf('/')
  return separatorIndex === -1 ? nextBaseName : `${filePath.slice(0, separatorIndex)}/${nextBaseName}`
}

export function isInvalidRename(name: string): boolean {
  const trimmed = name.trim()
  return trimmed === '' || trimmed === '.' || trimmed === '..' || trimmed.includes('/') || trimmed.includes('\\')
}

export function isCaseOnlyRename(oldPath: string, nextPath: string): boolean {
  return oldPath !== nextPath && oldPath.toLowerCase() === nextPath.toLowerCase()
}

export function renameWarningMessage(error: unknown): string {
  if (error instanceof Error) {
    if (/exist/i.test(error.message)) return 'File already exists'
    return error.message || 'Rename failed'
  }
  return 'Rename failed'
}

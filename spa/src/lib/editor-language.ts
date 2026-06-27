import type { FileSource } from '../types/fs'
import type { UntitledDocumentState } from '../types/tab'
import type { EditorBufferMetadata, EditorLanguageSource } from '../stores/useEditorStore'
import { STORAGE_ROOT, join, isUnderRoot, relativeToRoot } from './storage-paths'

export function detectLanguage(filePath: string): string {
  const ext = filePath.split('.').pop()?.toLowerCase() ?? ''
  const map: Record<string, string> = {
    ts: 'typescript', tsx: 'typescriptreact', js: 'javascript', jsx: 'javascriptreact',
    json: 'json', md: 'markdown', css: 'css', html: 'html', go: 'go',
    py: 'python', rs: 'rust', sh: 'shell', yml: 'yaml', yaml: 'yaml',
    sql: 'sql', php: 'php', rb: 'ruby', swift: 'swift', kt: 'kotlin',
    java: 'java', c: 'c', cpp: 'cpp', h: 'c', hpp: 'cpp',
  }
  return map[ext] ?? 'plaintext'
}

// Matches a root-level `Untitled` / `Untitled-N` document name (relative to
// STORAGE_ROOT) — e.g. `Untitled.md`, `Untitled-2.txt`. Nested paths keep the
// folder segment in `relativeToRoot`, so `^Untitled…` won't match them
// (identical to the old `^/buffer/Untitled…` behavior).
const UNTITLED_NAME_RE = /^Untitled(?:-\d+)?\./

export function detectLanguageSource(source: FileSource, filePath: string): EditorLanguageSource {
  if (source.type === 'inapp' && isUnderRoot(filePath) && UNTITLED_NAME_RE.test(relativeToRoot(filePath))) {
    return 'template'
  }
  return 'extension'
}

export function untitledSuggestedName(untitled: UntitledDocumentState): string {
  return untitled.hasBeenRenamed ? untitled.name : `${untitled.name}${untitled.suggestedExtension}`
}

export function untitledStoragePath(name: string): string {
  return join(STORAGE_ROOT, name)
}

export function createMetadata(
  source: FileSource,
  filePath: string,
  untitled?: UntitledDocumentState,
): Pick<EditorBufferMetadata, 'language' | 'languageSource' | 'untitled'> {
  const resolvedPath = untitled
    ? untitledStoragePath(untitledSuggestedName(untitled))
    : filePath

  return {
    language: detectLanguage(resolvedPath),
    languageSource: !untitled || untitled.hasBeenRenamed
      ? detectLanguageSource(source, resolvedPath)
      : 'template',
    untitled,
  }
}

import type { FileSource } from '../types/fs'
import type { UntitledDocumentState } from '../types/tab'
import type { EditorBufferMetadata, EditorLanguageSource } from '../stores/useEditorStore'

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

export function detectLanguageSource(source: FileSource, filePath: string): EditorLanguageSource {
  if (source.type === 'inapp' && /^\/buffer\/Untitled(?:-\d+)?\./.test(filePath)) {
    return 'template'
  }
  return 'extension'
}

export function untitledSuggestedName(untitled: UntitledDocumentState): string {
  return untitled.hasBeenRenamed ? untitled.name : `${untitled.name}${untitled.suggestedExtension}`
}

export function untitledStoragePath(name: string): string {
  return `/buffer/${name}`
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

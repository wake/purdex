// spa/src/components/editor/EditorPane.tsx
import { useEffect, useCallback } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useEditorStore } from '../../stores/useEditorStore'
import { getFsBackend } from '../../lib/fs-backend'
import { MonacoWrapper } from './MonacoWrapper'
import { EditorToolbar } from './EditorToolbar'
import { EditorStatusBar } from './EditorStatusBar'
import type { FileSource } from '../../types/fs'

function bufferKey(source: FileSource, filePath: string): string {
  if (source.type === 'daemon') return `daemon:${source.hostId}:${filePath}`
  return `${source.type}:${filePath}`
}

function detectLanguage(filePath: string): string {
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

// Outer component does kind guard to avoid hooks-after-early-return
export function EditorPane({ pane, isActive }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'editor') return null
  return <EditorPaneInner source={content.source} filePath={content.filePath} isActive={isActive} />
}

function EditorPaneInner({ source, filePath, isActive }: { source: FileSource; filePath: string; isActive: boolean }) {
  const key = bufferKey(source, filePath)
  const buffer = useEditorStore((s) => s.buffers[key])

  // Load file on mount
  useEffect(() => {
    if (useEditorStore.getState().buffers[key]) return // already loaded
    const backend = getFsBackend(source)
    if (!backend) return

    backend.read(filePath)
      .then((data) => {
        const text = new TextDecoder().decode(data)
        const lang = detectLanguage(filePath)
        return backend.stat(filePath).then((stat) => {
          useEditorStore.getState().openBuffer(key, text, lang, { mtime: stat.mtime, size: stat.size })
        })
      })
      .catch(() => {
        // New file — open empty buffer
        useEditorStore.getState().openBuffer(key, '', detectLanguage(filePath))
      })
  }, [key, source, filePath])

  const handleSave = useCallback(async () => {
    const buf = useEditorStore.getState().buffers[key]
    if (!buf || !buf.isDirty) return
    const backend = getFsBackend(source)
    if (!backend) return
    try {
      const encoded = new TextEncoder().encode(buf.content)
      await backend.write(filePath, encoded)
      useEditorStore.getState().markSaved(key)
    } catch (err) {
      console.error('[editor] Save failed:', err)
    }
  }, [key, source, filePath])

  if (!buffer) {
    return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading...</div>
  }

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <EditorToolbar filePath={filePath} isDirty={buffer.isDirty} onSave={handleSave} />
      <div className="flex-1 overflow-hidden">
        <MonacoWrapper
          content={buffer.content}
          language={buffer.language}
          onChange={(value) => useEditorStore.getState().updateContent(key, value)}
          onCursorChange={(line, col) => useEditorStore.getState().updateCursor(key, line, col)}
          onSave={handleSave}
        />
      </div>
      <EditorStatusBar
        language={buffer.language}
        line={buffer.cursorPosition.line}
        column={buffer.cursorPosition.column}
      />
    </div>
  )
}

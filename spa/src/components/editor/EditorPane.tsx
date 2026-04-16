// spa/src/components/editor/EditorPane.tsx
import { lazy, Suspense, useEffect, useCallback, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useEditorStore } from '../../stores/useEditorStore'
import { getFsBackend } from '../../lib/fs-backend'
import { MonacoWrapper } from './MonacoWrapper'
import { DiffView } from './DiffView'
import { EditorToolbar } from './EditorToolbar'
import { EditorStatusBar } from './EditorStatusBar'
import type { FileSource } from '../../types/fs'

const TiptapEditor = lazy(() =>
  import('./TiptapEditor').then((m) => ({ default: m.TiptapEditor }))
)

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
  const isMarkdown = filePath.endsWith('.md') || filePath.endsWith('.mdx')
  const [editorMode, setEditorMode] = useState<'raw' | 'wysiwyg'>('raw')
  const [showDiff, setShowDiff] = useState(false)

  // Load file on mount, cleanup buffer on unmount
  useEffect(() => {
    let stale = false
    if (useEditorStore.getState().buffers[key]) return // already loaded
    const backend = getFsBackend(source)
    if (!backend) return

    backend.read(filePath)
      .then((data) => {
        if (stale) return
        const text = new TextDecoder().decode(data)
        const lang = detectLanguage(filePath)
        return backend.stat(filePath).then((stat) => {
          if (stale) return
          useEditorStore.getState().openBuffer(key, text, lang, { mtime: stat.mtime, size: stat.size })
        })
      })
      .catch(() => {
        if (stale) return
        // New file — open empty buffer
        useEditorStore.getState().openBuffer(key, '', detectLanguage(filePath))
      })

    return () => { stale = true }
  }, [key, source, filePath])

  // Cleanup buffer on unmount
  useEffect(() => {
    return () => { useEditorStore.getState().closeBuffer(key) }
  }, [key])

  // Detect external file changes when tab becomes active
  useEffect(() => {
    if (!isActive) return

    const buf = useEditorStore.getState().buffers[key]
    if (!buf) return

    const backend = getFsBackend(source)
    if (!backend) return

    backend.stat(filePath)
      .then((stat) => {
        const currentBuf = useEditorStore.getState().buffers[key]
        if (!currentBuf?.lastStat) return
        if (stat.mtime === currentBuf.lastStat.mtime && stat.size === currentBuf.lastStat.size) return

        return backend.read(filePath).then((data) => {
          const text = new TextDecoder().decode(data)
          const latestBuf = useEditorStore.getState().buffers[key]
          if (!latestBuf || text === latestBuf.savedContent) return

          if (!latestBuf.isDirty) {
            useEditorStore.getState().reloadBuffer(key, text, { mtime: stat.mtime, size: stat.size })
          } else {
            console.warn(`[editor] External change detected for ${filePath}, buffer is dirty`)
          }
        })
      })
      .catch(() => {}) // File may have been deleted
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only re-check on tab activation, not on source/filePath change
  }, [isActive, key])

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
    <div className="h-full w-full flex flex-col overflow-hidden">
      <EditorToolbar
        filePath={filePath}
        isDirty={buffer.isDirty}
        isMarkdown={isMarkdown}
        editorMode={editorMode}
        showDiff={showDiff}
        onSave={handleSave}
        onToggleMode={isMarkdown ? () => setEditorMode((m) => (m === 'raw' ? 'wysiwyg' : 'raw')) : undefined}
        onDiff={() => setShowDiff((d) => !d)}
      />
      <div className="flex-1 min-h-0 overflow-hidden">
        {showDiff ? (
          <DiffView
            original={buffer.savedContent}
            modified={buffer.content}
            language={buffer.language}
          />
        ) : editorMode === 'raw' ? (
          <MonacoWrapper
            content={buffer.content}
            language={buffer.language}
            onChange={(value) => useEditorStore.getState().updateContent(key, value)}
            onCursorChange={(line, col) => useEditorStore.getState().updateCursor(key, line, col)}
            onSave={handleSave}
          />
        ) : (
          <Suspense fallback={<div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading editor...</div>}>
            <TiptapEditor
              content={buffer.content}
              onChange={(md) => useEditorStore.getState().updateContent(key, md)}
              onSave={handleSave}
            />
          </Suspense>
        )}
      </div>
      <EditorStatusBar
        language={buffer.language}
        line={buffer.cursorPosition.line}
        column={buffer.cursorPosition.column}
      />
    </div>
  )
}

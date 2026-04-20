// spa/src/components/editor/EditorPane.tsx
import { lazy, Suspense, useEffect, useCallback, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useEditorStore } from '../../stores/useEditorStore'
import { getFsBackend } from '../../lib/fs-backend'
import { getEditorCoordinator } from '../../lib/editor-service/coordinator'
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
  return (
    <EditorPaneInner
      source={content.source}
      docId={content.source.type === 'inapp' ? content.docId : undefined}
      filePath={content.filePath ?? ''}
      isActive={isActive}
    />
  )
}

function EditorPaneInner({
  source,
  docId,
  filePath,
  isActive,
}: {
  source: FileSource
  docId?: string
  filePath: string
  isActive: boolean
}) {
  const key = source.type === 'inapp' ? (docId ?? filePath) : bufferKey(source, filePath)
  const buffer = useEditorStore((s) => s.buffers[key])
  const [currentPath, setCurrentPath] = useState(filePath)
  const [loadState, setLoadState] = useState<'loading' | 'ready' | 'missing'>('loading')
  const displayPath = currentPath || filePath
  const isMarkdown = displayPath.endsWith('.md') || displayPath.endsWith('.mdx')
  const [editorMode, setEditorMode] = useState<'raw' | 'wysiwyg'>('raw')
  const [showDiff, setShowDiff] = useState(false)

  // Load file on mount, cleanup buffer on unmount
  useEffect(() => {
    let stale = false
    if (useEditorStore.getState().buffers[key]) return // already loaded
    const backend = getFsBackend(source)
    if (!backend) return

    const load = async () => {
      try {
        if (source.type === 'inapp' && docId) {
          const coordinator = await getEditorCoordinator()
          const snapshot = await coordinator.getDocumentSnapshot(docId)
          if (stale) return
          const nextPath = snapshot.path ?? filePath
          setCurrentPath(nextPath)
          let stat: { mtime: number; size: number } | undefined
          if (snapshot.path) {
            try {
              const nextStat = await backend.stat(snapshot.path)
              stat = { mtime: nextStat.mtime, size: nextStat.size }
            } catch {
              stat = undefined
            }
          }
          useEditorStore.getState().openBuffer(
            key,
            snapshot.text,
            detectLanguage(nextPath),
            stat,
            { baseVersion: snapshot.version, bindingStatus: snapshot.bindingStatus },
          )
          setLoadState('ready')
          return
        }

        const data = await backend.read(filePath)
        if (stale) return
        const text = new TextDecoder().decode(data)
        const stat = await backend.stat(filePath)
        if (stale) return
        setCurrentPath(filePath)
        useEditorStore.getState().openBuffer(key, text, detectLanguage(filePath), { mtime: stat.mtime, size: stat.size })
        setLoadState('ready')
      } catch {
        if (stale) return
        if (source.type === 'inapp') {
          setLoadState('missing')
          return
        }
        // New file — open empty buffer
        useEditorStore.getState().openBuffer(key, '', detectLanguage(filePath))
        setLoadState('ready')
      }
    }

    void load()

    return () => { stale = true }
  }, [docId, filePath, key, source])

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

    if (source.type === 'inapp' && docId) {
      void getEditorCoordinator()
        .then((coordinator) => coordinator.getDocumentSnapshot(docId))
        .then((snapshot) => {
          const latestBuf = useEditorStore.getState().buffers[key]
          if (!latestBuf) return
          const nextPath = snapshot.path ?? filePath
          setCurrentPath(nextPath)
          if (snapshot.text === latestBuf.savedContent && snapshot.bindingStatus === latestBuf.bindingStatus) return
          if (!latestBuf.isDirty) {
            useEditorStore.getState().reloadBuffer(
              key,
              snapshot.text,
              undefined,
              { baseVersion: snapshot.version, bindingStatus: snapshot.bindingStatus },
            )
          }
        })
        .catch(() => {})
      return
    }

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
  }, [docId, filePath, isActive, key, source])

  const handleSave = useCallback(async () => {
    const buf = useEditorStore.getState().buffers[key]
    if (!buf || !buf.isDirty) return
    const backend = getFsBackend(source)
    if (!backend) return
    try {
      if (source.type === 'inapp' && docId) {
        const coordinator = await getEditorCoordinator()
        const next = await coordinator.saveDocument(docId, buf.content, buf.baseVersion)
        setCurrentPath(next.path)
        let stat: { mtime: number; size: number } | undefined
        try {
          const newStat = await backend.stat(next.path)
          stat = { mtime: newStat.mtime, size: newStat.size }
        } catch {
          stat = undefined
        }
        useEditorStore.getState().markSaved(
          key,
          stat,
          { baseVersion: next.version, bindingStatus: 'active' },
        )
      } else {
        const encoded = new TextEncoder().encode(buf.content)
        await backend.write(filePath, encoded)
        const newStat = await backend.stat(filePath)
        useEditorStore.getState().markSaved(key, { mtime: newStat.mtime, size: newStat.size })
      }
      setShowDiff(false)
    } catch (err) {
      if (source.type === 'inapp' && /save as required|parent folder does not exist|path already exists/i.test(String(err))) {
        useEditorStore.getState().setBindingStatus(key, 'orphaned')
      }
      console.error('[editor] Save failed:', err)
    }
  }, [docId, filePath, key, source])

  if (!buffer) {
    if (loadState === 'missing') {
      return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Document unavailable</div>
    }
    return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading...</div>
  }

  return (
    <div className="h-full w-full flex flex-col overflow-hidden">
      <EditorToolbar
        filePath={displayPath}
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

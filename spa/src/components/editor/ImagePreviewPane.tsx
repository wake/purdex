// spa/src/components/editor/ImagePreviewPane.tsx
import { useEffect, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { getFsBackend } from '../../lib/fs-backend'
import type { FileSource } from '../../types/fs'

// Outer component does kind guard to avoid hooks-after-early-return
export function ImagePreviewPane({ pane }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'image-preview') return null
  return <ImagePreviewPaneInner source={content.source} filePath={content.filePath} />
}

function ImagePreviewPaneInner({ source, filePath }: { source: FileSource; filePath: string }) {
  const [objectUrl, setObjectUrl] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const backend = getFsBackend(source)

  useEffect(() => {
    if (!backend) return

    let url: string | null = null
    let stale = false

    backend.read(filePath)
      .then((data) => {
        if (stale) return
        url = URL.createObjectURL(new Blob([data]))
        setObjectUrl(url)
      })
      .catch((err: Error) => {
        if (!stale) setError(err.message)
      })

    return () => { stale = true; if (url) URL.revokeObjectURL(url) }
  }, [backend, filePath])

  if (!backend) return <div className="flex-1 flex items-center justify-center text-red-400 text-xs">No FS backend</div>

  if (error) return <div className="flex-1 flex items-center justify-center text-red-400 text-xs">{error}</div>
  if (!objectUrl) return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading...</div>

  const fileName = filePath.split('/').pop() ?? ''

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <div className="px-3 py-1 border-b border-border-subtle bg-surface-secondary text-xs text-text-secondary truncate">
        {fileName}
      </div>
      <div className="flex-1 flex items-center justify-center p-4 overflow-auto bg-surface-primary">
        <img src={objectUrl} alt={fileName} className="max-w-full max-h-full object-contain" />
      </div>
    </div>
  )
}

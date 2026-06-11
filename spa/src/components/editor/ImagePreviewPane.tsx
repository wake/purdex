// spa/src/components/editor/ImagePreviewPane.tsx
import { useEffect, useLayoutEffect, useRef, useState, useMemo, useCallback } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { getFsBackend } from '../../lib/fs-backend'
import { bufferKey } from '../../lib/editor-buffer-key'
import type { FileSource } from '../../types/fs'

export function ImagePreviewPane({ pane }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'image-preview') return null
  return <ImagePreviewPaneInner source={content.source} filePath={content.filePath} />
}

function ImagePreviewPaneInner({ source, filePath }: { source: FileSource; filePath: string }) {
  const [objectUrl, setObjectUrl] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [zoom, setZoom] = useState<'fit' | 'actual'>('fit')
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null)
  const [box, setBox] = useState<{ w: number; h: number } | null>(null)
  const backend = getFsBackend(source)

  const containerRef = useRef<HTMLDivElement | null>(null)
  const imgRef = useRef<HTMLImageElement | null>(null)

  // Reset zoom + measured natural size during render when the buffer changes
  // (the React-recommended "adjust state on prop change" pattern, avoiding a
  // setState-in-effect reset that would cascade-render and trip the lint rule).
  // Identity is the composite (source, filePath) key so that switching to the
  // same path on a different source (e.g. another daemon host, or inapp↔daemon)
  // still resets zoom/natural rather than carrying over stale measurements.
  const identity = bufferKey(source, filePath)
  const [seenIdentity, setSeenIdentity] = useState(identity)
  if (seenIdentity !== identity) {
    setSeenIdentity(identity)
    setZoom('fit')
    setNatural(null)
  }

  // Key the read effect off the stable composite identity string rather than the
  // `backend` object's identity: the backend is resolved fresh on every render and
  // would otherwise re-run this effect each render (and, with the synchronous
  // reset below, loop forever). `identity` changes exactly when source or filePath
  // changes — which is precisely when a re-read is needed.
  useEffect(() => {
    if (!backend) return
    // Clear any previously loaded image / error so switching files immediately
    // falls back to Loading instead of leaving the old image (or a stale error
    // banner) on screen until the new read resolves. The previous objectUrl is
    // still revoked by this effect's cleanup, so no blob URL leaks.
    setObjectUrl(null)
    setError(null)
    let url: string | null = null
    let stale = false
    backend.read(filePath)
      .then((data) => {
        if (stale) return
        url = URL.createObjectURL(new Blob([new Uint8Array(data)]))
        setObjectUrl(url)
      })
      .catch((err: Error) => { if (!stale) setError(err.message) })
    return () => { stale = true; if (url) URL.revokeObjectURL(url) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity])

  const measureNatural = useCallback(() => {
    const img = imgRef.current
    if (!img || !img.complete || img.naturalWidth === 0) return
    setNatural((prev) =>
      prev && prev.w === img.naturalWidth && prev.h === img.naturalHeight
        ? prev
        : { w: img.naturalWidth, h: img.naturalHeight },
    )
  }, [])

  // Cached / HMR blobs can be `complete` before any load event fires, so measure
  // synchronously AND attach a native load listener (React onLoad is unreliable
  // for already-complete images — that left oversized permanently false).
  useEffect(() => {
    const img = imgRef.current
    if (!img) return
    measureNatural()
    img.addEventListener('load', measureNatural)
    return () => img.removeEventListener('load', measureNatural)
  }, [objectUrl, measureNatural])

  useLayoutEffect(() => {
    const el = containerRef.current
    if (!el) return
    const update = () => {
      const w = el.clientWidth
      const h = el.clientHeight
      setBox((prev) => (prev && prev.w === w && prev.h === h ? prev : { w, h }))
    }
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [objectUrl])

  const oversized = useMemo(() => {
    if (!natural || !box) return false
    return natural.w > box.w || natural.h > box.h
  }, [natural, box])

  if (!backend) return <div className="flex-1 flex items-center justify-center text-red-400 text-xs">No FS backend</div>
  if (error) return <div className="flex-1 flex items-center justify-center text-red-400 text-xs">{error}</div>
  if (!objectUrl) return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading...</div>

  const fileName = filePath.split('/').pop() ?? ''
  const isActual = zoom === 'actual' && oversized
  const layoutClasses = isActual ? 'items-start justify-start' : 'items-center justify-center'
  const cursorClass = oversized ? (isActual ? 'cursor-zoom-out' : 'cursor-zoom-in') : ''
  const imgClasses = isActual ? 'max-w-none max-h-none' : 'max-w-full max-h-full object-contain'

  const handleClick = () => {
    if (!oversized) return
    setZoom((z) => (z === 'fit' ? 'actual' : 'fit'))
  }

  return (
    <div className="flex-1 flex flex-col overflow-hidden min-h-0">
      <div className="px-3 py-1 border-b border-border-subtle bg-surface-secondary text-xs text-text-secondary truncate">
        {fileName}
      </div>
      <div ref={containerRef} className={`flex-1 min-h-0 min-w-0 flex ${layoutClasses} p-4 overflow-auto bg-surface-primary`}>
        <img ref={imgRef} src={objectUrl} alt={fileName} onLoad={measureNatural} onClick={handleClick} className={`${imgClasses} ${cursorClass}`.trim()} />
      </div>
    </div>
  )
}

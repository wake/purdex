// spa/src/components/deck/AttachmentThumb.tsx — one attachment as a small thumbnail (v3 pt 8): the input's chips and the image
// tags in a sent message share it. An image shows its picture (48x48, cropped), any other file a file icon and a short name.
// Resting on it for a moment floats a larger preview above it (portal, fixed: never clipped by an overflow, never in the way of
// a click); clicking an image opens the lightbox. The picture itself comes in as `src` (a local blob: URL or the host's read).
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { File as FileIcon, X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { HOVER_PREVIEW_DELAY_MS, formatBytes, shortName } from '../../lib/conversations/attachment-format'
import { ImageLightbox } from './ImageLightbox'

const PREVIEW_MAX = 360

interface Props {
  name: string
  image: boolean
  /** The picture, when there is one. */
  src?: string | null
  /** The picture is on its way (a read in flight): a skeleton. */
  loading?: boolean
  size?: number
  /** An upload in flight: a veil with the percentage. */
  percent?: number
  /** Screen-reader text of the veil. */
  progressLabel?: string
  /** Shows the remove button. */
  onRemove?: () => void
  removeLabel?: string
  testId?: string
}

export function AttachmentThumb({ name, image, src, loading, size, percent, progressLabel, onRemove, removeLabel, testId = 'attachment-thumb' }: Props) {
  const t = useI18nStore((s) => s.t)
  const root = useRef<HTMLSpanElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const [broken, setBroken] = useState<string | null>(null) // the src that failed to decode
  const [hover, setHover] = useState(false)
  const [lightbox, setLightbox] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const cancel = () => { if (timer.current) { clearTimeout(timer.current); timer.current = null } }
  useEffect(() => cancel, [])
  const showsPicture = image && !!src && broken !== src
  const skeleton = image && !showsPicture && !!loading
  const canOpen = showsPicture && percent === undefined
  const enter = () => { cancel(); timer.current = setTimeout(() => { timer.current = null; setHover(true) }, HOVER_PREVIEW_DELAY_MS) }
  const leave = () => { cancel(); setHover(false) }
  const uploading = percent !== undefined

  const face = showsPicture ? (
    <img src={src ?? undefined} alt={name} draggable={false} onError={() => setBroken(src ?? null)} className="h-full w-full object-cover" />
  ) : skeleton ? (
    <span data-testid="attachment-skeleton" aria-hidden className="block h-full w-full animate-pulse bg-text-primary/10" />
  ) : (
    <span className={`flex h-full items-center gap-1 text-xs text-text-primary ${image ? 'flex-col justify-center px-2' : 'px-2'}`}>
      <FileIcon size={image ? 18 : 20} aria-hidden className="shrink-0 text-text-muted" />
      <span data-testid="attachment-name" className="whitespace-nowrap">{shortName(name, image ? 10 : 14)}</span>
    </span>
  )
  const box = `block h-12 overflow-hidden rounded-md border border-border-subtle bg-surface-secondary ${showsPicture || skeleton ? 'w-12' : 'min-w-12'}`

  return (
    <span
      ref={root}
      data-testid={testId}
      data-kind={image ? 'image' : 'file'}
      className="group relative inline-block align-top"
      onMouseEnter={enter}
      onMouseLeave={leave}
    >
      {canOpen ? (
        <button
          ref={trigger}
          type="button"
          aria-label={t('deck.attach.open', { name })}
          onClick={() => { leave(); setLightbox(true) }}
          className={`${box} cursor-zoom-in p-0 focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent`}
        >{face}</button>
      ) : (
        <span className={box}>{face}</span>
      )}
      {uploading && (
        <span data-testid="attachment-uploading" role="status" className="absolute inset-0 flex items-center justify-center rounded-md bg-black/50 text-xs font-medium text-white">
          <span aria-hidden>{percent}%</span>
          <span className="sr-only">{progressLabel}</span>
        </span>
      )}
      {onRemove && (
        <button
          type="button"
          aria-label={removeLabel}
          title={removeLabel}
          onClick={onRemove}
          className="absolute -right-1.5 -top-1.5 flex h-4 w-4 items-center justify-center rounded-full border border-border-subtle bg-surface-secondary text-text-muted opacity-70 hover:text-text-primary focus-visible:opacity-100 group-hover:opacity-100"
        ><X size={10} weight="bold" /></button>
      )}
      {hover && !lightbox && <HoverPreview anchor={root} name={name} src={showsPicture ? src : null} size={size} />}
      {lightbox && src && (
        <ImageLightbox src={src} name={name} onClose={() => { setLightbox(false); trigger.current?.focus() }} />
      )}
    </span>
  )
}

/** The larger preview, floating above the thumbnail. It never takes the pointer: the box under it stays clickable. */
function HoverPreview({ anchor, name, src, size }: { anchor: React.RefObject<HTMLElement | null>; name: string; src: string | null | undefined; size?: number }) {
  const [pos, setPos] = useState<{ left: number; bottom: number } | null>(null)
  const card = useRef<HTMLDivElement>(null)
  const [grown, setGrown] = useState(0) // the picture's load changes the card's width: place again
  useLayoutEffect(() => {
    const a = anchor.current
    if (!a) return
    const r = a.getBoundingClientRect()
    const w = card.current?.offsetWidth ?? PREVIEW_MAX
    const left = Math.min(Math.max(8, r.left + r.width / 2 - w / 2), Math.max(8, window.innerWidth - w - 8))
    // eslint-disable-next-line react-hooks/set-state-in-effect -- measured placement of a fixed card
    setPos({ left, bottom: window.innerHeight - r.top + 8 })
  }, [anchor, src, grown])
  const bytes = formatBytes(size)
  return createPortal(
    <div
      ref={card}
      data-testid="attachment-hover-preview"
      role="tooltip"
      style={{ position: 'fixed', left: pos?.left ?? 0, bottom: pos?.bottom ?? 0, visibility: pos ? 'visible' : 'hidden', pointerEvents: 'none', maxWidth: PREVIEW_MAX + 16 }}
      className="z-[1000] rounded-lg border border-border-default bg-surface-secondary p-2 shadow-xl"
    >
      {src && <img src={src} alt="" onLoad={() => setGrown((n) => n + 1)} style={{ maxWidth: PREVIEW_MAX, maxHeight: PREVIEW_MAX }} className="block rounded object-contain" />}
      <div className={`break-all text-xs text-text-primary ${src ? 'mt-1' : ''}`}>{name}</div>
      {bytes && <div className="text-xs text-text-muted">{bytes}</div>}
    </div>,
    document.body,
  )
}

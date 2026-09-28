// spa/src/components/room/AttachmentThumbs.tsx — the images a user line
// carried (worker-pane theme phase E, nexen contract §1.9), shared by the
// room and chat lines. A durable line knows only each image's metadata, so
// every thumbnail fetches its bytes from the host's capability route
// (`fetchAttachment`) — at most four fetches at a time across the whole app —
// and turns them into an object URL it revokes on unmount. The optimistic
// line already holds local object URLs (owned by the execution store) and
// only draws them. While loading, and when a fetch fails (a 404
// `attachment_not_found`, an older daemon, a host gone), the thumbnail is a
// placeholder naming the media type — never an empty box (Review Focus 5).
// A thumbnail that loaded opens the full image in a new tab.
import { useContext, useEffect, useState } from 'react'
import { Image as ImageIcon, ImageBroken } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectAttachmentFetch, selectReady, useNexHostStore } from '../../stores/useNexHostStore'
import { fetchAttachment } from '../../lib/nex/nex-api'
import type { AttachmentMeta } from '../../lib/nex/attachments'
import { AttachmentSourceContext } from './attachment-source'

/** Fetches in flight at once, app-wide. */
const ATTACHMENT_FETCH_CONCURRENCY = 4
let running = 0
const waiting: Array<() => void> = []

/** Run `task` once one of the shared slots is free; `cancelled` is re-checked when the slot is granted. */
function withSlot<T>(task: () => Promise<T>, cancelled: () => boolean): Promise<T | undefined> {
  return new Promise<T | undefined>((resolve, reject) => {
    const start = () => {
      if (cancelled()) { next(); resolve(undefined); return }
      running++
      task().then(resolve, reject).finally(() => { running--; next() })
    }
    if (running < ATTACHMENT_FETCH_CONCURRENCY) start()
    else waiting.push(start)
  })
}
function next() {
  if (running < ATTACHMENT_FETCH_CONCURRENCY) waiting.shift()?.()
}

type ThumbState = { status: 'loading' } | { status: 'ready'; url: string } | { status: 'error' }

function ThumbView({ state, mediaType }: { state: ThumbState; mediaType: string }) {
  const t = useI18nStore((s) => s.t)
  const [broken, setBroken] = useState(false)
  if (state.status === 'ready' && !broken) {
    return (
      <a data-testid="attachment-thumb" data-state="ready" href={state.url} target="_blank" rel="noopener noreferrer"
        title={t('worker.attachment.open', { type: mediaType })}
        className="block shrink-0 rounded border border-border-subtle overflow-hidden hover:border-border-default">
        <img src={state.url} alt={t('worker.attachment.image', { type: mediaType })} draggable={false}
          onError={() => setBroken(true)} className="block h-16 max-w-[8rem] object-cover" />
      </a>
    )
  }
  const error = state.status === 'error' || broken
  const label = t(error ? 'worker.attachment.unavailable' : 'worker.attachment.image', { type: mediaType })
  return (
    <div data-testid="attachment-thumb" data-state={error ? 'error' : 'loading'} role="img" aria-label={label} title={label}
      className="flex h-16 w-16 shrink-0 flex-col items-center justify-center gap-0.5 rounded border border-dashed border-border-subtle text-text-muted">
      {error ? <ImageBroken size={18} /> : <ImageIcon size={18} className="animate-pulse" />}
      <span className="max-w-full truncate px-1 text-[10px] leading-tight">{mediaType}</span>
    </div>
  )
}

function RemoteThumb({ meta, source }: { meta: AttachmentMeta; source: { hostId: string; executionId: string } | null }) {
  // Primitives only: the context value may be a fresh object on any render.
  const hostId = source?.hostId ?? ''
  const executionId = source?.executionId ?? ''
  const sha256 = meta.sha256
  const route = useNexHostStore(selectAttachmentFetch(hostId))
  const ready = useNexHostStore(selectReady(hostId))
  // What the last settled fetch was for; a new source, hash or route starts over.
  const want = `${hostId}\n${executionId}\n${sha256}\n${route?.method ?? ''} ${route?.path ?? ''}`
  const [result, setResult] = useState<{ want: string; state: ThumbState } | null>(null)

  useEffect(() => {
    if (!hostId || !executionId || !route) return
    let cancelled = false
    let url: string | null = null
    withSlot(() => fetchAttachment(hostId, executionId, sha256, route), () => cancelled)
      .then((blob) => {
        if (cancelled || !blob) return
        url = URL.createObjectURL(blob)
        setResult({ want, state: { status: 'ready', url } })
      })
      .catch(() => { if (!cancelled) setResult({ want, state: { status: 'error' } }) })
    return () => {
      cancelled = true
      if (url) URL.revokeObjectURL(url)
    }
  }, [hostId, executionId, sha256, route, want])

  // No route: still loading while the host's capabilities are on their way;
  // otherwise (no pane source, an older daemon, a host gone) it never comes.
  const state: ThumbState = !hostId || !executionId || (!route && ready) ? { status: 'error' }
    : result?.want === want ? result.state
    : { status: 'loading' }
  return <ThumbView key={state.status === 'ready' ? state.url : state.status} state={state} mediaType={meta.media_type} />
}

/** A durable user line's images, fetched from the pane's host. */
export default function AttachmentThumbs({ items }: { items: readonly AttachmentMeta[] }) {
  const source = useContext(AttachmentSourceContext)
  if (items.length === 0) return null
  return (
    <div data-testid="attachment-thumbs" className="flex flex-wrap gap-1.5">
      {items.map((m, i) => <RemoteThumb key={`${i}-${m.sha256}`} meta={m} source={source} />)}
    </div>
  )
}

/** The optimistic line's images: local object URLs the execution store owns; drawn, never revoked here. */
export function PendingAttachmentThumbs({ items }: { items: readonly { previewUrl: string; media_type: string }[] }) {
  if (items.length === 0) return null
  return (
    <div data-testid="attachment-thumbs" className="flex flex-wrap gap-1.5">
      {items.map((p, i) => <ThumbView key={`${i}-${p.previewUrl}`} state={{ status: 'ready', url: p.previewUrl }} mediaType={p.media_type} />)}
    </div>
  )
}

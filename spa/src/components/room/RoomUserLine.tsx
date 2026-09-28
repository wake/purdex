// spa/src/components/room/RoomUserLine.tsx — the human's own line in the room
// (spec §4.1, §5.4). Not a bubble: a full-width band, bleeding from the
// transcript's left edge to its right edge (the `-mx-4` matches
// RoomTranscript's `p-4` — if you change one, change the other), with a `›`
// prefix carrying authorship instead of a gutter mark. The optimistic line
// the pane shows before the daemon accepts a send is the same band, dimmed.
// Images the line carried (phase E) sit under the text, aligned with it.
import type { ReactNode } from 'react'

interface Props {
  text: string
  /** Not accepted by the daemon yet (ExecutionView's pendingLocal). */
  pending?: boolean
  /** Drawn after the text on the same row — the pending line's `queued` tag. */
  children?: ReactNode
  /** The search anchor of the text (a durable line's; the pending line has none). */
  searchUnit?: string
  /** The line's image thumbnails, on their own row under the text. */
  attachments?: ReactNode
}

const BAND = '-mx-4 px-4 py-1 bg-[var(--wt-user-band-bg)] text-[var(--wt-user-band-fg)] text-[length:var(--wt-font-size)] leading-[var(--wt-line-height)]'

export default function RoomUserLine({ text, pending, children, searchUnit, attachments }: Props) {
  const prefix = (
    <span
      data-testid="room-user-prefix"
      aria-hidden="true"
      className="shrink-0 text-[var(--wt-user-band-prefix-color)]"
    >
      ›
    </span>
  )
  const row = (
    <>
      {prefix}
      <p data-search-unit={searchUnit} className="min-w-0 whitespace-pre-wrap break-words">{text}</p>
      {children}
    </>
  )
  if (!attachments) {
    return (
      <div data-testid="room-user-line" className={`${BAND} flex items-baseline gap-2${pending ? ' opacity-60' : ''}`}>
        {row}
      </div>
    )
  }
  // An image-only line has no text row: the prefix goes on the thumbnails'
  // row. Otherwise an invisible twin keeps the thumbnails under the text.
  const textRow = !!text || (children != null && children !== false)
  return (
    <div data-testid="room-user-line" className={`${BAND} flex flex-col gap-1${pending ? ' opacity-60' : ''}`}>
      {textRow && <div className="flex items-baseline gap-2">{row}</div>}
      <div className="flex gap-2">
        {textRow ? <span aria-hidden="true" className="shrink-0 invisible">›</span> : prefix}
        {attachments}
      </div>
    </div>
  )
}

// spa/src/components/room/RoomUserLine.tsx — the human's own line in the room
// (spec §4.1). Not a bubble: it starts at the pane's one left edge like
// everything else, and authorship is carried by a mark in the gutter plus
// slightly stronger text. The optimistic line the pane shows before the daemon
// accepts a send is the same line, dimmed.
import type { ReactNode } from 'react'

interface Props {
  text: string
  /** Not accepted by the daemon yet (ExecutionView's pendingLocal). */
  pending?: boolean
  /** Drawn after the text on the same row — the pending line's `queued` tag. */
  children?: ReactNode
}

export default function RoomUserLine({ text, pending, children }: Props) {
  return (
    <div
      data-testid="room-user-line"
      className={`relative flex items-baseline gap-2 text-sm text-text-primary font-medium${pending ? ' opacity-60' : ''}`}
    >
      {/*
        In the gutter, not in the flow: the mark hangs left of the edge so the
        text itself starts at the same x as the prose and the operations.
      */}
      <span
        data-testid="room-user-mark"
        aria-hidden="true"
        className="absolute -left-2.5 top-0.5 bottom-0.5 w-0.5 rounded-full bg-accent"
      />
      <p className="whitespace-pre-wrap break-words">{text}</p>
      {children}
    </div>
  )
}

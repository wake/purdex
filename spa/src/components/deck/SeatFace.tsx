// spa/src/components/deck/SeatFace.tsx — a persona's face inside the deck (name pool v2 spec §9.4): the avatar of the agent speaking, on the persona's own colour when there is no picture.
import { Avatar } from '../Avatar'
import type { Seat } from '../../lib/names/seat'

export function SeatFace({ seat, size }: { seat: Seat; size: 14 | 16 }) {
  return <Avatar size={size} code={seat.code} name={seat.name} src={seat.avatar} decorative />
}

/** The compact line over a later speech start (spec §9.4): the face and the name — the name in the primary text colour, like the header's — no time. */
export function SpeechLine({ seat }: { seat: Seat }) {
  return (
    <div data-testid="deck-speech-line" className="mb-1 flex items-center gap-1.5 deck-t-down text-text-muted">
      <SeatFace seat={seat} size={14} />
      <span className="text-text-primary">{seat.name}</span>
    </div>
  )
}

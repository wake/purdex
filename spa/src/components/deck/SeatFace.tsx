// spa/src/components/deck/SeatFace.tsx — a persona's face inside the deck (name pool v2 spec §9.4): the avatar of the agent speaking, on the colour of the host the conversation lives on when there is no picture.
import { useContext } from 'react'
import { Avatar } from '../Avatar'
import { ConversationHostContext } from './conversation-host'
import { useHostLook } from '../../lib/host-look'
import { resolveHostColors } from '../../lib/host-color'
import type { Seat } from '../../lib/names/seat'

export function SeatFace({ seat, size }: { seat: Seat; size: 14 | 16 }) {
  const look = useHostLook(useContext(ConversationHostContext))
  const faceColor = resolveHostColors({ colors: look.colors, color: look.color }, 'terminal')?.main ?? '#6b7280'
  return <Avatar size={size} code={seat.code} name={seat.name} src={seat.avatar} fallbackColor={faceColor} />
}

/** The compact line over a later speech start (spec §9.4): the face and the name in muted text, no time. */
export function SpeechLine({ seat }: { seat: Seat }) {
  return (
    <div data-testid="deck-speech-line" className="mb-1 flex items-center gap-1.5 deck-t-down text-text-muted">
      <SeatFace seat={seat} size={14} />
      <span>{seat.name}</span>
    </div>
  )
}

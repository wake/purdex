// spa/src/components/deck/AgentHeader.tsx — proto: the head line of an agent turn (deck v2): model icon + name + time. A conversation whose agent is a persona (name pool v2 spec §9.4) shows
// `[avatar] Name · model · time` instead: the face takes the place of the model icon, the persona's name the place of the model's.
import { ModelIcon } from '../team/ModelIcon'
import { familyOf } from '../team/model-family'
import { formatClock } from '../../lib/conversations/deck-format'
import type { Seat } from '../../lib/names/seat'
import { SeatFace } from './SeatFace'

export function AgentHeader({ model, at, seat }: { model?: string; at: number; seat?: Seat | null }) {
  if (seat) {
    return (
      <div data-testid="deck-agent-header" data-persona={seat.code} className="mb-1 flex items-center gap-1.5 deck-t-down text-text-secondary">
        <SeatFace seat={seat} size={16} />
        <span className="font-semibold text-text-primary">{seat.name}</span>
        {model && <span>· {model}</span>}
        <span>· {formatClock(at)}</span>
      </div>
    )
  }
  const name = model || 'Claude'
  return (
    <div data-testid="deck-agent-header" className="mb-1 flex items-center gap-1.5 deck-t-down text-text-secondary">
      <span className="text-accent-hover"><ModelIcon model={familyOf(model) ?? 'opus'} size={11} /></span>
      <span className="font-medium text-text-primary">{name}</span>
      <span>· {formatClock(at)}</span>
    </div>
  )
}

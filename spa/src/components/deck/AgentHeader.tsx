// spa/src/components/deck/AgentHeader.tsx — proto: the head line of an agent turn (deck v2): model icon + name + time.
import { ModelIcon } from '../team/ModelIcon'
import { familyOf } from '../team/model-family'
import { formatClock } from '../../lib/conversations/deck-format'

export function AgentHeader({ model, at }: { model?: string; at: number }) {
  const name = model || 'Claude'
  return (
    <div data-testid="deck-agent-header" className="mb-1 flex items-center gap-1.5 text-xs text-text-secondary">
      <span className="text-accent-hover"><ModelIcon model={familyOf(model) ?? 'opus'} size={11} /></span>
      <span className="font-medium text-text-primary">{name}</span>
      <span>· {formatClock(at)}</span>
    </div>
  )
}

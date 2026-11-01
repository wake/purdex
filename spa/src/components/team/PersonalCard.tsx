// spa/src/components/team/PersonalCard.tsx — the personal panel's card (task groups spec §7, TG-5c; design R12–R15): the lead row's look for one session — 44 px avatar, agent icon with its light, host, name, title, context left with
// its ring — and the latest recap as the second line. The card is always the "active" row: it is the tab in view, so a click on it does nothing.
import { Avatar } from '../Avatar'
import { ContextRing } from './ModelIcon'
import { MODEL_LABEL } from './model-family'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { TeamSeatLight } from './TeamSeatLight'
import { SEAT_LIGHT_POSITION } from '../overlay-dot'
import { ctxLeftText, ctxTip } from './team-readings'
import { usePersonalNames, usePersonalReading } from './usePersonalPanel'
import { useI18nStore } from '../../stores/useI18nStore'
import type { PersonalTarget } from '../../lib/task-groups/personal'
import { RecapRowButton } from '../recap/RecapButton'
import { RecapLine } from '../recap/RecapLine'
import { openRecapView, type RecapTarget } from '../../stores/useRecapViewStore'

interface Props {
  target: PersonalTarget
}

export function PersonalCard({ target }: Props) {
  const t = useI18nStore((s) => s.t)
  const { name, title, persona } = usePersonalNames(target)
  const r = usePersonalReading(target)
  const ended = target.ended
  const ctx = ended ? undefined : r.ctx
  const model = ended ? '—' : r.model ? MODEL_LABEL[r.model] : r.modelRaw ?? '—'
  const tip = `${model} · ${ctxTip(ctx, t)}`
  const recapTarget: RecapTarget = { scope: target.tabId, teamKey: null, hostId: target.hostId, sessionId: target.agentSessionId, title: '', address: '' }
  return (
    <div
      data-testid="personal-card"
      data-ended={String(ended)}
      title={tip}
      className="mx-1.5 px-2 py-2 rounded-md grid grid-cols-[44px_minmax(0,1fr)] gap-x-2.5 bg-surface-active text-white [--seat-ring:var(--surface-active)]"
    >
      <div aria-hidden="true" className={`relative w-11 h-11 flex-shrink-0 ${ended ? 'opacity-40' : ''}`}>
        <Avatar size={44} code={persona?.code ?? ''} name={name} src={persona?.avatar} />
      </div>
      <div className="min-w-0">
        <div className="flex items-center gap-1.5">
          <span className="inline-flex items-center gap-1.5 text-text-muted">
            <span className={`relative inline-flex ${ended ? 'opacity-40' : ''}`}>
              <TeamSeatIcon hostId={target.hostId} sessionCode={target.sessionCode} isActive subagents light={false} />
              {!ended && <span data-testid="personal-card-light" className="absolute" style={SEAT_LIGHT_POSITION}><TeamSeatLight hostId={target.hostId} sessionCode={target.sessionCode} /></span>}
            </span>
            <TeamSeatHostBadge hostId={target.hostId} sessionCode={target.sessionCode} />
          </span>
          <span data-testid="personal-card-name" className={`${title !== '' ? 'flex-shrink-0 whitespace-nowrap' : 'truncate min-w-0'} text-[13px] font-semibold text-text-primary`}>{name}</span>
          {title !== '' && <span data-testid="personal-card-title" className="truncate min-w-0 flex-1 text-[11.5px] text-text-muted">{title}</span>}
          {title === '' && <span className="flex-1" />}
          {ended && <span data-testid="personal-card-ended" className="text-[9.5px] px-1 rounded border border-border-default flex-shrink-0 text-text-secondary">{t('personal.ended')}</span>}
          <span className="flex items-center gap-1 flex-shrink-0 text-text-primary">
            <span data-testid="personal-card-ctx" className="text-[10.5px] tabular-nums text-text-muted">{ctxLeftText(ctx)}</span>
            <ContextRing pct={ctx} model={ended ? undefined : r.model} size={16} />
          </span>
          <RecapRowButton target={recapTarget} />
        </div>
        {/* The card's second line: the newest recap's headline (an ended session's is the last one it left). */}
        <RecapLine hostId={target.hostId} sessionId={target.agentSessionId} ended={ended} onOpen={() => openRecapView(recapTarget)} />
      </div>
    </div>
  )
}

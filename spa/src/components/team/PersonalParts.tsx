// spa/src/components/team/PersonalParts.tsx — the pieces the personal panel's title-bar strip and one-line header share (task groups spec §7, TG-5b): the group's capsule, the session's avatar cell and its name + title.
// One copy, so the two places can not drift apart (the same rule as TeamCell.tsx, whose parts these are the personal twins of).
import { useMemo } from 'react'
import { TeamSeatIcon } from './TeamSeatIcon'
import { useSeatHostMain } from './useSeatHostMain'
import { ContextRing } from './ModelIcon'
import { MODEL_LABEL } from './model-family'
import { ctxTip } from './team-readings'
import { Avatar } from '../Avatar'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { UNGROUPED_GREY, type PersonalGroup, type PersonalTarget } from '../../lib/task-groups/personal'
import { usePersonalNames, usePersonalReading, usePersonalStatusLine } from './usePersonalPanel'

/** The text on a coloured capsule (the team capsule's). */
const ON_COLOR = '#14141f'

/**
 * The group's capsule: its name on its colour. 「未分組」 is neutral — a quiet fill, a thin border and the secondary text colour, none of the swatch colours (R24).
 * `data-testid="team-panel-name"` is what `useHeaderGestures` waits on for a double-click, as the team capsule is.
 */
export function GroupCapsule({ group, className = '', style }: { group: PersonalGroup; className?: string; style?: React.CSSProperties }) {
  const t = useI18nStore((s) => s.t)
  const label = group.kind === 'ungrouped' ? t('taskgroups.ungrouped') : group.name
  if (group.kind === 'ungrouped') {
    return (
      <span
        data-testid="team-panel-name"
        data-group-kind="ungrouped"
        className={`px-1.5 rounded text-[11px] leading-[18px] truncate border border-border-default bg-surface-hover text-text-secondary ${className}`}
        style={style}
        title={label}
      >
        {label}
      </span>
    )
  }
  const color = group.kind === 'manual' ? group.color ?? UNGROUPED_GREY : group.color
  return (
    <span
      data-testid="team-panel-name"
      data-group-kind={group.kind}
      className={`px-1.5 rounded text-[11px] font-semibold leading-[18px] truncate ${className}`}
      style={{ background: color, color: ON_COLOR, ...style }}
      title={label}
    >
      {label}
    </span>
  )
}

/** The one-line form's text: the name in the main text colour, the title after it in the secondary one; both give way to the width, the title first. */
export function PersonalNameLine({ target, className = '' }: { target: PersonalTarget; className?: string }) {
  const { name, title } = usePersonalNames(target)
  return (
    <span data-testid="personal-name-line" className={`flex items-baseline gap-1 min-w-0 text-[11px] leading-[18px] ${className}`} title={title !== '' ? `${name} ${title}` : name}>
      <span data-testid="personal-name" className="font-semibold text-text-primary truncate min-w-0 shrink-0 max-w-full">{name}</span>
      {title !== '' && <span data-testid="personal-title" className="text-text-muted truncate min-w-0">{title}</span>}
    </span>
  )
}

/**
 * The session's avatar cell: the persona's face (initials on the title with no persona) with the agent light, and the context ring. The tooltip carries the name, the model, what is left of the context and the last status line (R10);
 * an ended session is dim, with no light and a dash for the context (R2).
 */
export function PersonalCell({ target, isActive = true }: { target: PersonalTarget; isActive?: boolean }) {
  const t = useI18nStore((s) => s.t)
  const ringBox = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const hostMain = useSeatHostMain(target.hostId, target.sessionCode)
  const r = usePersonalReading(target)
  const { name, persona } = usePersonalNames(target)
  const line = usePersonalStatusLine(target)
  const ended = target.ended
  const ctx = ended ? undefined : r.ctx
  const model = ended ? '—' : r.model ? MODEL_LABEL[r.model] : r.modelRaw ?? '?'
  const tip = useMemo(
    () => [name, ended ? t('personal.ended') : model, ctxTip(ctx, t), line].filter((x) => x !== '').join(' · '),
    [name, ended, model, ctx, line, t],
  )
  return (
    <span
      data-testid="personal-cell"
      data-session-id={target.agentSessionId}
      data-ended={String(ended)}
      title={tip}
      className={`relative flex items-center gap-1.5 h-6 pl-1.5 pr-[3px] rounded-md ${isActive ? 'bg-surface-active text-white' : 'text-text-secondary'}`}
    >
      <span className={`inline-flex ${ended ? 'opacity-40' : ''}`}>
        <TeamSeatIcon
          hostId={target.hostId}
          sessionCode={target.sessionCode}
          isActive={isActive}
          subagents
          light={!ended}
          avatar={<Avatar size={18} code={persona?.code ?? ''} name={name} src={persona?.avatar} decorative />}
        />
      </span>
      <ContextRing pct={ctx} model={ended ? undefined : r.model} size={ringBox} symbolColor={hostMain} />
    </span>
  )
}

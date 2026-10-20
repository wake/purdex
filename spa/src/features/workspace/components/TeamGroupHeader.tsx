// spa/src/features/workspace/components/TeamGroupHeader.tsx — a team's header in the task-group view (name pool v2 spec §7, NA-6c): its name is renamed through the team's own edit form (`PUT /api/team/appearance`, the
// form the team panel opens too), not stored as a manual group. Opens on a double-click of the name when the lead's host offers `team.edit.v1` and the roster holds the team.
import { useRef, useState } from 'react'
import { TeamEditPopover } from '../../../components/team/TeamEditPopover'
import { useTeamRosterStore } from '../../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../../stores/useUnattendedStore'
import { TaskGroupHeader } from './TaskGroupHeader'

interface Props {
  hostId: string
  teamId: string
  color: string
  name: string
  selected?: boolean
  expanded?: boolean
  onClick?: () => void
  onToggle?: () => void
}

export function TeamGroupHeader({ hostId, teamId, color, name, selected, expanded, onClick, onToggle }: Props) {
  const editable = useUnattendedStore((s) => s.byHost[hostId]?.editSupport === 'yes')
  const roster = useTeamRosterStore((s) => s.byHost[hostId]?.find((r) => r.id === teamId))
  const [open, setOpen] = useState(false)
  const head = useRef<HTMLDivElement>(null)
  const canEdit = editable && roster !== undefined
  return (
    <>
      <TaskGroupHeader ref={head} color={color} name={name} selected={selected} expanded={expanded} onClick={onClick} onToggle={onToggle} strong onDoubleClickName={canEdit ? () => setOpen(true) : undefined} />
      {open && canEdit && (
        <TeamEditPopover
          target={{ hostId, teamId, name: roster.team_name, label: roster.team_label, color: roster.team_color ?? null }}
          anchor={() => head.current}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

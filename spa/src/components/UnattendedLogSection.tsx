// spa/src/components/UnattendedLogSection.tsx — the unattended panel's log (name pool v2 spec §8, U16; daemon `team.decision_log.v1`): the latest decisions of the team approvals
// and the switch's own on/off records, newest first, in a list that scrolls. A decision row: an 18 px avatar, the name (the team name for a lead request), the kind, a status tag —
// 自動通過 green, 你核准 blue, 自動拒絕 / 你拒絕 red, 逾時 yellow — and the time on the right. A toggle is a full-width pink-tinted row in bold: `無人值守開啟 · 由你 · 04:58`.
// What it draws is the entries handed in (already merged across hosts and cut to the newest 30 by the panel); it reads the persona store (avatars) and the rosters (team names).
import { Avatar } from './Avatar'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { approvalKindLabel, approvalSessionLabel } from '../lib/team/approval-format'
import { leadNameBySession } from '../lib/team/lead-name'
import { useSeat } from '../lib/names/seat'
import { sinceText } from '../lib/team/time-text'
import { TAG_CLASS, tagOf, type LogRow } from '../lib/team/unattended-log'
import type { ApprovalKind, Client, UnattendedLogDecision } from '../lib/team/types'

type T = (key: string, params?: Record<string, string | number>) => string

const KNOWN_KINDS: readonly string[] = ['lead', 'self_relay', 'member_relay', 'adopt']
function kindText(t: T, kind: string): string {
  return KNOWN_KINDS.includes(kind) ? approvalKindLabel(t, kind as ApprovalKind) : kind === 'spawn_resources' ? t('approval.kind.spawn_resources') : kind
}

/** 「你」 for the App's own switch, else what the daemon labelled the client. */
const whoOf = (t: T, by: Client): string => (by.kind === 'app' ? t('unattended.log.by_you') : by.label)

function DecisionRow({ hostId, entry, showHost }: { hostId: string; entry: UnattendedLogDecision; showHost: boolean }) {
  const t = useI18nStore((s) => s.t)
  const rosters = useTeamRosterStore((s) => s.byHost)
  // the daemon's resolved address only: `origin.name` is the registry's raw name, which a session sets itself and may end in any persona's code
  const seat = useSeat(entry.origin.address ?? '')
  const team = entry.kind === 'lead' ? leadNameBySession(rosters, entry.origin.session_id, entry.origin.title) : undefined
  const name = team?.name ?? seat?.name ?? approvalSessionLabel(entry.origin)
  const tag = tagOf(entry, t)
  return (
    <li data-testid="unattended-log-row" data-state={entry.state} className="flex items-center gap-2">
      <span aria-hidden="true" className="flex-shrink-0"><Avatar size={18} code={seat?.code ?? ''} name={name} src={seat?.avatar} /></span>
      <span data-testid="unattended-log-name" className="min-w-0 truncate font-medium text-text-primary">{name}</span>
      <span data-testid="unattended-log-kind" className="flex-shrink-0 text-text-muted">{kindText(t, entry.kind)}</span>
      <span data-testid="unattended-log-tag" data-tone={tag.tone} className={`flex-shrink-0 rounded px-1.5 text-[10.5px] ${TAG_CLASS[tag.tone]}`}>{tag.text}</span>
      <span className="ml-auto flex-shrink-0 text-text-muted tabular-nums">
        {showHost && <span data-testid="unattended-log-host" className="mr-1.5">{hostLabel(hostId, hostLookOf(hostId))}</span>}
        {sinceText(entry.at)}
      </span>
    </li>
  )
}

/** `multiHost`: more than one host is shown in the panel, so every row (a switch record too) names the host it belongs to. */
export function UnattendedLogSection({ rows, multiHost }: { rows: readonly LogRow[]; multiHost: boolean }) {
  const t = useI18nStore((s) => s.t)
  const showHost = multiHost
  return (
    <section data-testid="unattended-log-section" className="flex flex-col gap-2">
      <div data-testid="unattended-log-title" className="font-medium text-text-primary">{t('unattended.log.title')}</div>
      {rows.length === 0 ? (
        <div data-testid="unattended-log-empty" className="text-text-muted">{t('unattended.log.empty')}</div>
      ) : (
        <ul data-testid="unattended-log-list" className="flex max-h-64 flex-col gap-1.5 overflow-y-auto">
          {rows.map(({ hostId, entry }, i) => entry.type === 'toggle' ? (
            <li
              key={`${hostId}:t:${entry.at}:${i}`} data-testid="unattended-log-toggle" data-on={String(entry.on)}
              className="rounded bg-pink-500/15 px-2 py-0.5 font-bold text-text-primary"
            >
              {showHost && <span data-testid="unattended-log-host" className="mr-1.5 font-normal text-text-muted">{hostLabel(hostId, hostLookOf(hostId))}</span>}
              {t(entry.on ? 'unattended.log.toggle_on' : 'unattended.log.toggle_off', { who: whoOf(t, entry.by), time: sinceText(entry.at) })}
            </li>
          ) : (
            <DecisionRow key={`${hostId}:d:${entry.origin.session_id}:${entry.at}:${i}`} hostId={hostId} entry={entry} showHost={showHost} />
          ))}
        </ul>
      )}
    </section>
  )
}

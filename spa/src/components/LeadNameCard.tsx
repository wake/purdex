// spa/src/components/LeadNameCard.tsx — the 「名字」 row of the lead approval dialog (name pool v2 spec §5.3, §9.7, NA-0 design): pills with a 22 px avatar and the name.
// 「保留目前的名字（X）」 first and preselected; then the groups 「當過 lead」 and 「預設 lead」; the rest sits behind 「更多…（N 位）」. A refreshed list after a
// `name_taken` shows a one-line notice above the pills. Presentational: the dialog owns the list, the pick and the notice (it stays mounted while minimized).
import { useState } from 'react'
import { Avatar } from './Avatar'
import { useI18nStore } from '../stores/useI18nStore'
import { currentCollection, usePersonaStore } from '../stores/usePersonaStore'
import { useHostLook } from '../lib/host-look'
import { resolveHostColors } from '../lib/host-color'
import type { LeadNameCandidate } from '../lib/team/types'

interface Props {
  hostId: string
  /** The session's name now: the 「保留」 pill reads it. */
  currentName: string
  names: LeadNameCandidate[]
  /** The picked persona code, or null for 「保留」. */
  pick: string | null
  onPick: (code: string | null) => void
  /** The list was refreshed because the picked name was taken meanwhile. */
  taken: boolean
  disabled: boolean
}

export function LeadNameCard({ hostId, currentName, names, pick, onPick, taken, disabled }: Props) {
  const t = useI18nStore((s) => s.t)
  const collection = usePersonaStore((s) => currentCollection(s.byHost))
  const look = useHostLook(hostId === '' ? null : hostId)
  const faceColor = resolveHostColors({ colors: look.colors, color: look.color }, 'terminal')?.main ?? '#6b7280'
  const [more, setMore] = useState(false)
  const former = names.filter((n) => n.kind === 'former')
  const lead = names.filter((n) => n.kind === 'lead')
  const other = names.filter((n) => n.kind === 'other')
  // A pick that sits in the folded group keeps it open (a refresh must not hide the choice).
  const showOther = more || (pick !== null && other.some((n) => n.code === pick))

  const pill = (key: string, label: string, code: string, avatarCode: string, selected: boolean, onClick: () => void) => (
    <button
      key={key} type="button" role="radio" aria-checked={selected} disabled={disabled} onClick={onClick} data-testid="lead-name-pill" data-code={code}
      className={`inline-flex max-w-full items-center gap-1.5 rounded-full border py-0.5 pl-0.5 pr-2 text-xs ${selected ? 'border-accent bg-surface-hover text-text-primary' : 'border-border-subtle text-text-secondary hover:bg-surface-hover'} disabled:opacity-50`}
    >
      <Avatar size={22} code={avatarCode} name={label} src={collection?.items.find((p) => p.code === avatarCode)?.avatar_data} fallbackColor={faceColor} />
      <span className="truncate">{label}</span>
    </button>
  )
  const group = (testid: string, title: string, items: LeadNameCandidate[]) => items.length === 0 ? null : (
    <div data-testid={testid} className="mt-1.5">
      <div className="mb-1 text-[10.5px] text-text-muted">{title}</div>
      <div className="flex flex-wrap gap-1.5">{items.map((n) => pill(n.code, n.name, n.code, n.code, pick === n.code, () => onPick(n.code)))}</div>
    </div>
  )

  return (
    <div data-testid="lead-name-card" className="mt-3 text-xs text-text-secondary">
      <div className="mb-1">{t('approval.dialog.lead_name')}</div>
      {taken && <p data-testid="lead-name-taken" role="alert" className="mb-1 text-status-warning-text">{t('approval.dialog.lead_name_taken')}</p>}
      <div role="radiogroup" aria-label={t('approval.dialog.lead_name')}>
        <div className="flex flex-wrap gap-1.5">{pill('keep', t('approval.dialog.lead_name_keep', { name: currentName }), '', '', pick === null, () => onPick(null))}</div>
        {group('lead-name-former', t('approval.dialog.lead_name_former'), former)}
        {group('lead-name-lead', t('approval.dialog.lead_name_lead'), lead)}
        {other.length > 0 && (
          <div className="mt-1.5">
            <button type="button" data-testid="lead-name-more" aria-expanded={showOther} disabled={disabled} onClick={() => setMore((v) => !v)} className="cursor-pointer text-[10.5px] text-text-muted hover:text-text-primary">
              {showOther ? t('approval.dialog.lead_name_less') : t('approval.dialog.lead_name_more', { n: other.length })}
            </button>
            {showOther && <div data-testid="lead-name-other" className="mt-1 flex flex-wrap gap-1.5">{other.map((n) => pill(n.code, n.name, n.code, n.code, pick === n.code, () => onPick(n.code)))}</div>}
          </div>
        )}
      </div>
    </div>
  )
}

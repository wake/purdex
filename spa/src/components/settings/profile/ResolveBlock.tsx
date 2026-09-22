// spa/src/components/settings/profile/ResolveBlock.tsx — Settings › Profile › Current › the locked sections: one row
// per `status.locks` entry, in the section list's order, each with the ways out its kind has (P3d-4 plan, the Resolve
// block). Mounted by `CurrentBlock` where the "the panel arrives in the next version" sentence was.
//
// Profile-level states are NOT rows: `locked:schema` (update Purdex) and a gone profile keep their sentences above.
// The rows' state is per row (ResolveRow.tsx); a row is keyed by the master and the section, so a master that
// changes takes every open confirmation, every "sent", with it.
import { useI18nStore } from '../../../stores/useI18nStore'
import type { ExecutorStatus } from '../../../lib/profile/executor'
import type { SectionView } from '../../../lib/profile/sync-view'
import { ResolveRow } from './ResolveRow'

interface Props {
  master: { hostId: string; profileId: string }
  status: ExecutorStatus
  /** `describeSections` of the status' sections: the order and the labels of the section list above. */
  views: readonly SectionView[]
  labelOf: (view: SectionView) => string
  /** A follower window: every row carries the "reported by the window that is syncing" badge. */
  fromLeader: boolean
  /** No driver runs (`blocked`): a press would do nothing. */
  disabled: boolean
}

export function ResolveBlock({ master, status, views, labelOf, fromLeader, disabled }: Props) {
  const t = useI18nStore((s) => s.t)
  const locked = views.filter((view) => Object.hasOwn(status.locks, view.key))
  if (locked.length === 0) return null
  return (
    <div data-testid="profile-resolve-block" className="mt-3">
      <h4 className="text-xs text-text-secondary">{t('settings.profile.resolve.title')}</h4>
      <p className="text-xs text-text-muted">{t('settings.profile.resolve.desc')}</p>
      <ul className="mt-1 flex flex-col">
        {locked.map((view) => (
          <ResolveRow
            key={JSON.stringify([master.hostId, master.profileId, view.key])}
            master={master}
            sectionKey={view.key}
            kind={view.kind}
            label={labelOf(view)}
            lock={status.locks[view.key]}
            invalidReason={status.detail[view.key]?.invalidReason ?? null}
            fromLeader={fromLeader}
            disabled={disabled}
          />
        ))}
      </ul>
    </div>
  )
}

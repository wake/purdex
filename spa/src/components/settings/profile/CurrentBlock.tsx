// spa/src/components/settings/profile/CurrentBlock.tsx — Settings › Profile › Current: what the master syncs
// with, how that is going, and the three things a user can do about it (Auto-sync, Sync now, Stop sync).
//
// THE SYNC STATE COMES FROM `useProfileSync()` AND NOWHERE ELSE (P3 plan, P3a Task 2): in the leader window it
// is this window's own, in a follower it is what the leader published — labelled so, and `stale` said in words.
// The preferences (`autoSync`, the endpoint the master was attached at) are `useProfileStore`'s.
//
// WITHOUT A MASTER THIS IS STATIC TEXT. `useProfileSync()` then costs one Set entry (no storage, no listener, no
// timer — start.ts, THE IRON RULE); everything else this file subscribes to lives in `Attached`, which is not
// mounted. Nothing in either half starts a timer.
//
// ONE <section>, THREE CHILDREN IN FIXED PLACES: the half that depends on the master (`Attached` | the two
// sentences), `StopSyncControl`, the problem log. `StopSyncControl` sits OUTSIDE the first on purpose — its
// confirmation must stay up while the master goes (see that file) — and keeps its place in the tree, so React
// keeps its state across the change.
//
// WHAT THE SNAPSHOT DOES NOT CARRY, and this block therefore does not show: a revision per section (only a
// LOCKED section has one, `locks[key].sot.rev`) and the time of the last sync. Both would have to be published
// by the executor; neither is guessed from anything else.
import { useState, useSyncExternalStore } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useProfileStore } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../features/workspace/store'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { requestSyncNow } from '../../../lib/profile/start'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'
import { readMasterWorld, type UnsettledReason } from '../../../lib/profile/master-world'
import { SYNC_DOT_CLASS, describeSections, settingsWaitForWorkspaces, syncDotOf, type SectionView } from '../../../lib/profile/sync-view'
import { SettingItem } from '../SettingItem'
import { ToggleSwitch } from '../ToggleSwitch'
import { StopSyncControl } from './StopSyncControl'

const BTN =
  'shrink-0 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
const BADGE = 'rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary'
/** A state the user should know of, not a fault: the tone of `DeviceStateSection`'s "offline". */
const NOTICE = 'mt-2 text-xs text-yellow-500'
const PROBLEMS_SHOWN = 5

/** The three stores `readMasterWorld` reads. Plain store subscriptions: no timer, and no recovery asked from here. */
function subscribeWorld(fn: () => void): () => void {
  const stops = [useTabStore.subscribe(fn), useWorkspaceStore.subscribe(fn), useLocalProfilesStore.subscribe(fn)]
  return () => {
    for (const stop of stops) stop()
  }
}

/** A string (or null = settled), so that `useSyncExternalStore` sees a change only when there is one. */
function worldReason(): UnsettledReason | null {
  const read = readMasterWorld()
  return read.settled ? null : read.reason
}

/** The MASTER world's workspaces — parked while a slave is on screen — or null while nobody can say. The array is
 *  the store's own, so its identity moves only when the list did. */
function masterWorkspaces(): readonly { id: string; name: string }[] | null {
  const read = readMasterWorld()
  return read.settled ? read.world.workspaces : null
}

/** `epoch-mismatch` / `world-mismatch` / `behind-fence` heal with the next rehydrate: one sentence for the three. */
const WORLD_KEY: Record<UnsettledReason, string> = {
  'epoch-mismatch': 'settings.profile.current.world.catching_up',
  'world-mismatch': 'settings.profile.current.world.catching_up',
  'behind-fence': 'settings.profile.current.world.catching_up',
  'junk-epoch': 'settings.profile.current.world.junk_epoch',
  'no-parked-master': 'settings.profile.current.world.no_parked_master',
}

interface Props {
  /** The SOT profile's name, from the host's list when it has answered; null → the id stands in. */
  masterName: string | null
}

export function CurrentBlock({ masterName }: Props) {
  const t = useI18nStore((s) => s.t)
  const sync = useProfileSync()
  const problems = sync.master === null ? [] : sync.problems.slice(-PROBLEMS_SHOWN).reverse()

  return (
    <section data-testid="profile-current-block" data-state={sync.master === null ? 'none' : 'attached'} className="mt-6">
      <h3 className="text-sm text-text-primary">{t('settings.profile.current.title')}</h3>
      {sync.master === null ? (
        <div>
          <p className="text-xs text-text-secondary">{t('settings.profile.current.none_what')}</p>
          <p className="text-xs text-text-secondary">{t('settings.profile.current.none_how')}</p>
          {/* TODO(P3d-3): the wizard's entry goes HERE, under the two sentences — a button labelled
              `settings.profile.current.setup`, testid `profile-setup-start`, opening the wizard. Not before the
              wizard exists: a control that leads nowhere is worse than none. */}
        </div>
      ) : (
        <Attached sync={sync} master={sync.master} masterName={masterName} />
      )}
      <StopSyncControl attached={sync.master !== null} />
      {problems.length > 0 && (
        <div data-testid="profile-current-problems" className="mt-4">
          <h4 className="text-xs text-text-secondary">{t('settings.profile.current.problems')}</h4>
          <p className="text-xs text-text-muted">{t('settings.profile.current.problems_desc')}</p>
          <ul className="mt-1 flex flex-col gap-0.5">
            {problems.map((p, i) => (
              <li key={`${p.at}:${i}`} data-testid="profile-current-problem" className="text-xs text-text-muted">
                <span className="font-mono">{new Date(p.at).toLocaleTimeString()}</span>{' '}
                <span className="font-mono text-text-secondary">{p.section === undefined ? p.kind : `${p.kind} · ${p.section}`}</span>{' '}
                {p.detail}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}

function Attached({ sync, master, masterName }: { sync: ProfileSyncSnapshot; master: NonNullable<ProfileSyncSnapshot['master']>; masterName: string | null }) {
  const t = useI18nStore((s) => s.t)
  const autoSync = useProfileStore((s) => s.autoSync)
  const setAutoSync = useProfileStore((s) => s.setAutoSync)
  const attachedAt = useProfileStore((s) => s.masterEndpoint)
  const host = useHostStore((s) => s.hosts[master.hostId])
  const world = useSyncExternalStore(subscribeWorld, worldReason, worldReason)
  const workspaces = useSyncExternalStore(subscribeWorld, masterWorkspaces, masterWorkspaces)
  /** The snapshot a "Sync now" was pressed under: the note stays until something in the state moves. No timer. */
  const [askedUnder, setAskedUnder] = useState<ProfileSyncSnapshot | null>(null)

  // `syncDotOf` cannot answer null here: there is a master.
  const dot = syncDotOf(sync) ?? 'unknown'
  const source = sync.remote ? 'leader' : 'this-window'
  const fromLeader = sync.remote && (
    <span data-testid="profile-current-source" className={BADGE}>{t('settings.profile.current.from_leader')}</span>
  )
  const sections = sync.status === null ? [] : describeSections(Object.keys(sync.status.sections), workspaces)
  const anyLocked = sync.status !== null && Object.keys(sync.status.locks).length > 0
  const schemaLock = sync.status?.schemaLock ?? null

  const sectionLabel = (view: SectionView): string => {
    if (view.kind === 'other') return view.key // a kind this build does not know: nothing better to call it
    if (view.kind !== 'tabs') return t(`settings.profile.current.label.${view.kind}`)
    if (view.workspace === undefined) return t('settings.profile.current.label.tabs_unknown')
    // A workspace's name is the user's own text: into the sentence as it is.
    return view.workspace === null ? t('settings.profile.current.label.tabs_unseen') : t('settings.profile.current.label.tabs', { workspace: view.workspace })
  }

  const blockedText = (): string => {
    switch (sync.blocked) {
      case 'master-endpoint-changed':
        return t('settings.profile.current.blocked.endpoint_changed', { was: attachedAt ?? '', now: host ? `${host.ip}:${host.port}` : '' })
      case 'profile-gone':
        return t('settings.profile.current.blocked.profile_gone')
      case 'suspended':
        return t('settings.profile.current.blocked.suspended')
      default:
        return ''
    }
  }

  return (
    <div>
      <SettingItem label={t('settings.profile.current.master')} description={t('settings.profile.current.master_desc')}>
        <div className="flex flex-col items-end gap-0.5 text-xs">
          <span data-testid="profile-current-master" className={masterName === null ? 'font-mono text-text-secondary' : 'text-text-primary'}>
            {masterName ?? master.profileId}
          </span>
          <span data-testid="profile-current-host" className="text-text-muted">
            {t('settings.profile.current.on_host', { host: host?.name ?? master.hostId })}
          </span>
        </div>
      </SettingItem>

      <SettingItem label={t('settings.profile.current.state')}>
        <div className="flex items-center gap-2 text-xs">
          {fromLeader}
          <span data-testid="profile-current-state" data-state={dot} data-source={source} className="flex items-center gap-1.5 text-text-primary">
            <span aria-hidden="true" className={`w-1.5 h-1.5 rounded-full ${SYNC_DOT_CLASS[dot]}`} />
            {t(`profile.sync.${dot}`)}
          </span>
        </div>
      </SettingItem>

      {sync.remote && sync.stale && (
        <p data-testid="profile-current-stale" className={NOTICE}>{t('settings.profile.current.stale')}</p>
      )}
      {sync.blocked !== null && (
        <p data-testid="profile-current-blocked" data-reason={sync.blocked} className={NOTICE}>{blockedText()}</p>
      )}
      {world !== null && (
        // A state, not an error (P3 plan, "What the UI must say"). TODO(P3d-3): for `no-parked-master` the way out
        // is the wizard — link to it from this sentence once it exists.
        <p data-testid="profile-current-world" data-reason={world} className="mt-2 text-xs text-text-secondary">{t(WORLD_KEY[world])}</p>
      )}
      {schemaLock !== null && (
        <p data-testid="profile-current-schema" className={NOTICE}>{t(`settings.profile.current.schema.${schemaLock.verdict}`)}</p>
      )}
      {settingsWaitForWorkspaces(sync.status) && (
        <p data-testid="profile-current-settings-waiting" className={NOTICE}>{t('settings.profile.current.settings_waiting')}</p>
      )}

      <div data-testid="profile-current-sections" className="mt-4">
        <div className="mb-1 flex items-center gap-2">
          <h4 className="text-xs text-text-secondary">{t('settings.profile.current.sections')}</h4>
          {fromLeader}
        </div>
        {sync.status === null ? (
          <p data-testid="profile-current-no-status" className="text-xs text-text-muted">
            {t(sync.remote || !sync.leader ? 'settings.profile.current.no_status_follower' : 'settings.profile.current.no_status')}
          </p>
        ) : (
          <ul className="flex flex-col">
            {sections.map((view) => {
              const { key } = view
              const state = sync.status!.sections[key]
              const lock = sync.status!.locks[key]
              return (
                <li
                  key={key}
                  data-testid={`profile-current-section-${key}`}
                  data-section={key}
                  data-status={state}
                  data-source={source}
                  className="flex flex-wrap items-center justify-between gap-2 border-t border-border-default py-1.5 text-xs"
                >
                  {/* The raw key is for whoever needs it (a bug report, the acceptance run): the tooltip. */}
                  <span title={key} className="text-text-primary">{sectionLabel(view)}</span>
                  <span className="flex items-center gap-2 text-text-secondary">
                    {lock !== undefined && (
                      <span data-testid={`profile-current-section-rev-${key}`} className="text-text-muted">
                        {t('settings.profile.current.sot_rev', { rev: lock.sot.rev })}
                      </span>
                    )}
                    <span className={state.startsWith('locked:') ? 'text-yellow-500' : undefined}>{t(`settings.profile.current.section.${state.replace(':', '_')}`)}</span>
                  </span>
                </li>
              )
            })}
          </ul>
        )}
        {anyLocked && (
          // TODO(P3d-4): the Resolve block replaces this sentence — one row per `status.locks` entry, handing
          // `requestResolve` the very `SectionLock` it rendered.
          <p data-testid="profile-current-locked-note" className="mt-1 text-xs text-text-muted">{t('settings.profile.current.locked_note')}</p>
        )}
      </div>

      <SettingItem label={t('settings.profile.current.auto_sync')} description={t('settings.profile.current.auto_sync_desc')}>
        <ToggleSwitch testId="profile-auto-sync" label={t('settings.profile.current.auto_sync')} checked={autoSync} onChange={setAutoSync} />
      </SettingItem>

      <SettingItem label={t('settings.profile.current.sync_now')} description={t('settings.profile.current.sync_now_desc')}>
        <button
          type="button"
          data-testid="profile-sync-now"
          // While blocked no window runs a driver: the press would do nothing, and the reason is said above.
          disabled={sync.blocked !== null}
          onClick={() => {
            requestSyncNow()
            setAskedUnder(sync)
          }}
          className={BTN}
        >
          <ArrowsClockwise size={14} />
          {t('settings.profile.current.sync_now')}
        </button>
      </SettingItem>
      {askedUnder === sync && (
        <p data-testid="profile-sync-asked" className="text-xs text-text-secondary">
          {t(sync.remote ? 'settings.profile.current.sync_asked_follower' : 'settings.profile.current.sync_asked')}
        </p>
      )}
    </div>
  )
}

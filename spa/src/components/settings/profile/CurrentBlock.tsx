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
// THE WIZARD (P3d-3) OPENS IN PLACE of the master-dependent half — no modal: the settings pages have no
// multi-step flow to copy one from, and the plan forbids a new primitive. "Open" is this component's state and
// nothing else's: leaving the page closes it, nothing is persisted, and it does NOT follow the master — the
// wizard's first step clears the master and its last sets one. While it is open the plain Stop sync is not
// offered beside it (the wizard's first step is that very call); `StopSyncControl` stays mounted, because the
// notice of a host that was not told is its to show — the wizard must not hide that outcome.
//
// A PULL STOPPED BECAUSE THE HOSTS MOVED (#1366; `useProfileStore.pullUnconfirmed`): one sentence with a Dismiss,
// above whatever half is shown — after the stop that is the no-master half, whose "Set up sync…" is the way on. It
// is device-local and outlives the stop; it goes with Dismiss, with a new attach (the store clears it), and out of
// sight while the wizard — setting sync up again — is open.
//
// WHAT THE EXECUTOR PUBLISHES BEYOND THE STATUS (P3d-4a) — `detail`, `indexFailures`, `lastSuccessAt`,
// `profileGone` — is shown as it is: the AGREED rev on every row (a locked row keeps the host's beside it), a
// failing row's next try, "in sync as of", why settings wait. A record from an older build has none of them and
// the page then shows nothing for them — never "0" or "never" (sync-status.ts reads them as absent). Times are
// absolute local times IN THE UI LANGUAGE (`dateLocale`; P3d-4c F5 — the browser's showed 「上午」 in an English UI):
// no clock runs on this page — except a "sent" Resolve row's, bounded by the command's TTL (ResolveRow.tsx).
import { useState, useSyncExternalStore } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { getLocale } from '../../../lib/locale-registry'
import { endpointOfHost, useProfileStore } from '../../../stores/useProfileStore'
import { selectDaemonIdMismatch, useHostStore } from '../../../stores/useHostStore'
import { identityOfSync } from '../../../lib/profile/host-identity'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../features/workspace/store'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { requestSyncNow } from '../../../lib/profile/start'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'
import { readMasterWorld, type UnsettledReason } from '../../../lib/profile/master-world'
import {
  SYNC_DOT_CLASS,
  describeSections,
  heldByAutoSyncOff,
  profileIsGone,
  sectionHeldByAutoSyncOff,
  settingsWaitForWorkspaces,
  syncDotOf,
  type SectionView,
} from '../../../lib/profile/sync-view'
import { SettingItem } from '../SettingItem'
import { ToggleSwitch } from '../ToggleSwitch'
import { ResolveBlock } from './ResolveBlock'
import { StopSyncControl } from './StopSyncControl'
import { ProfileWizard } from './wizard/ProfileWizard'

const BTN =
  'shrink-0 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
const BADGE = 'rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary'
/** A state the user should know of, not a fault: yellow, never red — the tone of the Resolve rows' notices too. */
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

/**
 * The UI language as a tag `Date#toLocale*` takes. A built-in locale's id IS one (`en`, `zh-TW`); a user-imported
 * locale's id is random and names no language, and its missing keys fall back to English — so do its times. Never
 * `undefined`: that is the browser's language, which is not the one the page is written in.
 */
function dateLocaleOf(localeId: string): string {
  return getLocale(localeId)?.builtin === true ? localeId : 'en'
}

function useDateLocale(): string {
  return dateLocaleOf(useI18nStore((s) => s.activeLocaleId))
}

interface Props {
  /** The SOT profile's name, from the host's list when it has answered; null → the id stands in. */
  masterName: string | null
}

export function CurrentBlock({ masterName }: Props) {
  const t = useI18nStore((s) => s.t)
  const dateLocale = useDateLocale()
  const sync = useProfileSync()
  const [wizardOpen, setWizardOpen] = useState(false)
  const pullUnconfirmed = useProfileStore((s) => s.pullUnconfirmed)
  const clearPullUnconfirmed = useProfileStore((s) => s.clearPullUnconfirmed)
  const problems = sync.master === null ? [] : sync.problems.slice(-PROBLEMS_SHOWN).reverse()

  return (
    <section data-testid="profile-current-block" data-state={sync.master === null ? 'none' : 'attached'} className="mt-6">
      <h3 className="text-sm text-text-primary">{t('settings.profile.current.title')}</h3>
      {pullUnconfirmed !== null && !wizardOpen && (
        <div data-testid="profile-pull-unconfirmed" className="mt-2 flex items-start justify-between gap-3">
          <p className="text-xs text-yellow-500">{t('settings.profile.current.pull_unconfirmed')}</p>
          <button type="button" data-testid="profile-pull-unconfirmed-dismiss" onClick={clearPullUnconfirmed} className={BTN}>
            {t('settings.profile.current.pull_unconfirmed_dismiss')}
          </button>
        </div>
      )}
      {wizardOpen ? (
        <ProfileWizard onClose={() => setWizardOpen(false)} />
      ) : sync.master === null ? (
        <div>
          <p className="text-xs text-text-secondary">{t('settings.profile.current.none_what')}</p>
          <p className="text-xs text-text-secondary">{t('settings.profile.current.none_how')}</p>
          <button type="button" data-testid="profile-setup-start" onClick={() => setWizardOpen(true)} className={`mt-3 ${BTN}`}>
            {t('settings.profile.current.setup')}
          </button>
        </div>
      ) : (
        <>
          <Attached sync={sync} master={sync.master} masterName={masterName} />
          <SettingItem label={t('settings.profile.current.change')} description={t('settings.profile.current.change_desc')}>
            <button type="button" data-testid="profile-setup-change" onClick={() => setWizardOpen(true)} className={BTN}>
              {t('settings.profile.current.change')}
            </button>
          </SettingItem>
        </>
      )}
      <StopSyncControl attached={sync.master !== null && !wizardOpen} />
      {problems.length > 0 && (
        <div data-testid="profile-current-problems" className="mt-4">
          <h4 className="text-xs text-text-secondary">{t('settings.profile.current.problems')}</h4>
          <p className="text-xs text-text-muted">{t('settings.profile.current.problems_desc')}</p>
          <ul className="mt-1 flex flex-col gap-0.5">
            {problems.map((p, i) => (
              <li key={`${p.at}:${i}`} data-testid="profile-current-problem" className="text-xs text-text-muted">
                <span className="font-mono">{new Date(p.at).toLocaleTimeString(dateLocale)}</span>{' '}
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
  const dateLocale = useDateLocale()
  const autoSync = useProfileStore((s) => s.autoSync)
  const setAutoSync = useProfileStore((s) => s.setAutoSync)
  const attachedAt = useProfileStore((s) => s.masterEndpoint)
  const host = useHostStore((s) => s.hosts[master.hostId])
  // Every host, for the sentence that names the one(s) the sync is paused on (`blocked: 'host-identity-*'`).
  const allHosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const world = useSyncExternalStore(subscribeWorld, worldReason, worldReason)
  const workspaces = useSyncExternalStore(subscribeWorld, masterWorkspaces, masterWorkspaces)
  /** The snapshot a "Sync now" was pressed under: the note stays until something in the state moves. No timer. */
  const [askedUnder, setAskedUnder] = useState<ProfileSyncSnapshot | null>(null)

  // `syncDotOf` cannot answer null here: there is a master.
  const dot = syncDotOf(sync) ?? 'unknown'
  // Auto-sync off: `pending` waits for the user, it is not being synced (P3d-4c F2). The raw state stays in data-*.
  const held = heldByAutoSyncOff(sync, autoSync)
  const source = sync.remote ? 'leader' : 'this-window'
  const fromLeader = sync.remote && (
    <span data-testid="profile-current-source" className={BADGE}>{t('settings.profile.current.from_leader')}</span>
  )
  const sections = sync.status === null ? [] : describeSections(Object.keys(sync.status.sections), workspaces)
  const schemaLock = sync.status?.schemaLock ?? null
  // A gone profile has two sources (a 404; the index no longer listing it) and ONE sentence: read alike.
  const blocked = sync.blocked ?? (profileIsGone(sync) ? 'profile-gone' : null)
  // While `blocked` (the profile gone included) no driver runs: nothing is waited for and no retry is armed,
  // whatever the last figures say (review F2).
  // A STALE follower's figures are the last ones a leader that is gone reported (review F3): a failure and its
  // "next try" were promises of that leader's executor, so they are not shown at all — the simpler of the two
  // choices, and the stale sentence says why. A lock is a fact, not a promise: a lock-based wait is still said.
  const live = !(sync.remote && sync.stale)
  const waitReason = blocked === null ? settingsWaitForWorkspaces(sync.status) : null
  const waiting = waitReason === 'failing' && !live ? null : waitReason
  const lastSuccessAt = sync.status?.lastSuccessAt ?? null

  const sectionLabel = (view: SectionView): string => {
    if (view.kind === 'other') return view.key // a kind this build does not know: nothing better to call it
    if (view.kind !== 'tabs') return t(`settings.profile.current.label.${view.kind}`)
    if (view.workspace === undefined) return t('settings.profile.current.label.tabs_unknown')
    // A workspace's name is the user's own text: into the sentence as it is.
    return view.workspace === null ? t('settings.profile.current.label.tabs_unseen') : t('settings.profile.current.label.tabs', { workspace: view.workspace })
  }

  const blockedText = (): string => {
    switch (blocked) {
      case 'master-endpoint-changed':
        return t('settings.profile.current.blocked.endpoint_changed', { was: attachedAt ?? '', now: host ? endpointOfHost(host) : '' })
      case 'profile-gone':
        return t('settings.profile.current.blocked.profile_gone')
      case 'host-identity-mismatch':
      case 'host-identity-conflict': {
        // Which hosts: read off THIS window's store — the conflict is synced config (the same everywhere), a
        // mismatch this window's runtime; a follower that does not see what its leader saw says it without names.
        const ids = blocked === 'host-identity-conflict'
          ? (identityOfSync(allHosts).conflict ?? [])
          : hostOrder.filter((id) => allHosts[id] !== undefined && selectDaemonIdMismatch({ hosts: allHosts, runtime }, id) !== undefined)
        const key = blocked.replace(/-/g, '_')
        // Host names are the user's own text: into the sentence as they are.
        return ids.length > 0
          ? t(`settings.profile.current.blocked.${key}`, { hosts: ids.map((id) => allHosts[id]?.name ?? id).join(', ') })
          : t(`settings.profile.current.blocked.${key}_unnamed`)
      }
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
          <span
            data-testid="profile-current-state"
            data-state={dot}
            data-held={held ? 'auto-sync-off' : undefined}
            data-source={source}
            className="flex items-center gap-1.5 text-text-primary"
          >
            <span aria-hidden="true" className={`w-1.5 h-1.5 rounded-full ${SYNC_DOT_CLASS[dot]}`} />
            {t(held ? 'profile.sync.held' : `profile.sync.${dot}`)}
          </span>
        </div>
      </SettingItem>
      {lastSuccessAt !== null && (
        <p data-testid="profile-current-last-sync" className="text-xs text-text-muted">
          {t('settings.profile.current.last_sync', { time: new Date(lastSuccessAt).toLocaleString(dateLocale) })}
        </p>
      )}

      {sync.remote && sync.stale && (
        <p data-testid="profile-current-stale" className={NOTICE}>{t('settings.profile.current.stale')}</p>
      )}
      {blocked !== null && (
        <p data-testid="profile-current-blocked" data-reason={blocked} className={NOTICE}>{blockedText()}</p>
      )}
      {world !== null && (
        // A state, not an error (P3 plan, "What the UI must say"). NOT linked to the wizard (P3d-3 looked): for
        // `no-parked-master` the wizard can do nothing — a promote and the copy both refuse an unsettled world —
        // and refuses to start, saying what does help (a reload: the store's `merge` repairs it).
        <p data-testid="profile-current-world" data-reason={world} className="mt-2 text-xs text-text-secondary">{t(WORLD_KEY[world])}</p>
      )}
      {schemaLock !== null && (
        <p data-testid="profile-current-schema" className={NOTICE}>{t(`settings.profile.current.schema.${schemaLock.verdict}`)}</p>
      )}
      {waiting !== null && (
        <p data-testid="profile-current-settings-waiting" data-reason={waiting} className={NOTICE}>{t(`settings.profile.current.settings_waiting_${waiting}`)}</p>
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
              const detail = sync.status!.detail[key] // absent: a record from an older build — nothing is shown for it
              const rowHeld = sectionHeldByAutoSyncOff(sync, state, autoSync)
              return (
                <li
                  key={key}
                  data-testid={`profile-current-section-${key}`}
                  data-section={key}
                  data-status={state}
                  data-held={rowHeld ? 'auto-sync-off' : undefined}
                  data-source={source}
                  className="flex flex-wrap items-center justify-between gap-2 border-t border-border-default py-1.5 text-xs"
                >
                  {/* The raw key is for whoever needs it (a bug report, the acceptance run): the tooltip. */}
                  <span title={key} className="text-text-primary">{sectionLabel(view)}</span>
                  <span className="flex flex-wrap items-center gap-2 text-text-secondary">
                    {blocked === null && live && detail !== undefined && detail.failures > 0 && (
                      // A state that heals by itself (the retry is armed), not an error: the notice tone.
                      <span data-testid={`profile-current-section-failing-${key}`} className="text-yellow-500">
                        {detail.retryAt === null
                          ? t('settings.profile.current.section_failing')
                          : t('settings.profile.current.section_failing_at', { time: new Date(detail.retryAt).toLocaleTimeString(dateLocale) })}
                      </span>
                    )}
                    {detail !== undefined && detail.rev !== null && (
                      <span data-testid={`profile-current-section-rev-${key}`} className="text-text-muted">
                        {t('settings.profile.current.rev', { rev: detail.rev })}
                      </span>
                    )}
                    {lock !== undefined && (
                      <span data-testid={`profile-current-section-sot-rev-${key}`} className="text-text-muted">
                        {t('settings.profile.current.sot_rev', { rev: lock.sot.rev })}
                      </span>
                    )}
                    <span className={state.startsWith('locked:') ? 'text-yellow-500' : undefined}>{t(rowHeld ? 'settings.profile.current.section.held' : `settings.profile.current.section.${state.replace(':', '_')}`)}</span>
                  </span>
                </li>
              )
            })}
          </ul>
        )}
        {sync.status !== null && (
          // One row per `status.locks` entry (P3d-4b): each confirmation hands `requestResolve` the very lock it was
          // opened with. A profile-level state (`locked:schema`, a gone profile) is a sentence above, not a row.
          <ResolveBlock
            master={master}
            status={sync.status}
            views={sections}
            labelOf={sectionLabel}
            fromLeader={sync.remote}
            disabled={blocked !== null}
          />
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
          disabled={blocked !== null}
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

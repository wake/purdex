// spa/src/components/settings/profile/wizard/WizardChoiceSteps.tsx — the wizard's three steps that only CHOOSE
// (the host and its profile · the local profile · the direction). Nothing in this file does anything: every
// choice is handed up to ProfileWizard.tsx, which owns the order, the premises and the run.
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../../stores/useI18nStore'
import { selectDaemonIdMismatch, useHostStore } from '../../../../stores/useHostStore'
import { hostLabel, hostLookOf } from '../../../../lib/host-look'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../../stores/useLocalProfilesStore'
import type { SyncDirection } from '../../../../stores/useProfileStore'
import type { UnsettledReason } from '../../../../lib/profile/master-world'
import type { SotProfilesView } from '../useSotProfiles'
import type { SotDelete } from '../useSotDelete'
import type { Attachment } from '../../../../lib/profile/api'
import { countWorld, offeredProfileName, worldToBeMaster, type PullPremiseReason } from './wizard-run'
import { BTN, INPUT, NOTICE, requestKey } from './wizard-shared'

const BADGE = 'rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary'
const CHOICE = 'flex items-center gap-2 py-1 text-xs text-text-primary cursor-pointer'
const RADIO = 'accent-border-active'

const deviceNames = (attachments: Attachment[]): string => attachments.map((a) => a.deviceName).join(', ')

/** `name` of an existing choice: what the wizard calls a profile it has just created, until the list has it. */
export type SotChoice = { kind: 'existing'; id: string; name?: string } | { kind: 'new' }

// === Step 2 ===

interface SotStepProps {
  hostId: string | null
  onHost: (hostId: string) => void
  /** Null: no host to ask. */
  view: SotProfilesView | null
  reload: () => void
  choice: SotChoice | null
  onChoice: (choice: SotChoice) => void
  newName: string
  onNewName: (name: string) => void
  /** A create that did not end in a profile: what is known of it (wizard-run.ts, `CreateResult`) and the failure's class. */
  createError: { outcome: 'failed' | 'not-created' | 'unknown' | 'same-name'; request: string } | null
  /** The profile that MAY be the one a create of unknown outcome made: marked, never chosen for the user. */
  maybeId: string | null
  /** Deleting a profile nobody is attached to (useSotDelete.ts; its dialog is the wizard's). */
  del: SotDelete
  disabled: boolean
}

export function SotStep({ hostId, onHost, view, reload, choice, onChoice, newName, onNewName, createError, maybeId, del, disabled }: SotStepProps) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const listed = hostOrder.filter((id) => hosts[id] !== undefined)
  // Disabled only WHILE nothing can be chosen (review F5): a host that connects later must be pickable.
  const anyConnected = listed.some((id) => runtime[id]?.status === 'connected')
  const state = view === null ? 'none' : view.kind === 'rows' ? (view.rows.length === 0 ? 'empty' : 'rows') : view.kind

  return (
    <div className="mt-3 text-xs">
      <p className="text-text-secondary">{t('settings.profile.wizard.sot.what')}</p>

      <label className="mt-2 flex flex-wrap items-center gap-2 text-text-primary">
        {t('settings.profile.wizard.sot.host')}
        <select
          data-testid="profile-wizard-host"
          value={hostId ?? ''}
          disabled={disabled || !anyConnected}
          onChange={(e) => onHost(e.target.value)}
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-60 hover:border-text-muted focus:border-border-active focus:outline-none"
        >
          {hostId === null && <option value="">{t(anyConnected ? 'settings.profile.wizard.sot.host_choose' : 'settings.profile.wizard.sot.host_placeholder')}</option>}
          {listed.map((id) => {
            const connected = runtime[id]?.status === 'connected'
            const name = hostLabel(id, hostLookOf(id, hosts))
            return (
              <option key={id} value={id} disabled={!connected} data-testid={`profile-wizard-host-option-${id}`}>
                {connected ? name : t('settings.profile.wizard.sot.host_offline', { name })}
              </option>
            )
          })}
        </select>
      </label>
      {!anyConnected && (
        <p data-testid="profile-wizard-host-none" className={NOTICE}>{t('settings.profile.wizard.sot.host_none')}</p>
      )}

      {hostId !== null && (
        <div data-testid="profile-wizard-profiles" data-state={state} role="radiogroup" aria-label={t('settings.profile.wizard.sot.profiles')} className="mt-3">
          <div className="flex items-center justify-between gap-2">
            <p className="text-text-secondary">{t('settings.profile.wizard.sot.profiles')}</p>
            {view?.kind === 'rows' && (
              <button type="button" data-testid="profile-wizard-profiles-refresh" disabled={disabled} onClick={reload} className={BTN}>
                <ArrowsClockwise size={14} />
                {t('settings.profile.sot.refresh')}
              </button>
            )}
          </div>
          {view?.kind === 'loading' && <p data-testid="profile-wizard-profiles-loading" className="text-text-muted">{t('settings.profile.wizard.sot.loading')}</p>}
          {view?.kind === 'error' && (
            <div className="flex flex-wrap items-center gap-2">
              <p data-testid="profile-wizard-profiles-error" data-reason={view.reason} className="text-red-500">{t(requestKey(view.reason))}</p>
              <button type="button" data-testid="profile-wizard-profiles-retry" onClick={reload} className={BTN}>
                <ArrowsClockwise size={14} />
                {t('settings.profile.wizard.run.retry')}
              </button>
            </div>
          )}
          {state === 'empty' && <p data-testid="profile-wizard-profiles-empty" className="text-text-muted">{t('settings.profile.wizard.sot.empty')}</p>}
          {view?.kind === 'rows' &&
            view.rows.map((row) => {
              const blocked = del.blocked(row)
              return (
                <div key={row.id} data-testid={`profile-wizard-profile-row-${row.id}`}>
                  <div className="flex items-center justify-between gap-2">
                    <label className={CHOICE}>
                      <input
                        type="radio"
                        name="profile-wizard-profile"
                        data-testid={`profile-wizard-profile-${row.id}`}
                        className={RADIO}
                        disabled={disabled}
                        checked={choice?.kind === 'existing' && choice.id === row.id}
                        onChange={() => onChoice({ kind: 'existing', id: row.id })}
                      />
                      {/* A profile's name is its owner's text: shown as it is. */}
                      <span>{row.name}</span>
                      <span className="text-text-muted">{t('settings.profile.wizard.sot.devices', { count: row.attachments.length })}</span>
                      {row.id === maybeId && <span data-testid={`profile-wizard-profile-maybe-${row.id}`} className={BADGE}>{t('settings.profile.wizard.sot.maybe_yours')}</span>}
                    </label>
                    {blocked === null && (
                      <button type="button" data-testid={`profile-wizard-profile-delete-${row.id}`} disabled={disabled} onClick={() => del.ask(row)} className={BTN}>
                        {t('common.delete')}
                      </button>
                    )}
                  </div>
                  {blocked !== null && (
                    <p data-testid={`profile-wizard-profile-delete-blocked-${row.id}`} className="ml-5 text-text-muted">
                      {t(`settings.profile.sot.delete_blocked_${blocked}`)}
                    </p>
                  )}
                  {del.refused?.id === row.id && (
                    <p data-testid={`profile-wizard-profile-attached-${row.id}`} className="ml-5 text-status-warning">
                      {del.refused.attachments.length > 0
                        ? t('settings.profile.sot.attached', { names: deviceNames(del.refused.attachments) })
                        : t('settings.profile.sot.attached_none')}
                    </p>
                  )}
                </div>
              )
            })}
          {/* A new one can be made whatever the list says — also when it could not be read. */}
          <label className={CHOICE}>
            <input
              type="radio"
              name="profile-wizard-profile"
              data-testid="profile-wizard-profile-new"
              className={RADIO}
              disabled={disabled}
              checked={choice?.kind === 'new'}
              onChange={() => onChoice({ kind: 'new' })}
            />
            <span>{t('settings.profile.wizard.sot.new')}</span>
          </label>
          {choice?.kind === 'new' && (
            <div className="ml-5 flex flex-col gap-1">
              <input
                type="text"
                data-testid="profile-wizard-new-name"
                aria-label={t('settings.profile.wizard.sot.new_name')}
                spellCheck={false}
                disabled={disabled}
                value={newName}
                onChange={(e) => onNewName(e.target.value)}
                className={INPUT}
              />
              <span className="text-text-muted">{t('settings.profile.wizard.sot.new_hint')}</span>
            </div>
          )}
          {del.status !== null && (
            <p data-testid="profile-wizard-delete-status" role="alert" className="mt-1 text-red-500">{del.status}</p>
          )}
          {createError !== null && (
            <p data-testid="profile-wizard-create-error" data-outcome={createError.outcome} data-reason={createError.request} role="alert" className="mt-1 text-red-500">
              {createError.outcome === 'unknown' || createError.outcome === 'same-name'
                ? t(`settings.profile.wizard.sot.create_${createError.outcome.replace('-', '_')}`)
                : `${t('settings.profile.wizard.sot.create_failed')} ${t(requestKey(createError.request))}${createError.outcome === 'not-created' ? ` ${t('settings.profile.wizard.sot.create_not_created')}` : ''}`}
            </p>
          )}
        </div>
      )}
    </div>
  )
}

// === Step 3 ===

/** One sentence for the three states that heal by themselves; the other two never get this far (the wizard refuses to start). */
const WORLD_KEY: Record<UnsettledReason, string> = {
  'epoch-mismatch': 'settings.profile.wizard.local.catching_up',
  'world-mismatch': 'settings.profile.wizard.local.catching_up',
  'behind-fence': 'settings.profile.wizard.local.catching_up',
  'junk-epoch': 'settings.profile.wizard.refused.junk_epoch',
  'no-parked-master': 'settings.profile.wizard.refused.no_parked_master',
}

/** `demotedAlso`: the names the run would number the demoted master past for the current draft (wizard-run's `promote`). */
export function LocalStep({ localId, onLocal, worldReason, demotedAlso }: { localId: string; onLocal: (id: string) => void; worldReason: UnsettledReason | null; demotedAlso: readonly string[] }) {
  const t = useI18nStore((s) => s.t)
  const slaves = useLocalProfilesStore((s) => s.slaves)
  const slaveOrder = useLocalProfilesStore((s) => s.slaveOrder)
  const master = useLocalProfilesStore((s) => s.master)
  const activeProfileId = useLocalProfilesStore((s) => s.activeProfileId)
  const order = slaveOrder.filter((id) => slaves[id])
  const onScreenId = slaves[activeProfileId] ? activeProfileId : MASTER_PROFILE_ID
  const masterName = master.name ?? t('nav.home')

  const row = (id: string, name: string) => (
    <label key={id} className={CHOICE}>
      <input type="radio" name="profile-wizard-local" data-testid={`profile-wizard-local-${id}`} className={RADIO} checked={localId === id} onChange={() => onLocal(id)} />
      <span>{name}</span>
      {id === MASTER_PROFILE_ID && <span className={BADGE}>{t('profile.master')}</span>}
      {onScreenId === id && <span data-testid={`profile-wizard-local-on-screen-${id}`} className={BADGE}>{t('settings.profile.local.on_screen')}</span>}
    </label>
  )

  return (
    <div className="mt-3 text-xs">
      <p className="text-text-secondary">{t('settings.profile.wizard.local.what')}</p>
      <div role="radiogroup" aria-label={t('settings.profile.wizard.step.local')} className="mt-2">
        {row(MASTER_PROFILE_ID, masterName)}
        {order.map((id) => row(id, slaves[id].name))}
      </div>
      <p data-testid="profile-wizard-local-consequence" className="mt-2 text-text-secondary">
        {localId === MASTER_PROFILE_ID || !slaves[localId]
          ? t('settings.profile.wizard.local.keep')
          : master.name !== null
            ? t('settings.profile.wizard.local.move', { name: slaves[localId].name, master: master.name })
            : // unnamed: the run names it after this device, at run time — said as "right now", not promised (#1450)
              t('settings.profile.wizard.local.move_unnamed', { name: slaves[localId].name, master: masterName, demoted: offeredProfileName(demotedAlso) })}
      </p>
      {worldReason !== null && (
        <p data-testid="profile-wizard-local-world" data-reason={worldReason} role="status" className={NOTICE}>{t(WORLD_KEY[worldReason])}</p>
      )}
    </div>
  )
}

// === Step 4 ===

interface DirectionStepProps {
  profileName: string
  /** Why pull is not offered; null = it is. */
  pullUnavailable: 'new' | 'empty' | null
  direction: SyncDirection | null
  onDirection: (direction: SyncDirection) => void
  localId: string
  saveFirst: boolean
  onSaveFirst: (save: boolean) => void
  saveName: string
  onSaveName: (name: string) => void
  saveNameOk: boolean
  /** Why a pull cannot be made through this host (host-sync-identity spec §8, D3); null = it can. Said with pull chosen. */
  pullRefused: PullPremiseReason | null
}

export function DirectionStep({ profileName, pullUnavailable, direction, onDirection, localId, saveFirst, onSaveFirst, saveName, onSaveName, saveNameOk, pullRefused }: DirectionStepProps) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  // Read at render: the step is re-rendered by every choice made on it, and the run reads the world again itself.
  const counts = countWorld(worldToBeMaster(localId))
  // Hosts whose daemon is not the one they are recorded as: not in the way of starting, but the sync will pause
  // on them (start.ts, `host-identity-mismatch`) — said here, before it does.
  const mismatched = hostOrder.filter((id) => hosts[id] !== undefined && selectDaemonIdMismatch({ hosts, runtime }, id) !== undefined)
  const hostName = (id: string): string => hostLabel(id, hostLookOf(id, hosts))

  return (
    <div className="mt-3 text-xs">
      <p className="text-text-secondary">{t('settings.profile.wizard.direction.what')}</p>
      <div role="radiogroup" aria-label={t('settings.profile.wizard.step.direction')} className="mt-2">
        <label className={CHOICE}>
          <input type="radio" name="profile-wizard-direction" data-testid="profile-wizard-direction-push" className={RADIO} checked={direction === 'push'} onChange={() => onDirection('push')} />
          <span>{t('settings.profile.wizard.direction.push')}</span>
        </label>
        <label className={`${CHOICE} ${pullUnavailable !== null ? 'opacity-50 cursor-not-allowed' : ''}`}>
          <input
            type="radio"
            name="profile-wizard-direction"
            data-testid="profile-wizard-direction-pull"
            className={RADIO}
            disabled={pullUnavailable !== null}
            checked={direction === 'pull'}
            onChange={() => {
              if (pullUnavailable === null) onDirection('pull')
            }}
          />
          <span>{t('settings.profile.wizard.direction.pull')}</span>
        </label>
      </div>
      {pullUnavailable !== null && (
        <p data-testid="profile-wizard-pull-unavailable" className="mt-1 text-text-muted">{t(`settings.profile.wizard.direction.pull_${pullUnavailable}`)}</p>
      )}

      {direction === 'push' && pullUnavailable === null && (
        <p data-testid="profile-wizard-push-warning" className={NOTICE}>{t('settings.profile.wizard.direction.push_replaces', { profile: profileName })}</p>
      )}

      {direction === 'pull' && (
        <div className="mt-2 flex flex-col gap-1">
          <p data-testid="profile-wizard-pull-replaces" className="text-yellow-500">
            {counts === null ? t('settings.profile.wizard.direction.pull_replaces_unknown') : t('settings.profile.wizard.direction.pull_replaces', { workspaces: counts.workspaces, tabs: counts.tabs })}
          </p>
          <p className="text-text-secondary">{t('settings.profile.wizard.direction.pull_also')}</p>
          {pullRefused !== null && (
            <p data-testid="profile-wizard-pull-refused" data-reason={pullRefused} role="alert" className="text-red-500">
              {t(`settings.profile.wizard.pull.${pullRefused.replace(/-/g, '_')}`)}
            </p>
          )}
          <label className={CHOICE}>
            <input type="checkbox" data-testid="profile-wizard-save-first" className={RADIO} checked={saveFirst} onChange={(e) => onSaveFirst(e.target.checked)} />
            <span>{t('settings.profile.wizard.direction.save_first')}</span>
          </label>
          {saveFirst ? (
            <div className="ml-5 flex flex-col gap-1">
              <input
                type="text"
                data-testid="profile-wizard-save-name"
                aria-label={t('settings.profile.wizard.direction.save_name')}
                spellCheck={false}
                value={saveName}
                onChange={(e) => onSaveName(e.target.value)}
                className={INPUT}
              />
              {!saveNameOk && <span data-testid="profile-wizard-save-name-error" className="text-red-500">{t('settings.profile.wizard.direction.save_name_needed')}</span>}
            </div>
          ) : (
            <p data-testid="profile-wizard-no-copy-warning" className="text-yellow-500">{t('settings.profile.wizard.direction.no_copy')}</p>
          )}
        </div>
      )}

      {mismatched.map((id) => (
        <p key={id} data-testid={`profile-wizard-host-mismatch-${id}`} className={NOTICE}>{t('settings.profile.wizard.direction.host_mismatch', { name: hostName(id) })}</p>
      ))}
    </div>
  )
}

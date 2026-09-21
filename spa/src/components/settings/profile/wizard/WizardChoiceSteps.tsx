// spa/src/components/settings/profile/wizard/WizardChoiceSteps.tsx — the wizard's three steps that only CHOOSE
// (the host and its profile · the local profile · the direction). Nothing in this file does anything: every
// choice is handed up to ProfileWizard.tsx, which owns the order, the premises and the run.
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../../stores/useI18nStore'
import { useHostStore } from '../../../../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../../stores/useLocalProfilesStore'
import type { SyncDirection } from '../../../../stores/useProfileStore'
import type { UnsettledReason } from '../../../../lib/profile/master-world'
import type { SotProfilesView } from '../useSotProfiles'
import { countWorld, worldToBeMaster } from './wizard-run'
import { BTN, INPUT, NOTICE, requestKey } from './wizard-shared'

const BADGE = 'rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary'
const CHOICE = 'flex items-center gap-2 py-1 text-xs text-text-primary cursor-pointer'
const RADIO = 'accent-border-active'

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
  /** A failed create's class (`requestKey`). */
  createError: string | null
  disabled: boolean
}

export function SotStep({ hostId, onHost, view, reload, choice, onChoice, newName, onNewName, createError, disabled }: SotStepProps) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const listed = hostOrder.filter((id) => hosts[id] !== undefined)
  const state = view === null ? 'none' : view.kind === 'rows' ? (view.rows.length === 0 ? 'empty' : 'rows') : view.kind

  return (
    <div className="mt-3 text-xs">
      <p className="text-text-secondary">{t('settings.profile.wizard.sot.what')}</p>

      <label className="mt-2 flex flex-wrap items-center gap-2 text-text-primary">
        {t('settings.profile.wizard.sot.host')}
        <select
          data-testid="profile-wizard-host"
          value={hostId ?? ''}
          disabled={disabled || hostId === null}
          onChange={(e) => onHost(e.target.value)}
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-60 hover:border-text-muted focus:border-border-active focus:outline-none"
        >
          {hostId === null && <option value="">{t('settings.profile.wizard.sot.host_placeholder')}</option>}
          {listed.map((id) => {
            const connected = runtime[id]?.status === 'connected'
            return (
              <option key={id} value={id} disabled={!connected} data-testid={`profile-wizard-host-option-${id}`}>
                {connected ? hosts[id].name : t('settings.profile.wizard.sot.host_offline', { name: hosts[id].name })}
              </option>
            )
          })}
        </select>
      </label>
      {hostId === null && (
        <p data-testid="profile-wizard-host-none" className={NOTICE}>{t('settings.profile.wizard.sot.host_none')}</p>
      )}

      {hostId !== null && (
        <div data-testid="profile-wizard-profiles" data-state={state} role="radiogroup" aria-label={t('settings.profile.wizard.sot.profiles')} className="mt-3">
          <p className="text-text-secondary">{t('settings.profile.wizard.sot.profiles')}</p>
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
            view.rows.map((row) => (
              <label key={row.id} className={CHOICE}>
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
              </label>
            ))}
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
          {createError !== null && (
            <p data-testid="profile-wizard-create-error" data-reason={createError} role="alert" className="mt-1 text-red-500">
              {t('settings.profile.wizard.sot.create_failed')} {t(requestKey(createError))}
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

export function LocalStep({ localId, onLocal, worldReason }: { localId: string; onLocal: (id: string) => void; worldReason: UnsettledReason | null }) {
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
          : t('settings.profile.wizard.local.move', { name: slaves[localId].name, master: masterName })}
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
}

export function DirectionStep({ profileName, pullUnavailable, direction, onDirection, localId, saveFirst, onSaveFirst, saveName, onSaveName, saveNameOk }: DirectionStepProps) {
  const t = useI18nStore((s) => s.t)
  // Read at render: the step is re-rendered by every choice made on it, and the run reads the world again itself.
  const counts = countWorld(worldToBeMaster(localId))

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
    </div>
  )
}

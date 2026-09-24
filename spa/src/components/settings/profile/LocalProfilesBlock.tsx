// spa/src/components/settings/profile/LocalProfilesBlock.tsx — Settings › Profile › Profiles: every profile of
// THIS device. The master first (it is the one that can sync), then the slaves in `slaveOrder`; per row the look,
// a switch, and for a slave its place in the order and Delete; below, the three ways to a new one — three separate
// buttons (per-workbench shown hosts plan §0.7): "Duplicate all" = what is on screen with its shown-hosts list
// (`saveScreenAsSlave`), "Duplicate settings only" = the shown-hosts list and one empty workspace
// (`createSettingsCopySlave`), "New blank workbench" = one empty workspace, every host hidden (`createBlankSlave`).
// None copies a name, icon or colour. Copying the master has no button: that is the wizard's.
//
// The stores are read; the worlds are moved by lib/profile/switch-active.ts and nothing else (P3 plan, P3b "As
// built"). A SWITCH goes through `useProfileSwitcherStore.chooseProfile` — the Home menu's own path, with its
// silent `busy` retries and its one toast — so there is one switch under way per window, whoever asked for it.
// `promoteToMaster` is not here: it is the wizard's third step (P3d-3), and refused while a master is attached.
import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { ensureDefaultDeviceName, useDeviceNameStore } from '../../../stores/useDeviceNameStore'
import { effectiveDeviceName } from '../../../lib/device-name'
import { createBlankSlave, createSettingsCopySlave, deleteSlave, reorderSlaves, saveScreenAsSlave, type CopyResult } from '../../../lib/profile/switch-active'
import { LocalProfileRow } from './LocalProfileRow'
import { defaultSlaveName } from './profile-rules'

type Tone = 'info' | 'error'
interface Note { tone: Tone; message: string }

const TONE_COLOR: Record<Tone, string> = { info: 'text-text-secondary', error: 'text-red-500' }

const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'

type NewKind = 'duplicate' | 'settings' | 'blank'

/** Per kind: its create, and its button's label key. */
const NEW_KINDS: readonly { kind: NewKind; create: (name: string) => CopyResult; label: string }[] = [
  { kind: 'duplicate', create: (name) => saveScreenAsSlave(name), label: 'settings.profile.local.duplicate' },
  { kind: 'settings', create: (name) => createSettingsCopySlave(name), label: 'settings.profile.local.duplicate_settings' },
  { kind: 'blank', create: (name) => createBlankSlave(name), label: 'settings.profile.local.new_blank' },
]

export function LocalProfilesBlock() {
  const t = useI18nStore((s) => s.t)
  const slaves = useLocalProfilesStore((s) => s.slaves)
  const slaveOrder = useLocalProfilesStore((s) => s.slaveOrder)
  const master = useLocalProfilesStore((s) => s.master)
  const activeProfileId = useLocalProfilesStore((s) => s.activeProfileId)
  const pendingId = useProfileSwitcherStore((s) => s.pending?.targetId ?? null)
  const chooseProfile = useProfileSwitcherStore((s) => s.chooseProfile)
  const [note, setNote] = useState<Note | null>(null)
  const [creating, setCreating] = useState<{ kind: NewKind; name: string; touched: boolean } | null>(null)
  const [createNote, setCreateNote] = useState<Note | null>(null)

  const order = slaveOrder.filter((id) => slaves[id])
  // A pointer at a slave that is not there is the store's `merge` to heal; until then the master is the honest answer.
  const onScreenId = slaves[activeProfileId] ? activeProfileId : MASTER_PROFILE_ID

  /** `unsettled` is a state, not a fault: the refusal has already asked the stores to catch up. */
  const sentence = (reason: string, detail?: string): Note => {
    switch (reason) {
      case 'unsettled': return { tone: 'info', message: t('settings.profile.local.error.unsettled') }
      case 'not-found': return { tone: 'error', message: t('settings.profile.local.error.not_found') }
      case 'on-screen': return { tone: 'error', message: t('settings.profile.local.error.on_screen') }
      case 'bad-order': return { tone: 'error', message: t('settings.profile.local.error.bad_order') }
      case 'bad-name': return { tone: 'error', message: t('settings.profile.local.error.bad_name') }
      case 'bad-world': return { tone: 'error', message: t('settings.profile.local.error.bad_world') }
      case 'write-failed': return { tone: 'error', message: t('settings.profile.local.error.write_failed', { detail: detail ?? '' }) }
      default: return { tone: 'error', message: t('settings.profile.local.error.unknown', { reason }) }
    }
  }

  const move = (id: string, by: -1 | 1) => {
    const from = order.indexOf(id)
    const to = from + by
    if (from < 0 || to < 0 || to >= order.length) return
    const next = [...order]
    ;[next[from], next[to]] = [next[to], next[from]]
    const r = reorderSlaves(next)
    setNote(r.ok ? null : sentence(r.reason))
  }

  const remove = (id: string) => {
    const r = deleteSlave(id)
    setNote(r.ok ? null : sentence(r.reason))
  }

  /** The device name, with a number when a profile is already called that. */
  const offeredName = (): string => {
    const local = useLocalProfilesStore.getState()
    const taken = [...Object.values(local.slaves).map((s) => s.name), ...(local.master.name === null ? [] : [local.master.name])]
    return defaultSlaveName(effectiveDeviceName(useDeviceNameStore.getState()), taken)
  }

  const openCreate = (kind: NewKind) => {
    setCreateNote(null)
    setCreating({ kind, name: offeredName(), touched: false })
    // Electron's hostname is resolved on demand (nothing does it for a user without a master once device-state
    // is gone): asked for HERE, by a click — opening the page asks for nothing — and taken only if the user has
    // not typed meanwhile.
    void ensureDefaultDeviceName().then(() => setCreating((c) => (c !== null && !c.touched ? { ...c, name: offeredName() } : c)))
  }

  const create = () => {
    if (creating === null) return
    const kind = creating.kind
    const r: CopyResult = (NEW_KINDS.find((k) => k.kind === kind) ?? NEW_KINDS[0]).create(creating.name)
    if (r.ok) {
      setCreating(null)
      setCreateNote(null)
      return
    }
    setCreateNote(sentence(r.reason, r.reason === 'write-failed' ? r.detail : undefined))
  }

  return (
    <section data-testid="profile-local-block" className="mt-8">
      <h3 className="text-sm text-text-primary">{t('settings.profile.local.title')}</h3>
      <p className="mb-2 text-xs text-text-secondary">{t('settings.profile.local.desc')}</p>

      <ul className="flex flex-col">
        <LocalProfileRow
          id={MASTER_PROFILE_ID}
          name={master.name ?? t('nav.home')}
          look={master}
          onScreen={onScreenId === MASTER_PROFILE_ID}
          switchPending={pendingId !== null}
          switchingHere={pendingId === MASTER_PROFILE_ID}
          canMoveUp={false}
          canMoveDown={false}
          onSwitch={chooseProfile}
          onMove={move}
          onDelete={remove}
        />
        {order.map((id, index) => (
          <LocalProfileRow
            key={id}
            id={id}
            name={slaves[id].name}
            look={slaves[id]}
            onScreen={onScreenId === id}
            switchPending={pendingId !== null}
            switchingHere={pendingId === id}
            canMoveUp={index > 0}
            canMoveDown={index < order.length - 1}
            onSwitch={chooseProfile}
            onMove={move}
            onDelete={remove}
          />
        ))}
      </ul>

      {note && (
        <p data-testid="profile-local-status" data-tone={note.tone} className={`mt-2 text-xs ${TONE_COLOR[note.tone]}`}>
          {note.message}
        </p>
      )}

      <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-border-default pt-3">
        {NEW_KINDS.map(({ kind, label }) => (
          <button key={kind} type="button" data-testid={`profile-new-${kind}`} aria-pressed={creating?.kind === kind} onClick={() => openCreate(kind)} className={BTN}>
            {t(label)}
          </button>
        ))}
      </div>

      {creating && (
        <div data-testid="profile-new-form" data-kind={creating.kind} role="group" className="mt-2 flex flex-wrap items-center gap-2 text-xs">
          <span className="text-text-secondary">{t(`settings.profile.local.new_${creating.kind}_hint`)}</span>
          <input
            type="text"
            autoFocus
            aria-label={t('settings.profile.local.name')}
            data-testid="profile-new-name"
            spellCheck={false}
            value={creating.name}
            onChange={(e) => setCreating({ ...creating, name: e.target.value, touched: true })}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing) return
              if (e.key === 'Enter') create()
              else if (e.key === 'Escape') setCreating(null)
            }}
            className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-xs text-text-primary w-48"
          />
          <button type="button" data-testid="profile-new-create" onClick={create} className={BTN}>
            {t('settings.profile.local.create')}
          </button>
          <button type="button" data-testid="profile-new-cancel" onClick={() => setCreating(null)} className={BTN}>
            {t('common.cancel')}
          </button>
        </div>
      )}
      {creating && createNote && (
        <p data-testid="profile-new-error" data-tone={createNote.tone} className={`mt-1 text-xs ${TONE_COLOR[createNote.tone]}`}>
          {createNote.message}
        </p>
      )}
    </section>
  )
}

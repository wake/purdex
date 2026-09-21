import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react'
import { Menu, type MenuEntry, type MenuPlacement } from '../../../components/Menu'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useLocalProfilesStore, MASTER_PROFILE_ID } from '../../../stores/useLocalProfilesStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { switchActiveProfile, type SwitchResult } from '../../../lib/profile/switch-active'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'

/** `busy` is the 3 s ownership gate or another window holding the world lock (P3 plan, "What the UI must say"):
 *  it ends by itself, so it is retried in silence — this often, for this long IN TOTAL (a refusal can itself
 *  have taken 3 s to arrive), and only then said. */
export const BUSY_RETRY_MS = 250
export const BUSY_RETRY_TOTAL_MS = 4_000

type SyncDot = 'synced' | 'syncing' | 'locked' | 'problem' | 'unknown'

// Tailwind classes the app already uses for status dots; no new colour.
const DOT_CLASS: Record<SyncDot, string> = {
  synced: 'bg-green-500',
  syncing: 'bg-yellow-500',
  locked: 'bg-amber-500',
  problem: 'bg-red-500',
  unknown: 'bg-gray-500',
}

/**
 * One dot for the whole master; null = no master attached, no dot. In a follower window the figures are the
 * leader's (`remote`) and are shown all the same; once `stale` nobody is there to correct them, so the dot stops
 * vouching for them. `problems` is not read: it is a log of the last 50, with nothing saying one is over — a dot
 * driven by it would stay red for ever. What blocks the sync NOW is `blocked`; the log is Settings › Profile's.
 */
function syncDotOf(sync: ProfileSyncSnapshot): SyncDot | null {
  if (sync.master === null) return null
  if (sync.blocked === 'suspended') return 'syncing' // an attach is under way somewhere: transient
  if (sync.blocked !== null) return 'problem'
  if (sync.status === null || (sync.remote && sync.stale)) return 'unknown'
  const { profile } = sync.status
  if (profile.startsWith('locked:')) return 'locked'
  if (profile === 'synced') return 'synced'
  if (profile === 'pending') return 'syncing'
  return 'unknown'
}

/** One chosen switch, across its retries. */
interface Attempt {
  cancelled: boolean
  timer: ReturnType<typeof setTimeout> | null
}

interface Props {
  /** The Home button. */
  trigger: RefObject<HTMLElement | null>
  placement: MenuPlacement
}

/**
 * The Home button's menu: the master, then the slaves in `slaveOrder`; the one on screen is checked. Choosing one
 * calls `switchActiveProfile` and nothing else — switching never starts or stops syncing (spec §4.1). Rendered by
 * a Home button only while there is a slave (`useProfileSwitcherTrigger`).
 */
export function ProfileSwitcher({ trigger, placement }: Props) {
  const t = useI18nStore((s) => s.t)
  const slaves = useLocalProfilesStore((s) => s.slaves)
  const slaveOrder = useLocalProfilesStore((s) => s.slaveOrder)
  const activeProfileId = useLocalProfilesStore((s) => s.activeProfileId)
  const open = useProfileSwitcherStore((s) => s.open)
  const setOpen = useProfileSwitcherStore((s) => s.setOpen)
  const sync = useProfileSync()

  /** The switch under way: no second one starts meanwhile; `retrying` once it has answered `busy`. */
  const [pending, setPending] = useState<{ id: string; retrying: boolean } | null>(null)
  /** The current attempt. Closing the menu (or unmounting) cancels it: its timer is cleared and a late answer is dropped. */
  const attempt = useRef<Attempt | null>(null)

  const close = useCallback(() => setOpen(false), [setOpen])

  useEffect(() => {
    if (!open) return
    return () => {
      const a = attempt.current
      if (a) {
        a.cancelled = true
        if (a.timer !== null) clearTimeout(a.timer)
        attempt.current = null
      }
      setPending(null)
    }
  }, [open])

  const choose = (id: string) => {
    if (attempt.current) return
    const mine: Attempt = { cancelled: false, timer: null }
    attempt.current = mine
    const startedAt = Date.now()
    setPending({ id, retrying: false })

    const say = (message: string) => useUndoToast.getState().show(message)
    /** The attempt is over; the menu stays unless told otherwise — nothing was written, the user is where they were. */
    const end = (closeMenu: boolean) => {
      attempt.current = null
      setPending(null)
      if (closeMenu) close()
    }

    const settle = (r: SwitchResult) => {
      if (r.ok || r.reason === 'already-on-screen') return end(true)
      switch (r.reason) {
        case 'busy':
          if (Date.now() - startedAt < BUSY_RETRY_TOTAL_MS) {
            setPending({ id, retrying: true })
            mine.timer = setTimeout(run, BUSY_RETRY_MS)
            return
          }
          say(t('profile.switch.busy'))
          return end(false)
        case 'unsettled':
          // Not an error: the refusal has already asked the stores to catch up, the same click works a moment later.
          say(t('profile.switch.try_again'))
          return end(false)
        case 'superseded':
          // Another window's switch won and this window is about to show ITS world: not retried, and the menu goes.
          say(t('profile.switch.try_again'))
          return end(true)
        case 'write-failed':
          say(t('profile.switch.write_failed', { detail: r.detail }))
          return end(false)
        case 'not-found':
          say(t('profile.switch.not_found'))
          return end(false)
        default:
          say(t('profile.switch.failed', { reason: r.reason }))
          return end(false)
      }
    }

    function run() {
      mine.timer = null
      switchActiveProfile(id).then(
        (r) => { if (!mine.cancelled) settle(r) },
        (e: unknown) => {
          if (mine.cancelled) return
          say(t('profile.switch.failed', { reason: e instanceof Error ? e.message : String(e) }))
          end(false)
        },
      )
    }
    run()
  }

  const dot = syncDotOf(sync)
  const dotLabel = dot === null ? '' : t(`profile.sync.${dot}`)

  const items = useMemo<MenuEntry[]>(() => {
    const entry = (id: string, label: string, extra: Partial<Extract<MenuEntry, { id: string }>> = {}): MenuEntry => ({
      id,
      label,
      checked: id === activeProfileId,
      busy: pending?.id === id && pending.retrying,
      disabled: pending !== null && pending.id !== id,
      keepOpen: true, // `choose` closes the menu when the switch has ended
      onSelect: () => choose(id),
      testId: `profile-item-${id}`,
      ...extra,
    })
    return [
      entry(MASTER_PROFILE_ID, t('profile.master'), {
        trailing: dot !== null && (
          <span
            role="img"
            aria-label={dotLabel}
            title={dotLabel}
            data-testid="profile-sync-dot"
            data-state={dot}
            className={`w-1.5 h-1.5 rounded-full ${DOT_CLASS[dot]}`}
          />
        ),
      }),
      // A slave's name is the user's own text: shown as is, never through t(); the title has it in full.
      ...slaveOrder.filter((id) => slaves[id]).map((id) => entry(id, slaves[id].name, { title: slaves[id].name })),
      // TODO(P3d-2): once Settings › Profile exists, add `{ divider: true }` and a plain item here that opens it
      // (`profile.switcher.settings`, testId `profile-item-settings`; spec §4.9 "then Settings › Profile"). With
      // no master and no slaves that one item — labelled `Set up sync…` — is the whole menu, and
      // `useProfileSwitcherTrigger` then enables the trigger for everyone. Not before: the page does not exist yet.
    ]
    // `choose` closes over refs and setters only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slaves, slaveOrder, activeProfileId, pending, dot, dotLabel, t])

  return <Menu trigger={trigger} open={open} onClose={close} items={items} label={t('profile.switcher.label')} placement={placement} testId="profile-switcher-menu" />
}

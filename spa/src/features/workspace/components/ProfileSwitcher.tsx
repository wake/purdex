import { useCallback, useMemo, type RefObject } from 'react'
import { Menu, type MenuEntry, type MenuPlacement } from '../../../components/Menu'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useLocalProfilesStore, MASTER_PROFILE_ID } from '../../../stores/useLocalProfilesStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useProfileSync } from '../../../hooks/useProfileSync'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'

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

interface Props {
  /** The Home button. */
  trigger: RefObject<HTMLElement | null>
  placement: MenuPlacement
}

/**
 * The Home button's menu: the master, then the slaves in `slaveOrder`; the one on screen is checked. Choosing one
 * calls `switchActiveProfile` and nothing else — switching never starts or stops syncing (spec §4.1). Rendered by
 * a Home button only while there is a slave (`useProfileSwitcherTrigger`).
 *
 * It only READS the switch under way: `useProfileSwitcherStore` owns it (the attempt, its retries, every answer),
 * because this component is one of two — the other bar's copy replaces it when the bar's width changes, and must
 * come up just as busy.
 */
export function ProfileSwitcher({ trigger, placement }: Props) {
  const t = useI18nStore((s) => s.t)
  const slaves = useLocalProfilesStore((s) => s.slaves)
  const slaveOrder = useLocalProfilesStore((s) => s.slaveOrder)
  const activeProfileId = useLocalProfilesStore((s) => s.activeProfileId)
  const open = useProfileSwitcherStore((s) => s.open)
  const setOpen = useProfileSwitcherStore((s) => s.setOpen)
  const pendingId = useProfileSwitcherStore((s) => s.pending?.targetId ?? null)
  const chooseProfile = useProfileSwitcherStore((s) => s.chooseProfile)
  const sync = useProfileSync()

  const close = useCallback(() => setOpen(false), [setOpen])

  const dot = syncDotOf(sync)
  const dotLabel = dot === null ? '' : t(`profile.sync.${dot}`)

  const items = useMemo<MenuEntry[]>(() => {
    const entry = (id: string, label: string, extra: Partial<Extract<MenuEntry, { id: string }>> = {}): MenuEntry => ({
      id,
      label,
      checked: id === activeProfileId,
      // One switch at a time: the chosen item is busy from the click to the last answer, the others wait.
      busy: pendingId === id,
      disabled: pendingId !== null && pendingId !== id,
      keepOpen: true, // the store closes the menu when the switch has ended
      onSelect: () => { chooseProfile(id) },
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
  }, [slaves, slaveOrder, activeProfileId, pendingId, chooseProfile, dot, dotLabel, t])

  return <Menu trigger={trigger} open={open} onClose={close} items={items} label={t('profile.switcher.label')} placement={placement} testId="profile-switcher-menu" />
}

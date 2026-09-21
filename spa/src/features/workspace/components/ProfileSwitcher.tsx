import { useCallback, useMemo, type RefObject } from 'react'
import { Menu, type MenuEntry, type MenuPlacement } from '../../../components/Menu'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useLocalProfilesStore, MASTER_PROFILE_ID, type ProfileAppearance } from '../../../stores/useLocalProfilesStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { SYNC_DOT_CLASS, syncDotOf } from '../../../lib/profile/sync-view'
import { WorkspaceIcon } from './WorkspaceIcon'

/**
 * A profile's icon: its Phosphor icon tinted with its colour (as a workspace's icon, plus the tint a host's
 * colour gives a host icon) — or, with no icon chosen, the Purdex logo, exactly the `<img>` the Home button
 * always had (`logoAlt` is that button's own `alt`). The colour tints an icon only: the logo is a bitmap.
 */
export function ProfileIcon({ appearance, size, logoAlt = '' }: { appearance: ProfileAppearance; size: number; logoAlt?: string }) {
  if (appearance.icon === undefined) {
    return <img src="/icons/logo-transparent.png" alt={logoAlt} width={size} height={size} className="rounded-sm" />
  }
  return (
    <span
      data-testid="profile-icon"
      data-icon={appearance.icon}
      data-weight={appearance.iconWeight}
      className="shrink-0 flex items-center justify-center"
      style={appearance.color !== undefined ? { color: appearance.color } : undefined}
    >
      <WorkspaceIcon icon={appearance.icon} name="" size={size} weight={appearance.iconWeight} />
    </span>
  )
}

interface Props {
  /** The Home button. */
  trigger: RefObject<HTMLElement | null>
  placement: MenuPlacement
}

/**
 * The Home button's menu: the master, then the slaves in `slaveOrder`, each with its icon and name; the one on
 * screen is checked. The master — the only one that ever syncs — is tagged as such, named or not. Choosing one
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
  const master = useLocalProfilesStore((s) => s.master)
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
    const entry = (id: string, label: string, look: ProfileAppearance, extra: Partial<Extract<MenuEntry, { id: string }>> = {}): MenuEntry => ({
      id,
      label,
      title: label, // a name may be truncated
      icon: <ProfileIcon appearance={look} size={14} />,
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
      // Unnamed → `Home`, as on the button. `profile.master` is THE word for it, here and wherever P3d-2 needs one.
      entry(MASTER_PROFILE_ID, master.name ?? t('nav.home'), master, {
        hint: t('profile.master'),
        trailing: dot !== null && (
          <span
            role="img"
            aria-label={dotLabel}
            title={dotLabel}
            data-testid="profile-sync-dot"
            data-state={dot}
            className={`w-1.5 h-1.5 rounded-full ${SYNC_DOT_CLASS[dot]}`}
          />
        ),
      }),
      // A name is the user's own text: shown as is, never through t().
      ...slaveOrder.filter((id) => slaves[id]).map((id) => entry(id, slaves[id].name, slaves[id])),
      // TODO(P3d-2): once Settings › Profile exists, add `{ divider: true }` and a plain item here that opens it
      // (`profile.switcher.settings`, testId `profile-item-settings`; spec §4.9 "then Settings › Profile"). With
      // no master and no slaves that one item — labelled `Set up sync…` — is the whole menu, and
      // `useProfileSwitcherTrigger` then enables the trigger for everyone. Not before: the page does not exist yet.
    ]
  }, [slaves, slaveOrder, master, activeProfileId, pendingId, chooseProfile, dot, dotLabel, t])

  return <Menu trigger={trigger} open={open} onClose={close} items={items} label={t('profile.switcher.label')} placement={placement} testId="profile-switcher-menu" />
}

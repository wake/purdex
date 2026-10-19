import { useRef } from 'react'
import { CaretDown } from '@phosphor-icons/react'
import { useProfileSwitcherTrigger } from '../../../stores/useProfileSwitcherStore'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { DEFAULT_TEAM_ICON } from '../../../stores/useLocalProfilesStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { ProfileIcon, ProfileSwitcher } from './ProfileSwitcher'
import { WorkspaceIcon } from './WorkspaceIcon'
import { ProfileConflictButton } from './ProfileConflict'
import { WatchShieldButton } from './WatchShieldButton'

interface Props {
  isActive: boolean
  onSelectHome: () => void
}

/**
 * The sidebar's view toggle (name pool v2 spec §9.6): a 30 px square button that switches the sidebar between the workspace view and the task-group view. In the workspace view it wears the
 * profile icon (the icon that used to sit in the Home button); in the task-group view the profile's team icon with a pink outline. The flag is the device's (useTeamUiStore).
 */
function ViewToggle({ appearance }: { appearance: ReturnType<typeof useProfileSwitcherTrigger>['current'] }) {
  const t = useI18nStore((s) => s.t)
  const view = useTeamUiStore((s) => s.sidebarView)
  const setView = useTeamUiStore((s) => s.setSidebarView)
  const groups = view === 'groups'
  // A fixed name with aria-pressed (a two-state toggle): the name does not change with the state it already reports.
  const label = t('sidebar.view.toggle')
  return (
    <button
      type="button"
      data-testid="sidebar-view-toggle"
      aria-pressed={groups}
      aria-label={label}
      title={label}
      onClick={() => setView(groups ? 'workspace' : 'groups')}
      className={`w-[30px] h-[30px] shrink-0 flex items-center justify-center rounded-md cursor-pointer focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active ${
        groups ? 'ring-1 ring-pink-400 text-text-primary' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      }`}
    >
      {groups ? (
        <span data-testid="sidebar-view-team-icon" data-icon={appearance.teamIcon ?? DEFAULT_TEAM_ICON} className="shrink-0 flex items-center justify-center">
          <WorkspaceIcon icon={appearance.teamIcon ?? DEFAULT_TEAM_ICON} name="" size={16} />
        </span>
      ) : (
        <ProfileIcon appearance={appearance} size={16} />
      )}
    </button>
  )
}

/**
 * The Home button of the wide bar. It used to head a list of the tabs that were in no workspace; every tab
 * belongs to a workspace now (Profile Sync spec §4.3), so it is a plain button — no list, no drop target.
 * It shows the profile on screen — its icon and name; with neither set that is the Purdex logo and `Home`, the
 * row as it always was. On a device with a local profile (a slave) it is the profile switcher's trigger as
 * well: a chevron, and a click opens the menu (spec §4.9). With the task-group view (name pool v2 §9.6) the profile icon leaves this button for the view toggle before it. While the master has sync locks, an icon beside the button
 * opens what they are (sidebar conflict icons spec §3).
 */
export function HomeRow({ isActive, onSelectHome }: Props) {
  const buttonRef = useRef<HTMLButtonElement>(null)
  const switcher = useProfileSwitcherTrigger(onSelectHome)

  return (
    <div
      data-testid="home-header"
      className={`mx-2 flex items-center gap-1 pl-1.5 rounded-md text-sm transition-colors focus:outline-none focus-visible:outline-none ${
        isActive
          ? 'bg-surface-hover text-text-primary ring-1 ring-purple-400'
          : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      }`}
    >
      <ViewToggle appearance={switcher.current} />
      <button
        ref={buttonRef}
        type="button"
        data-testid="home-button"
        onClick={switcher.onClick}
        {...switcher.triggerProps}
        title={switcher.current.name ?? undefined}
        className="flex-1 min-w-0 flex items-center gap-2 py-1.5 text-left rounded cursor-pointer focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active"
      >
        {/* The profile on screen, by name; one nobody named is `Home` — which, with the logo above, is the row
            as it always was. A slave never syncs: which world this is must be readable without opening the menu. */}
        <span data-testid="home-label" className="flex-1 truncate">{switcher.label}</span>
        {switcher.enabled && <CaretDown size={12} data-testid="home-switcher-chevron" className="mr-2 shrink-0 text-text-muted" />}
      </button>
      {/* Beside the trigger, not in it: a click on the icon opens its popover only (ProfileConflict.tsx). */}
      <ProfileConflictButton />
      <WatchShieldButton />
      {switcher.enabled && <ProfileSwitcher trigger={buttonRef} placement="bottom-start" />}
    </div>
  )
}

import { useRef } from 'react'
import { CaretDown } from '@phosphor-icons/react'
import { useProfileSwitcherTrigger } from '../../../stores/useProfileSwitcherStore'
import { ProfileIcon, ProfileSwitcher } from './ProfileSwitcher'

interface Props {
  isActive: boolean
  onSelectHome: () => void
}

/**
 * The Home button of the wide bar. It used to head a list of the tabs that were in no workspace; every tab
 * belongs to a workspace now (Profile Sync spec §4.3), so it is a plain button — no list, no drop target.
 * It shows the profile on screen — its icon and name; with neither set that is the Purdex logo and `Home`, the
 * row as it always was. On a device with a local profile (a slave) it is the profile switcher's trigger as
 * well: a chevron, and a click opens the menu (spec §4.9).
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
      <button
        ref={buttonRef}
        type="button"
        data-testid="home-button"
        onClick={switcher.onClick}
        {...switcher.triggerProps}
        title={switcher.current.name ?? undefined}
        className="flex-1 min-w-0 flex items-center gap-2 py-1.5 text-left cursor-pointer focus:outline-none"
      >
        <ProfileIcon appearance={switcher.current} size={16} />
        {/* The profile on screen, by name; one nobody named is `Home` — which, with the logo above, is the row
            as it always was. A slave never syncs: which world this is must be readable without opening the menu. */}
        <span data-testid="home-label" className="flex-1 truncate">{switcher.label}</span>
        {switcher.enabled && <CaretDown size={12} data-testid="home-switcher-chevron" className="mr-2 shrink-0 text-text-muted" />}
      </button>
      {switcher.enabled && <ProfileSwitcher trigger={buttonRef} placement="bottom-start" />}
    </div>
  )
}

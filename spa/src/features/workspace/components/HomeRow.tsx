import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  isActive: boolean
  onSelectHome: () => void
}

/**
 * The Home button of the wide bar. It used to head a list of the tabs that were in no workspace; every tab
 * belongs to a workspace now (Profile Sync spec §4.3), so it is a plain button — no list, no chevron, no drop
 * target. `data-testid="home-header"` is what P3d looks for when it turns this into the profile switcher.
 */
export function HomeRow({ isActive, onSelectHome }: Props) {
  const t = useI18nStore((s) => s.t)

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
        type="button"
        onClick={onSelectHome}
        className="flex-1 flex items-center gap-2 py-1.5 text-left cursor-pointer focus:outline-none"
      >
        <img
          src="/icons/logo-transparent.png"
          alt=""
          width={16}
          height={16}
          className="rounded-sm"
        />
        <span className="truncate">{t('nav.home')}</span>
      </button>
    </div>
  )
}

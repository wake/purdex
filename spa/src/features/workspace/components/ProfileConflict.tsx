// spa/src/features/workspace/components/ProfileConflict.tsx — the Home row's sync-conflict icon and its popover
// (sidebar conflict icons spec §3, D1 / D2; the plan's review amendments M2 / M4 / M5).
//
// WHEN: the master has at least one section lock (`locksOf`) — whichever profile is on screen: the locks are the
// master's, so a slave on screen still shows the icon (D2). Nothing is stored; the icon goes with the last lock.
//
// THE POPOVER IS INFORMATION ONLY (D1): what is locked and why, and where it is resolved — never a choice here.
//   settings / workspaces / a kind this build does not know       → "Resolve in Settings" (Settings › Profile)
//   tabs.<ws>, the master on screen, the workspace in the sidebar  → "Go to workspace": that row's own panel
//   tabs.<ws>, a slave on screen                                   → it is in the master: "Switch to Master"
//   tabs.<ws>, the workspace not on this device / world unsettled  → Settings (nobody can point at a row)
// Labels are Settings › Profile's own, with a tabs section named from the MASTER world (`useMasterWorkspaces`),
// never the live store: while a slave is on screen that holds the slave's workspaces. Reasons are the Resolve
// rows' sentences, without "only Keep this device's" — this popover offers no choice to qualify.
//
// The button sits OUTSIDE the switcher trigger (HomeRow.tsx): a click never selects Home nor opens the menu.
import { useRef, useState } from 'react'
import { useLocation } from 'wouter'
import { WarningCircle } from '@phosphor-icons/react'
import { FloatingPanel } from '../../../components/FloatingPanel'
import { useMasterScreen, useMasterWorkspaces } from '../../../hooks/useMasterOnScreen'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { locksOf, type LockView } from '../../../lib/profile/conflict-view'
import type { SectionView } from '../../../lib/profile/sync-view'
import { useConflictPanelStore } from '../../../stores/useConflictPanelStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { MASTER_PROFILE_ID } from '../../../stores/useLocalProfilesStore'
import { useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useWorkspaceStore } from '../store'

/** `profile` is the section's id in `registerSettingsSection` (as ProfileSwitcher.tsx). */
const PROFILE_SETTINGS_PATH = '/settings/profile'
const BTN =
  'shrink-0 rounded-md border border-border-default px-2 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'

type Action = 'settings' | 'show' | 'switch'

export function ProfileConflictButton() {
  const t = useI18nStore((s) => s.t)
  const sync = useProfileSync()
  const masterWorkspaces = useMasterWorkspaces()
  const screen = useMasterScreen()
  const liveWorkspaces = useWorkspaceStore((s) => s.workspaces)
  const switching = useProfileSwitcherStore((s) => s.pending !== null)
  const [, setLocation] = useLocation()
  const buttonRef = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)

  const locks = locksOf(sync, masterWorkspaces)
  // The last lock lifted: the popover closes with it — adjusted during render, so no frame shows it empty, and a
  // lock that comes later does not reopen it by itself.
  if (open && locks.length === 0) setOpen(false)
  if (locks.length === 0) return null

  const label = t('profile.conflict.button', { count: locks.length })

  const sectionLabel = (view: SectionView): string => {
    if (view.kind === 'other') return view.key
    if (view.kind !== 'tabs') return t(`settings.profile.current.label.${view.kind}`)
    if (view.workspace === undefined) return t('settings.profile.current.label.tabs_unknown')
    return view.workspace === null ? t('settings.profile.current.label.tabs_unseen') : t('settings.profile.current.label.tabs', { workspace: view.workspace })
  }

  const why = (lock: LockView): { reason?: string; text: string } => {
    if (lock.status === 'locked:conflict') return { text: t('settings.profile.resolve.why.conflict') }
    if (lock.status === 'locked:reset') return { text: t('settings.profile.resolve.why.reset') }
    const reason = sync.status?.detail[lock.key]?.invalidReason ?? 'unknown'
    return { reason, text: t(`settings.profile.resolve.why.invalid.${reason.replace(/-/g, '_')}`) }
  }

  const actionOf = (lock: LockView): Action => {
    if (lock.workspaceId === null) return 'settings'
    if (screen === 'slave') return 'switch'
    // The master on screen: the live store IS the master world — is that workspace's row in the sidebar?
    if (screen === 'master' && liveWorkspaces.some((w) => w.id === lock.workspaceId)) return 'show'
    return 'settings'
  }

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        data-testid="home-conflict-button"
        aria-label={label}
        title={label}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="mr-1 p-0.5 rounded text-amber-500 hover:bg-surface-secondary cursor-pointer focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active"
      >
        <WarningCircle size={14} />
      </button>
      {open && (
        <FloatingPanel title={t('profile.conflict.title')} anchorRef={buttonRef} onClose={() => setOpen(false)} testId="profile-conflict-panel">
          <ul className="flex flex-col">
            {locks.map((lock) => {
              const reason = why(lock)
              const action = actionOf(lock)
              return (
                <li
                  key={lock.key}
                  data-testid={`profile-conflict-row-${lock.key}`}
                  data-section={lock.key}
                  data-lock={lock.status}
                  className="flex flex-col gap-1 border-t border-border-default py-1.5 text-xs first:border-t-0"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span title={lock.key} className="min-w-0 truncate text-text-primary">{sectionLabel(lock.view)}</span>
                    {action === 'settings' && (
                      <button
                        type="button"
                        data-testid={`profile-conflict-settings-${lock.key}`}
                        onClick={() => {
                          setOpen(false)
                          setLocation(PROFILE_SETTINGS_PATH)
                        }}
                        className={BTN}
                      >
                        {t('profile.conflict.open_settings')}
                      </button>
                    )}
                    {action === 'show' && (
                      <button
                        type="button"
                        data-testid={`profile-conflict-show-${lock.key}`}
                        onClick={() => {
                          setOpen(false)
                          useConflictPanelStore.getState().openFor(lock.workspaceId!)
                        }}
                        className={BTN}
                      >
                        {t('profile.conflict.show_workspace')}
                      </button>
                    )}
                    {action === 'switch' && (
                      <button
                        type="button"
                        data-testid={`profile-conflict-switch-${lock.key}`}
                        // One switch at a time (useProfileSwitcherStore): a second would be refused anyway.
                        disabled={switching}
                        onClick={() => useProfileSwitcherStore.getState().chooseProfile(MASTER_PROFILE_ID)}
                        className={BTN}
                      >
                        {t('profile.conflict.switch_to_master')}
                      </button>
                    )}
                  </div>
                  <p data-testid={`profile-conflict-why-${lock.key}`} data-reason={reason.reason} className="text-text-secondary">{reason.text}</p>
                  {action === 'switch' && <p className="text-yellow-500">{t('profile.conflict.on_master')}</p>}
                </li>
              )
            })}
          </ul>
        </FloatingPanel>
      )}
    </>
  )
}

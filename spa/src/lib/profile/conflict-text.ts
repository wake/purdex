// The words for a section lock, shared by the conflict popover, the Profile page's current block and its resolve rows
// so the three cannot drift (#1485).
import type { SectionLock } from './executor'
import type { SectionView } from './sync-view'

type T = (key: string, params?: Record<string, string | number>) => string

/** What a section is called: its kind, or for a tabs section the workspace it belongs to. */
export function sectionLabelOf(t: T, view: SectionView): string {
  if (view.kind === 'other') return view.key // a kind this build does not know: nothing better to call it
  if (view.kind !== 'tabs') return t(`settings.profile.current.label.${view.kind}`)
  if (view.workspace === undefined) return t('settings.profile.current.label.tabs_unknown')
  // A workspace's name is the user's own text: into the sentence as it is.
  return view.workspace === null
    ? t('settings.profile.current.label.tabs_unseen')
    : t('settings.profile.current.label.tabs', { workspace: view.workspace })
}

/** Why a section is locked; `reason` is set only for an invalid lock (a missing one reads as `unknown`). */
export function lockWhyOf(t: T, status: SectionLock['status'], invalidReason: string | null | undefined): { reason?: string; text: string } {
  if (status === 'locked:conflict') return { text: t('settings.profile.resolve.why.conflict') }
  if (status === 'locked:reset') return { text: t('settings.profile.resolve.why.reset') }
  const reason = invalidReason ?? 'unknown'
  return { reason, text: t(`settings.profile.resolve.why.invalid.${reason.replace(/-/g, '_')}`) }
}

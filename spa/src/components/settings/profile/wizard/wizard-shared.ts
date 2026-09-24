// spa/src/components/settings/profile/wizard/wizard-shared.ts — what the wizard's files share: the look of its
// controls (the *Current* block's own), the master world as a subscription, and THE ONE PLACE a failure's reason
// becomes an i18n key. A reason that is not on a list gets the list's `other` sentence — so whatever a reason
// holds (an `Error.message`, say), it is never a key, and never text.
import { useSyncExternalStore } from 'react'
import { useWorkspaceStore } from '../../../../features/workspace/store'
import { readMasterWorld, type UnsettledReason } from '../../../../lib/profile/master-world'
import { useLocalProfilesStore } from '../../../../stores/useLocalProfilesStore'
import { useTabStore } from '../../../../stores/useTabStore'
import { ATTACH_REASONS, type SubStepId } from './wizard-run'

export const BTN =
  'shrink-0 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
/** A state the user should know of, not a fault (CurrentBlock's `NOTICE`). */
export const NOTICE = 'mt-2 text-xs text-yellow-500'
export const INPUT = 'bg-surface-secondary border border-border-default rounded px-2 py-1 text-xs text-text-primary w-48'

const KNOWN: Record<SubStepId, ReadonlySet<string>> = {
  promote: new Set(['master-attached', 'busy', 'unsettled', 'superseded', 'not-found', 'bad-name', 'bad-epoch', 'write-failed', 'rollback-incomplete']),
  save: new Set(['unsettled', 'bad-name', 'bad-world', 'write-failed']),
  attach: new Set(ATTACH_REASONS),
}

/** The api layer's `FailureReason`, plus `thrown`. */
const REQUEST = new Set(['network', 'timeout', 'aborted', 'contended', 'not-found', 'too-large', 'rejected', 'unauthorized', 'server', 'malformed', 'unknown-host', 'endpoint-changed', 'thrown'])

const snake = (reason: string): string => reason.replace(/-/g, '_')

/** The sentence for a failed sub-step of the run. */
export function reasonKey(step: SubStepId, reason: string): string {
  return `settings.profile.wizard.${step}.${KNOWN[step].has(reason) ? snake(reason) : 'other'}`
}

/** The sentence for a failed request (the list, the create). */
export function requestKey(reason: string): string {
  return `settings.profile.wizard.request.${REQUEST.has(reason) ? snake(reason) : 'thrown'}`
}

function subscribeWorld(fn: () => void): () => void {
  const stops = [useTabStore.subscribe(fn), useWorkspaceStore.subscribe(fn), useLocalProfilesStore.subscribe(fn)]
  return () => {
    for (const stop of stops) stop()
  }
}

const worldReason = (): UnsettledReason | null => {
  const read = readMasterWorld()
  return read.settled ? null : read.reason
}

/** Why nobody can read the master's world right now; null = it can be read. Plain subscriptions: no timer, and no
 *  recovery asked for (as CurrentBlock: a settings page does not rehydrate the app's stores). */
export function useMasterWorldReason(): UnsettledReason | null {
  return useSyncExternalStore(subscribeWorld, worldReason, worldReason)
}

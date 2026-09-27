// spa/src/lib/quick-replies.ts — the quick-reply dock's list, per host (R3,
// user decisions Q2/Q3). The list lives on each host's daemon
// (`useHostConfigStore`); a host that never wrote one shows the defaults.
// Mirrors `resume-templates.ts`: defaults + lookup + a subscribed hook.
import { useEffect } from 'react'
import type { QuickReply } from './host-config-api'
import { useHostConfigStore, type HostConfigEntry } from '../stores/useHostConfigStore'

/** What a host with nothing configured shows (Q3). Editable and deletable. */
export const DEFAULT_QUICK_REPLIES: readonly QuickReply[] = Object.freeze([
  Object.freeze({ id: 'continue', text: 'continue' }),
  Object.freeze({ id: 'run-tests', text: 'run the tests' }),
  Object.freeze({ id: 'explain', text: 'explain that' }),
])

/**
 * The list to show for a host.
 *
 * Defaults while the entry is not loaded, when the daemon predates the
 * collection, or when the collection was never written (revision 0). Once
 * written, the stored items — **even an empty list**, which means "no dock".
 */
export function effectiveQuickReplies(entry: HostConfigEntry | undefined): readonly QuickReply[] {
  if (!entry || entry.status !== 'ready' || !entry.quickRepliesSupported) return DEFAULT_QUICK_REPLIES
  if (entry.revisions.quickReplies === 0) return DEFAULT_QUICK_REPLIES
  return entry.quickReplies
}

/** The host's quick replies; loads its host config if nothing has yet. */
export function useQuickReplies(hostId: string): readonly QuickReply[] {
  useEffect(() => {
    void useHostConfigStore.getState().ensureLoaded(hostId)
  }, [hostId])
  return useHostConfigStore((s) => effectiveQuickReplies(s.byHost[hostId]))
}

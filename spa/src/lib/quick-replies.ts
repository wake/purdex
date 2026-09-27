// spa/src/lib/quick-replies.ts — the quick-reply dock's list, per host (R3,
// user decisions Q2/Q3). The list lives on each host's daemon
// (`useHostConfigStore`); a host that never wrote one shows the defaults.
// Mirrors `resume-templates.ts`: defaults + lookup + a subscribed hook.
import { useEffect } from 'react'
import type { QuickReply } from './host-config-api'
import { useHostConfigStore, type HostConfigEntry } from '../stores/useHostConfigStore'

/** The daemon's limits (`internal/module/hostconfig/validate.go`). */
export const MAX_QUICK_REPLIES = 20
export const QUICK_REPLY_MAX_BYTES = 1000

/** What a host with nothing configured shows (Q3). Editable and deletable. */
export const DEFAULT_QUICK_REPLIES: readonly QuickReply[] = Object.freeze([
  Object.freeze({ id: 'continue', text: 'continue' }),
  Object.freeze({ id: 'run-tests', text: 'run the tests' }),
  Object.freeze({ id: 'explain', text: 'explain that' }),
])

const NO_QUICK_REPLIES: readonly QuickReply[] = Object.freeze([])

const utf8 = new TextEncoder()

/**
 * Why `text` (already trimmed) cannot be saved, as a locale key, or `null`.
 * The same rules the daemon enforces, so a bad text never costs a round trip.
 */
export function validateQuickReplyText(text: string): string | null {
  if (text === '') return 'hosts.quick_replies.error_empty'
  if (text.includes('\0') || utf8.encode(text).length > QUICK_REPLY_MAX_BYTES) return 'hosts.quick_replies.error_invalid'
  return null
}

/**
 * The list to show for a host.
 *
 * A tap sends at once (Q1) and an emptied list means "no dock" (Q3), so the
 * defaults are shown only when the host is KNOWN to have none of its own:
 *
 * - The collection loaded once (`quickRepliesSupported`) → what it held,
 *   whatever the current status: a failed or running reload keeps the last
 *   known copy. Revision 0 (never written) → the defaults; otherwise the
 *   stored items, **even an empty list**.
 * - The daemon predates the collection (`unsupported`, or loaded without it)
 *   → the defaults.
 * - Anything else — no entry, idle, loading, an error before any success —
 *   is unknown → nothing (no dock), never a guess the user may have deleted.
 */
export function effectiveQuickReplies(entry: HostConfigEntry | undefined): readonly QuickReply[] {
  if (!entry) return NO_QUICK_REPLIES
  if (entry.quickRepliesSupported) {
    return entry.revisions.quickReplies === 0 ? DEFAULT_QUICK_REPLIES : entry.quickReplies
  }
  if (entry.status === 'unsupported' || entry.status === 'ready') return DEFAULT_QUICK_REPLIES
  return NO_QUICK_REPLIES
}

/** The host's quick replies; loads its host config if nothing has yet. */
export function useQuickReplies(hostId: string): readonly QuickReply[] {
  useEffect(() => {
    void useHostConfigStore.getState().ensureLoaded(hostId)
  }, [hostId])
  return useHostConfigStore((s) => effectiveQuickReplies(s.byHost[hostId]))
}

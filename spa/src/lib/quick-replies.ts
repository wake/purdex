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

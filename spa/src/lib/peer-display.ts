/**
 * Shared rendering of the daemon's peer vocabulary.
 *
 * The status bar and the tab panel both turn a row's `reason` into text, and
 * they disagreed: the panel guarded against a reason this build does not know
 * about, the status bar looked the key up blindly and would have rendered
 * `peer.reason.<whatever>` into a tooltip. One copy, one behaviour.
 */

import type { PeerRow } from '../stores/usePeerStore'
import { addressName } from './names/seat'

type T = (key: string, params?: Record<string, string | number>) => string

/** The reasons the daemon gives for a row that cannot be delivered to. */
export const PEER_REASONS = new Set(['no_agent', 'not_cc', 'inbox_dead', 'proxy', 'ambiguous'])

/**
 * A reason is the actionable half of "not deliverable", so it is never elided.
 * A reason this build does not know about is shown raw rather than as a
 * missing-translation key: the daemon's vocabulary can outrun the SPA's, and a
 * raw `some_new_reason` still tells the reader something, where
 * `peer.reason.some_new_reason` tells them only that we failed.
 */
export function reasonText(reason: string, t: T): string {
  if (!reason) return t('peer.none')
  return PEER_REASONS.has(reason) ? t(`peer.reason.${reason}`) : reason
}

/**
 * The peer segment's two strings: what the row shows, and what a click copies.
 *
 * Display drops the host; the clipboard keeps it, and keeps the ref. What
 * gets copied is what gets pasted into a handoff and used hours later —
 * exactly the window in which a name drifts or is taken by someone else. The
 * prettier string is the weaker one; it does not belong on the clipboard.
 */
export function peerIdText(row: PeerRow | null): { display: string; value: string } {
  const address = row?.address ?? ''
  if (address === '') return { display: '', value: '' }
  const name = addressName(address)
  const ref = row?.ref ?? ''
  // A session whose registry name is not routable is addressed by its ref, so
  // the address already ends in it and there is no name to bracket: appending
  // would render `_q34psn [q34psn]`, which says the same thing twice.
  if (ref === '' || name === ref) return { display: name, value: address }
  const bracketed = ` [${ref.replace(/^_/, '')}]`
  return { display: name + bracketed, value: address + bracketed }
}

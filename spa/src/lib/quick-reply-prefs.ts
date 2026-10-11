// spa/src/lib/quick-reply-prefs.ts — how a host meets the quick-reply switches (quick-replies v2 spec §3, QR-2b). The prefs store is keyed by WIRE host
// id; a local host reads and writes the key `lookKeyOf` names (its `d1_…`, or its local id while that entry awaits the pass's re-key), and a ref that is
// no local host is its own key. The same rule as `purdex-host-looks`, so the two agree on which host an entry is.
import { useMemo } from 'react'
import { lookKeyOf, useHostStore } from '../stores/useHostStore'
import { useQuickReplyPrefsStore } from '../stores/useQuickReplyPrefsStore'
import type { QuickReply } from './host-config-api'

const NONE: readonly string[] = Object.freeze([])

/** The key `hostId`'s off-list lives under now. */
export function quickReplyPrefsKey(hostId: string): string {
  const hosts = useHostStore.getState().hosts
  if (!Object.hasOwn(hosts, hostId)) return hostId
  return lookKeyOf(hosts[hostId], useQuickReplyPrefsStore.getState().disabled)
}

/** After a successful PUT: the host's off-list keeps only the ids the daemon's answer still holds. */
export function pruneQuickReplyPrefs(hostId: string, keepIds: readonly string[]): void {
  useQuickReplyPrefsStore.getState().prune(quickReplyPrefsKey(hostId), keepIds)
}

/** The ids switched off for `hostId` (its own off-list), subscribed. */
export function useDisabledQuickReplyIds(hostId: string): readonly string[] {
  const host = useHostStore((s) => (Object.hasOwn(s.hosts, hostId) ? s.hosts[hostId] : undefined))
  const off = useQuickReplyPrefsStore((s) => {
    const key = host === undefined ? hostId : lookKeyOf(host, s.disabled)
    return Object.hasOwn(s.disabled, key) ? s.disabled[key] : undefined
  })
  return off ?? NONE
}

/** `replies` without the row's switch (off: none) and without the host's switched-off items. */
export function useShownReplies(hostId: string, replies: readonly QuickReply[]): readonly QuickReply[] {
  const rowEnabled = useQuickReplyPrefsStore((s) => s.rowEnabled)
  const off = useDisabledQuickReplyIds(hostId)
  return useMemo(() => (!rowEnabled ? [] : off.length === 0 ? replies : replies.filter((r) => !off.includes(r.id))), [rowEnabled, off, replies])
}

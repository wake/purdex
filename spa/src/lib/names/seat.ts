// spa/src/lib/names/seat.ts — the one place that turns a peer address or virtual name into a persona (name pool v2
// spec §9.2). Every inline "slice after the slash" in the App goes through here.
import { useMemo } from 'react'
import { currentCollection, usePersonaStore } from '../../stores/usePersonaStore'
import { useSourceId } from '../../stores/usePersonaSourceStore'
import type { PersonaCollection } from './persona-types'

export interface Seat {
  code: string
  name: string
  /** The stored raster (`data:image/…`), or `""` when the App should draw initials. */
  avatar: string
}

/** The name part of an address: what follows the last `/` (the whole string when there is none). */
export function addressName(address: string): string {
  return address.slice(address.lastIndexOf('/') + 1)
}

/** The persona code a virtual name ends in: the suffix after the last `-`. */
function codeOf(address: string): string {
  const name = addressName(address)
  return name.slice(name.lastIndexOf('-') + 1)
}

/** `address` may be `<host>/<name>`, or the bare virtual name. Retired personas still resolve (L5). */
export function seatOf(address: string): Seat | null {
  return seatFrom(usePersonaStore.getState().current(), address)
}

function seatFrom(collection: PersonaCollection | null, address: string): Seat | null {
  const code = codeOf(address)
  if (code === '') return null
  const hit = collection?.items.find((p) => p.code === code)
  return hit ? { code: hit.code, name: hit.name, avatar: hit.avatar_data } : null
}

/** The same lookup for a component: it re-renders when the persona collection changes. */
export function useSeat(address: string): Seat | null {
  // The selector returns the stored collection itself (a stable reference); the seat is derived outside it.
  const sourceId = useSourceId()
  const collection = usePersonaStore((s) => currentCollection(s.byHost, sourceId))
  return useMemo(() => seatFrom(collection, address), [collection, address])
}

/** Whether a message's sender may be shown as a person: another session whose identity the transport established. A mod's notice (kind plugin) is no person, and an
 *  `unverified` sender is only what the message text says (the daemon's own caveat): its name is never resolved to a persona's face. */
export function isVerifiedPeer(item: { from?: { kind: string; name?: string; unverified?: boolean } }): boolean {
  return item.from?.kind === 'peer' && item.from.unverified !== true
}

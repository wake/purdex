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

/** The name a tooltip or a title shows for a seat (spec §9.3): its persona's name, else `fallback` (the title the seat had). `address` must be the daemon's resolved one. Plain,
 *  without a subscription: the component calls `usePersonaRevision()` once. */
export function seatName(address: string, fallback: string): string {
  return seatOf(address)?.name ?? fallback
}

/** For a component that formats names through the plain functions (`seatLabel`, `approvalSessionLabel`), which read the store without subscribing: call this once at the top
 *  and the component redraws when the persona collection loads or changes. The returned collection is only there to be depended on (a memo's dependency list). */
export function usePersonaRevision(): PersonaCollection | null {
  const sourceId = useSourceId()
  return usePersonaStore((s) => currentCollection(s.byHost, sourceId))
}

/** What a session is called where a string is needed (a toast, a push, a table cell): the persona's name with the title after it. `null` when the address names no
 *  persona, so the caller keeps the label it had. Only for an address the daemon resolved itself (a registry origin), never for a sender read out of message text. */
export function seatLabel(address: string, title?: string): string | null {
  const seat = seatOf(address)
  if (seat === null) return null
  const t = title?.trim() ?? ''
  return t !== '' ? `${seat.name} · ${t}` : seat.name
}

/** Whether a message's sender may be shown as a person: another session whose identity the transport established. A mod's notice (kind plugin) is no person, and an
 *  `unverified` sender is only what the message text says (the daemon's own caveat): its name is never resolved to a persona's face. */
export function isVerifiedPeer(item: { from?: { kind: string; name?: string; unverified?: boolean } }): boolean {
  return item.from?.kind === 'peer' && item.from.unverified !== true
}

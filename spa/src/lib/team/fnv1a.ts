// spa/src/lib/team/fnv1a.ts — FNV-1a, 32-bit, over a string's UTF-16 code units. Shared by the approval notification's
// dedup key (approval-notify.ts) and the team colour index (team-views.ts): both need a hash that is the same in every
// window and after every restart, which `Math.random` and object identity are not.
export const FNV_OFFSET_32 = 0x811c9dc5
const FNV_PRIME_32 = 0x01000193

/** FNV-1a over the string's UTF-16 code units, 32-bit, from the given basis. */
export function fnv1a32(s: string, basis: number = FNV_OFFSET_32): number {
  let h = basis >>> 0
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, FNV_PRIME_32) >>> 0
  }
  return h
}

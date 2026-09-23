// spa/src/components/settings/profile/profile-rules.ts — the small rules of Settings › Profile that are
// DECISIONS rather than facts, each in one place so that changing one's mind is one edit.
import { normalizeLocalProfileName, type ProfileAppearance } from '../../../stores/useLocalProfilesStore'
import { DEVICE_NAME_MAX_CODE_POINTS } from '../../../lib/device-name'
import type { PendingDetach } from '../../../stores/useProfileStore'

/**
 * THE COLOUR NEEDS AN ICON (confirmed by the user, 2026-09-22: an icon first, then a colour). A profile's
 * colour tints its Phosphor icon; with no icon chosen the profile shows the Purdex logo, a bitmap the colour
 * does nothing to (`ProfileIcon`). So while the logo shows, the colour control is disabled and says why — and a
 * colour already stored is KEPT, not cleared: it comes back with the next icon. `false` = offer the colour
 * always (it then shows in the editor's swatch only).
 */
export const COLOR_NEEDS_ICON = true

/** May the colour control be used for a profile that looks like `look`? */
export function canTintProfile(look: ProfileAppearance): boolean {
  return !COLOR_NEEDS_ICON || look.icon !== undefined
}

/** What `lib/device-name.ts` falls back to; `effectiveDeviceName` never hands over a blank, this is for whoever does. */
const FALLBACK_NAME = 'Browser'

/**
 * The name offered for a new local profile: this device's name (spec §4.9: a slave saved before a pull is "named
 * after the host"), with the next free number when a profile is already called exactly that. Duplicate names are
 * the store's to allow — a DEFAULT must not produce one. Compared as the store would keep them, and the number
 * survives the 64 code point cut: the base gives way, not the suffix.
 */
export function defaultSlaveName(deviceName: string, taken: readonly string[]): string {
  const base = normalizeLocalProfileName(deviceName) ?? FALLBACK_NAME
  const used = new Set(taken)
  if (!used.has(base)) return base
  for (let n = 2; ; n++) {
    const suffix = ` ${n}`
    const room = DEVICE_NAME_MAX_CODE_POINTS - suffix.length
    const candidate = `${Array.from(base).slice(0, room).join('').trimEnd()}${suffix}`
    if (!used.has(candidate)) return candidate
  }
}

/** Which master a SOT profile action was opened under: the host AND the profile this device syncs with (the
 *  second decides what may be deleted). JSON, so that no id can pass for part of another pair. */
export function sotScopeOf(hostId: string, attachedProfileId: string): string {
  return JSON.stringify([hostId, attachedProfileId])
}

/**
 * May an action that was opened under `openedUnder`, on profile `profileId`, still be sent? Only under the very
 * scope it was opened under, and only while the fetched list still holds that profile. `p2` on host B is not the
 * `p2` the user was asked about on host A — the master is every window's to move (review F1).
 */
export function sotActionStillValid(openedUnder: string, scopeNow: string, profileId: string, rowsNow: readonly { id: string }[] | null): boolean {
  return openedUnder === scopeNow && rowsNow !== null && rowsNow.some((row) => row.id === profileId)
}

/**
 * Why a SOT profile may NOT be offered for deletion; null = it may. Decided by the FETCHED index: anybody attached
 * blocks it — and so does being the profile THIS device syncs with (`attachedProfileId`; null when none), listed
 * attached or not (its attachment may not have been written yet): stop sync first. The index can be old, so the
 * daemon still has the last word (409 `attached`).
 */
export function sotDeleteBlocked(row: { id: string; attachments: readonly unknown[] }, attachedProfileId: string | null): 'current' | 'attached' | null {
  if (row.id === attachedProfileId) return 'current'
  return row.attachments.length > 0 ? 'attached' : null
}

/** The wizard's scope for a SOT delete: the host chosen AND the step it was opened on — a confirmation opened on
 *  step 2 for host A is not one for host B, nor one that may be sent from any other step. */
export function wizardSotScopeOf(hostId: string | null, step: string): string {
  return JSON.stringify(['wizard', hostId, step])
}

/** A record's key as a test id: `<host>.<profile>.<endpoint>`, everything but letters, digits, `.`, `-` and `_`
 *  replaced by `_` (the endpoint's colon). For the acceptance run to address one notice; React keys use the real key. */
export function pendingDetachTestId(left: Pick<PendingDetach, 'hostId' | 'profileId' | 'endpoint'>): string {
  return `${left.hostId}.${left.profileId}.${left.endpoint ?? 'unknown'}`.replace(/[^A-Za-z0-9._-]/g, '_')
}

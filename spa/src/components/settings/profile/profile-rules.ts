// spa/src/components/settings/profile/profile-rules.ts — the small rules of Settings › Profile that are
// DECISIONS rather than facts, each in one place so that changing one's mind is one edit.
import { normalizeLocalProfileName, type ProfileAppearance } from '../../../stores/useLocalProfilesStore'
import { DEVICE_NAME_MAX_CODE_POINTS } from '../../../lib/device-name'

/**
 * THE COLOUR NEEDS AN ICON (the main session's default, 2026-09-22; the user has not ruled on it). A profile's
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

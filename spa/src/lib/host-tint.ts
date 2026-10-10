// spa/src/lib/host-tint.ts — the host's colour as the ground of a row (the deck's session status row, c9d9c7-52 pt 2).
// The colour is mixed into the theme's own surface, not laid over it with alpha, so the ground is always the surface plus a fixed share
// of the host colour and the row's text (--text-primary) keeps its contrast in every theme (pinned in host-tint.test.ts).
import { useHostLook } from './host-look'
import { resolveHostColorSet, type HostColorMode } from './host-color'

/** A middle tone: clearly the host's colour, still dark/light enough in each theme for --text-primary to read at 4.5:1 for every preset
 *  colour (pinned in host-tint.test.ts). A hand-typed extreme colour (pure white on nord, say) is not guaranteed that. */
export const HOST_TINT_PCT = 34

export function hostTintBackground(hex: string): string {
  return `color-mix(in srgb, ${hex} ${HOST_TINT_PCT}%, var(--surface-secondary))`
}

/** The row's background for `hostId`'s colour, or undefined when the host has none (the row keeps its plain look). */
export function useHostTint(hostId: string | null, mode: HostColorMode): string | undefined {
  const look = useHostLook(hostId || null)
  const hex = resolveHostColorSet({ colors: look.colors, color: look.color }, mode)?.main.color
  return hex ? hostTintBackground(hex) : undefined
}

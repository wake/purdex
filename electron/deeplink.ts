// Pure deeplink target-selection, split out of main.ts so it is unit-testable
// without booting Electron (main.ts runs app-level side effects on import).

// DeeplinkWindow is the minimal shape target selection needs from a renderer
// window: a way to know it is still alive. BrowserWindow satisfies it.
export interface DeeplinkWindow {
  isDestroyed(): boolean
}

// pickDeeplinkTarget chooses the SINGLE window a deeplink should land in, so a
// multi-window app opens exactly one detail page instead of one per window (each
// window running the resolver and stealing focus). Preference: the focused window
// when it is among the ready candidates, else the first ready candidate
// (insertion order). Destroyed windows are ignored. Returns null when no ready
// window exists — the caller then buffers the deeplink for the next spa:ready
// (cold start).
export function pickDeeplinkTarget<T extends DeeplinkWindow>(
  ready: readonly T[],
  focused: T | null,
): T | null {
  const live = ready.filter((w) => !w.isDestroyed())
  if (live.length === 0) return null
  if (focused && !focused.isDestroyed() && live.includes(focused)) return focused
  return live[0]
}

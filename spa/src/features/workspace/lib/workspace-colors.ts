/**
 * Derive consistent workspace color variants from a single base color.
 * Used by WorkspaceChip, ActivityBar, WorkspaceSettingsPage, etc.
 *
 * All locations share the same derivation to ensure visual consistency.
 */
export function workspaceColorStyle(color: string) {
  return {
    /** Text / icon foreground — the raw color */
    fg: color,
    /** Subtle background fill — 20% opacity */
    bg: color + '33',
    /** Border / ring / emphasis — 50% opacity */
    border: color + '80',
  }
}

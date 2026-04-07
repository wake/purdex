/**
 * Derive consistent workspace color variants from a single base color.
 * Used by WorkspaceSettingsPage and WorkspaceChip.
 */
export function workspaceColorStyle(color: string) {
  return {
    /** Text / icon foreground — the raw color */
    fg: color,
    /** Background fill — 27% opacity */
    bg: color + '44',
    /** Border / ring / emphasis — 67% opacity */
    border: color + 'aa',
  }
}

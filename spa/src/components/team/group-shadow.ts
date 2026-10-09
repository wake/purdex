// spa/src/components/team/group-shadow.ts — the box-shadow of a team group tab (TI-6, spec §4.2, §5). The Shadow trial ended
// (user 2026-10-10): the shadow stays the crisp top-right V0. One function shared by the tab and its tests.

/** The colour the shadow and the wash draw with: the light theme darkens the team colour first (§5). */
export function shadowBase(color: string, theme: string): string {
  return theme === 'light' ? `color-mix(in oklab, ${color}, black 25%)` : color
}

/** The crisp top-right 1px shadow at 70 % in oklab. */
export function groupShadow(color: string, theme: string): string {
  return `1px -1px 0 color-mix(in oklab, ${shadowBase(color, theme)} 70%, transparent)`
}

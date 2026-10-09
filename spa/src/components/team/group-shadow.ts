// spa/src/components/team/group-shadow.ts — the box-shadow of a team group tab for each trial variant (TI-6, spec §4.2 Shadow
// trial, §5). One function shared by the tab and its tests; removed with the trial once the user picks a variant.

export type GroupShadowVariant = 'v0' | 'v1' | 'v2' | 'v3'
export const GROUP_SHADOW_VARIANTS: readonly GroupShadowVariant[] = ['v0', 'v1', 'v2', 'v3']
export const DEFAULT_GROUP_SHADOW: GroupShadowVariant = 'v2'

export const isGroupShadowVariant = (v: unknown): v is GroupShadowVariant => GROUP_SHADOW_VARIANTS.includes(v as GroupShadowVariant)

/** The colour the shadow and the wash draw with: the light theme darkens the team colour first (§5). */
export function shadowBase(color: string, theme: string): string {
  return theme === 'light' ? `color-mix(in oklab, ${color}, black 25%)` : color
}

/** `base` at `pct` % in oklab. */
const mixed = (base: string, pct: number): string => `color-mix(in oklab, ${base} ${pct}%, transparent)`

export function groupShadow(variant: GroupShadowVariant, color: string, theme: string): string {
  const b = shadowBase(color, theme)
  switch (variant) {
    case 'v1': return `1px -1px 0 ${mixed(b, 70)}, 0 0 4px ${mixed(b, 30)}`
    case 'v2': return `1px -1px 0 ${mixed(b, 70)}, -1px 1px 3px ${mixed(b, 40)}`
    case 'v3': return `1px -1px 0 ${mixed(b, 60)}, -2px 2px 5px ${mixed(b, 35)}`
    default: return `1px -1px 0 ${mixed(b, 70)}`
  }
}

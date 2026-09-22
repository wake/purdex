export type PresetKey = '3col' | '2col' | '1col'

export interface LayoutPreset {
  enabled: boolean
  columns: string[][]
}

export function resolvePreset(
  isWide: boolean,
  isMid: boolean,
  presets: Record<PresetKey, LayoutPreset>,
): PresetKey {
  const desired: PresetKey = isWide ? '3col' : isMid ? '2col' : '1col'
  const chain: PresetKey[] =
    desired === '3col' ? ['3col', '2col', '1col']
    : desired === '2col' ? ['2col', '1col']
    : ['1col']
  return chain.find((k) => presets[k].enabled) ?? '1col'
}

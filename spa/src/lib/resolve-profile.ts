export type ProfileKey = '3col' | '2col' | '1col'

export interface Profile {
  enabled: boolean
  columns: string[][]
}

export function resolveProfile(
  isWide: boolean,
  isMid: boolean,
  profiles: Record<ProfileKey, Profile>,
): ProfileKey {
  const desired: ProfileKey = isWide ? '3col' : isMid ? '2col' : '1col'
  const chain: ProfileKey[] =
    desired === '3col' ? ['3col', '2col', '1col']
    : desired === '2col' ? ['2col', '1col']
    : ['1col']
  return chain.find((k) => profiles[k].enabled) ?? '1col'
}

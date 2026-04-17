import { useMediaQuery } from './useMediaQuery'

/** Returns viewport tier flags. Used by layout code to pick a profile. */
export function useBreakpoint(): { isWide: boolean; isMid: boolean } {
  const isWide = useMediaQuery('(min-width: 1024px)')
  const isMid = useMediaQuery('(min-width: 640px)')
  return { isWide, isMid }
}

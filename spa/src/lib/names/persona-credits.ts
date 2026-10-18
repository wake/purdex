// spa/src/lib/names/persona-credits.ts — who drew the avatar styles the persona page offers, and under what licence. From
// the `LICENSE.md` of @dicebear/styles 10.6.0 (each style has its own licence there). Micah is CC BY 4.0 and must be
// credited; the others are CC0 and are listed as courtesy (name pool v2 spec §9.1).
import type { DicebearStyle } from './persona-draw'

export interface AvatarCredit {
  style: DicebearStyle
  label: string
  artist: string
  artistUrl: string
  license: 'CC0 1.0' | 'CC BY 4.0'
  licenseUrl: string
}

const CC0 = 'https://creativecommons.org/publicdomain/zero/1.0/'
const CC_BY = 'https://creativecommons.org/licenses/by/4.0/'

export const AVATAR_CREDITS: readonly AvatarCredit[] = [
  { style: 'lorelei', label: 'Lorelei', artist: 'Lisa Wischofsky', artistUrl: 'https://www.instagram.com/lischi_art/', license: 'CC0 1.0', licenseUrl: CC0 },
  { style: 'notionists', label: 'Notionists', artist: 'Zoish', artistUrl: 'https://bio.link/heyzoish', license: 'CC0 1.0', licenseUrl: CC0 },
  { style: 'open-peeps', label: 'Open Peeps', artist: 'Pablo Stanley', artistUrl: 'https://twitter.com/pablostanley', license: 'CC0 1.0', licenseUrl: CC0 },
  { style: 'micah', label: 'Micah', artist: 'Micah Lanier', artistUrl: 'https://dribbble.com/micahlanier', license: 'CC BY 4.0', licenseUrl: CC_BY },
  { style: 'shapes', label: 'Shapes', artist: 'DiceBear', artistUrl: 'https://www.dicebear.com', license: 'CC0 1.0', licenseUrl: CC0 },
  { style: 'clay', label: 'Clay', artist: 'DiceBear', artistUrl: 'https://www.dicebear.com', license: 'CC0 1.0', licenseUrl: CC0 },
]

export function creditsRequiringAttribution(): AvatarCredit[] {
  return AVATAR_CREDITS.filter((c) => c.license === 'CC BY 4.0')
}

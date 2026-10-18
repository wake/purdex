import { describe, it, expect } from 'vitest'
import { AVATAR_CREDITS, creditsRequiringAttribution } from './persona-credits'
import { DICEBEAR_STYLES } from './persona-draw'

describe('avatar credits', () => {
  it('has one entry for each style the page can draw', () => {
    expect(AVATAR_CREDITS.map((c) => c.style).sort()).toEqual([...DICEBEAR_STYLES].sort())
  })

  it('only Micah requires attribution (CC BY 4.0, Micah Lanier); the rest are CC0', () => {
    expect(creditsRequiringAttribution().map((c) => c.style)).toEqual(['micah'])
    const micah = AVATAR_CREDITS.find((c) => c.style === 'micah')!
    expect(micah.license).toBe('CC BY 4.0')
    expect(micah.artist).toBe('Micah Lanier')
    for (const c of AVATAR_CREDITS.filter((c) => c.style !== 'micah')) expect(c.license).toBe('CC0 1.0')
  })

  it('names an artist and a link for each', () => {
    for (const c of AVATAR_CREDITS) {
      expect(c.artist).not.toBe('')
      expect(c.artistUrl.startsWith('https://')).toBe(true)
      expect(c.licenseUrl.startsWith('https://')).toBe(true)
    }
  })
})

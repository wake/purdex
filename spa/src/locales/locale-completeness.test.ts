import { describe, it, expect } from 'vitest'
import en from './en.json'
import zhTW from './zh-TW.json'

describe('locale completeness', () => {
  const enKeys = Object.keys(en).sort()
  const zhKeys = Object.keys(zhTW).sort()

  it('en.json and zh-TW.json have identical key sets', () => {
    const missingInZh = enKeys.filter((k) => !zhKeys.includes(k))
    const extraInZh = zhKeys.filter((k) => !enKeys.includes(k))
    expect(missingInZh, 'Keys in en.json but missing in zh-TW.json').toEqual([])
    expect(extraInZh, 'Keys in zh-TW.json but not in en.json').toEqual([])
  })

  it('no empty translation values in en.json', () => {
    const empty = Object.entries(en).filter(([, v]) => !v.trim())
    expect(empty.map(([k]) => k), 'Empty values in en.json').toEqual([])
  })

  it('no empty translation values in zh-TW.json', () => {
    const empty = Object.entries(zhTW).filter(([, v]) => !v.trim())
    expect(empty.map(([k]) => k), 'Empty values in zh-TW.json').toEqual([])
  })

  // The Live Mode gate explains to the user why their file opened raw, so it is
  // the one place a half-translated string is actively confusing. Capitalised
  // terms (HTML, Live Mode) are product/UI names this file keeps in English by
  // convention; a lowercase English word is a leftover fragment.
  it('the Live Mode gate messages are fully translated in zh-TW', () => {
    const gateEntries = Object.entries(zhTW as Record<string, string>)
      .filter(([key]) => key.startsWith('editor.live_mode.'))
    expect(gateEntries.length).toBeGreaterThan(0)

    for (const [key, value] of gateEntries) {
      expect(value.replace(/\{\{\w+\}\}/g, ''), key).not.toMatch(/\b[a-z]{2,}\b/)
    }
  })
})

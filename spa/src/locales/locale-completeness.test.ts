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

  // The peer namespace (peer-info-panel spec §8.9). The whole-file check above
  // would catch a key added to one side and forgotten in the other, but it
  // reports the whole file; this one fails with the feature's own name on it,
  // and it also pins the two things the whole-file check does not look at: that
  // the namespace exists at all, and that a translation did not quietly drop a
  // placeholder — `{{what}}` missing from `peer.copied` renders half a sentence
  // to whichever half of the fleet runs the other locale.
  describe('the peer namespace', () => {
    const peerKeys = (o: Record<string, string>) => Object.keys(o).filter((k) => k.startsWith('peer.')).sort()
    const enPeer = peerKeys(en as Record<string, string>)
    const zhPeer = peerKeys(zhTW as Record<string, string>)

    it('exists in both files', () => {
      expect(enPeer.length).toBeGreaterThan(0)
      expect(zhPeer.length).toBeGreaterThan(0)
    })

    it('has identical key sets', () => {
      expect(zhPeer, 'peer.* keys differ between en.json and zh-TW.json').toEqual(enPeer)
    })

    it('keeps every placeholder in the translation', () => {
      const placeholders = (v: string) => (v.match(/\{\{\w+\}\}/g) ?? []).sort()
      for (const key of enPeer) {
        const enValue = (en as Record<string, string>)[key]
        const zhValue = (zhTW as Record<string, string>)[key]
        expect(placeholders(zhValue), key).toEqual(placeholders(enValue))
      }
    })
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

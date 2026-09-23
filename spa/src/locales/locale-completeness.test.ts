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

  // The host transfer namespace (host ownership spec §6.1 / §6.4; H4b). The trust sentence is the one piece of copy
  // the spec fixes word for word, in both locales, and a dropped `{{relay}}` would hide WHICH host holds the tokens.
  describe('the hosts.transfer namespace', () => {
    const transferKeys = (o: Record<string, string>) => Object.keys(o).filter((k) => k.startsWith('hosts.transfer.')).sort()
    const enT = transferKeys(en as Record<string, string>)
    const zhT = transferKeys(zhTW as Record<string, string>)

    it('exists with identical key sets', () => {
      expect(enT.length).toBeGreaterThan(0)
      expect(zhT).toEqual(enT)
    })

    it('keeps every placeholder in the translation', () => {
      const placeholders = (v: string) => (v.match(/\{\{\w+\}\}/g) ?? []).sort()
      for (const key of enT) {
        expect(placeholders((zhTW as Record<string, string>)[key]), key).toEqual(placeholders((en as Record<string, string>)[key]))
      }
    })

    it('has an error text for every failure reason', () => {
      const reasons = [
        'bad_payload', 'bad_request', 'too_large', 'capacity', 'rate_limited', 'rate_limited_later', 'invalid_code',
        'unavailable', 'unauthorized', 'no_token', 'unsupported', 'network', 'timeout', 'malformed', 'unknown_host',
      ]
      for (const r of reasons) expect(enT, r).toContain(`hosts.transfer.error.${r}`)
    })

    it('carries the spec §6.1 trust sentence in both locales', () => {
      expect((en as Record<string, string>)['hosts.transfer.trust']).toBe(
        '{{relay}} will hold the access tokens of the hosts you share, readable by that host, until the code is used or expires (10 min). Only relay through a host you trust.',
      )
      expect((zhTW as Record<string, string>)['hosts.transfer.trust']).toBe(
        '{{relay}} 會保存你分享的主機的存取 token，直到代碼被使用或過期（10 分鐘）；這段期間這台主機讀得到它們。只透過你信任的主機中轉。',
      )
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

import { describe, expect, it } from 'vitest'
import { reasonText } from './peer-display'

const t = (key: string) => `T:${key}`

describe('reasonText', () => {
  it('translates every reason the daemon documents', () => {
    for (const reason of ['no_agent', 'not_cc', 'inbox_dead', 'proxy', 'ambiguous']) {
      expect(reasonText(reason, t)).toBe(`T:peer.reason.${reason}`)
    }
  })

  it('renders an empty reason as "no peer"', () => {
    expect(reasonText('', t)).toBe('T:peer.none')
  })

  // The daemon can grow a reason before the SPA learns its translation. Showing
  // the raw token still tells the reader something; showing
  // `peer.reason.<token>` tells them only that we failed. Both call sites go
  // through here so they cannot drift apart again.
  it('shows an unknown reason raw, never as a missing-translation key', () => {
    expect(reasonText('some_future_reason', t)).toBe('some_future_reason')
  })
})

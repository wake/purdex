// Pins hash.ts to the shared canonical-hash fixture. The daemon's Go port (internal/profilehash) is pinned to the same file,
// so the two implementations cannot drift apart unnoticed (QR pairing spec §5.2 gate 4).
import { describe, expect, it } from 'vitest'
import fixtureJson from './__fixtures__/canonical-hash.json'
import { hashSection } from './hash'

const fixture = fixtureJson as { cases: { name: string; json: string; hash: string }[] }

describe('canonical-hash fixture', () => {
  it('has the cases the Go port also checks', () => {
    expect(fixture.cases.length).toBeGreaterThanOrEqual(20)
  })
  for (const c of fixture.cases) {
    it(c.name, async () => {
      expect(await hashSection(JSON.parse(c.json))).toBe(c.hash)
    })
  }
})

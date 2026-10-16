import { describe, it, expect } from 'vitest'
import { aliasShapeOk, fleetRowError, probeNameError } from './config-validate'

describe('probeNameError (probehosts.go: 1 to 40 runes after trimming, no control characters)', () => {
  it('accepts a plain name and exactly 40 runes (counted as runes, not bytes)', () => {
    expect(probeNameError('nas')).toBeNull()
    expect(probeNameError('機'.repeat(40))).toBeNull()
  })
  it('refuses empty and blank names', () => {
    expect(probeNameError('')).toBe('fleet_hosts.probe.err_name')
    expect(probeNameError('   ')).toBe('fleet_hosts.probe.err_name')
  })
  it('trims before it counts: 40 runes plus padding pass, 41 do not', () => {
    expect(probeNameError(`  ${'x'.repeat(40)}  `)).toBeNull()
    expect(probeNameError('x'.repeat(41))).toBe('fleet_hosts.probe.err_name_rule')
  })
  it('refuses control characters', () => {
    expect(probeNameError('a\u0007b')).toBe('fleet_hosts.probe.err_name_rule')
    expect(probeNameError('a\nb')).toBe('fleet_hosts.probe.err_name_rule')
  })
})

describe('aliasShapeOk (config.ValidateSelfAlias)', () => {
  it.each([['air26', true], ['a', true], ['a.b_c-d', true], ['A'.repeat(64), true], ['A'.repeat(65), false], ['', false], ['.', false], ['..', false], ['-a', false], ['a b', false], ['a/b', false]])('%s -> %s', (a, ok) => {
    expect(aliasShapeOk(a as string)).toBe(ok)
  })
})

describe('fleetRowError (fleetrow.go)', () => {
  const known = ['mlab', 'air26']
  it('accepts 1 to 4 unique aliases and a known or empty peer', () => {
    expect(fleetRowError(['air26'], '', known)).toBeNull()
    expect(fleetRowError(['a', 'b', 'c', 'd'], 'mlab', known)).toBeNull()
  })
  it('refuses 0 and 5 aliases', () => {
    expect(fleetRowError([], '', known)?.key).toBe('fleet_hosts.row.err_order_count')
    expect(fleetRowError(['a', 'b', 'c', 'd', 'e'], '', known)?.key).toBe('fleet_hosts.row.err_order_count')
  })
  it('refuses a bad shape and names it', () => {
    expect(fleetRowError(['air26', 'bad alias'], '', known)).toEqual({ key: 'fleet_hosts.row.err_order_shape', alias: 'bad alias' })
  })
  it('refuses a duplicate compared in lower case', () => {
    expect(fleetRowError(['air26', 'AIR26'], '', known)).toEqual({ key: 'fleet_hosts.row.err_order_dup', alias: 'AIR26' })
  })
  it('refuses an unknown peer, and skips the check when the alias list could not be read', () => {
    expect(fleetRowError(['air26'], 'ghost', known)).toEqual({ key: 'fleet_hosts.row.err_peer_unknown', alias: 'ghost' })
    expect(fleetRowError(['air26'], 'ghost', null)).toBeNull()
  })
})

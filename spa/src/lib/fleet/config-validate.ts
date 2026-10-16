// spa/src/lib/fleet/config-validate.ts — the daemon's own rules for the fleet host-config rows, checked before a save so a
// 400 comes as a field message instead of a half-written pair. Mirrors internal/module/hostconfig/probehosts.go
// (probeNameMaxRunes, hasControl) and fleetrow.go (maxFleetOrder, config.ValidateSelfAlias's aliasPattern, the lower-case
// duplicate check, a known peer alias).

export const PROBE_NAME_MAX_RUNES = 40
export const FLEET_ORDER_MAX = 4
const ALIAS_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/
const CONTROL = /\p{Cc}/u

/**
 * The i18n key of the first thing wrong with a probe-host name, or null. The daemon trims the name before it counts
 * (probehosts.go), so this does too; what is saved is the trimmed name.
 */
export function probeNameError(name: string): string | null {
  const trimmed = name.trim()
  const n = Array.from(trimmed).length
  if (n < 1) return 'fleet_hosts.probe.err_name'
  if (n > PROBE_NAME_MAX_RUNES || CONTROL.test(trimmed)) return 'fleet_hosts.probe.err_name_rule'
  return null
}

export const aliasShapeOk = (alias: string): boolean => ALIAS_PATTERN.test(alias)

/** The i18n key (and the offending alias, for the message) of the first problem in a fleet row, or null. `known` null: the alias list could not be read, the daemon checks the peer alias. */
export function fleetRowError(order: string[], peer: string, known: string[] | null): { key: string; alias?: string } | null {
  if (order.length < 1 || order.length > FLEET_ORDER_MAX) return { key: 'fleet_hosts.row.err_order_count' }
  const seen = new Set<string>()
  for (const a of order) {
    if (!aliasShapeOk(a)) return { key: 'fleet_hosts.row.err_order_shape', alias: a }
    const k = a.toLowerCase()
    if (seen.has(k)) return { key: 'fleet_hosts.row.err_order_dup', alias: a }
    seen.add(k)
  }
  if (peer !== '' && known !== null && !known.includes(peer)) return { key: 'fleet_hosts.row.err_peer_unknown', alias: peer }
  return null
}

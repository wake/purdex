// spa/src/lib/pairing-mint.ts — mint a device token on every host and package one pair row per minted host on the relay
// (moved verbatim out of pairing.ts, #2238).
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { isValidDaemonId } from './daemon-id'
import { hostLookOf, type HostLook } from './host-look'
import { payloadRowsOf, isTransferHost, parseTransferRows } from './host-transfer-plan'
import type { TransferLook } from './host-transfer-api'
import { call, CREATE_STATUS, isPlainObject, revokeHosts } from './pairing-transport'
import {
  PAIRING_MIN_ENTRY_S, PAIRING_TOKEN_MARGIN_S, PAIRING_TTL_MS,
  type LeftOut, type LeftOutReason, type PackageResult, type PairingFailure, type PairingFailureReason, type PairingInput,
} from './pairing-types'

interface Plan {
  host: HostConfig
  name: string
  ip: string
  port: number
  daemonId: string
  look: TransferLook
}

type MintOutcome =
  | { kind: 'ok'; id: string; token: string }
  | { kind: 'rejected' } //  a definite refusal: nothing was minted
  | { kind: 'unknown' } //   timeout, network, or a 201 we could not read: a token may exist

/** The row skeleton of one host, or why it cannot be a row. Pure; the token is not known yet. */
function planOf(host: HostConfig, lookOf: (id: string) => HostLook): Plan | LeftOutReason {
  if (!useHostStore.getState().hosts[host.id]) return 'unknown_host'
  if (typeof host.token !== 'string' || host.token === '') return 'no_token'
  if (!isValidDaemonId(host.daemonId)) return 'no_daemon_id'
  if (!isTransferHost(host.ip)) return 'invalid_address'
  const shown = payloadRowsOf([host], lookOf)[0]
  // Same guards as a received row: only look fields that pass `sanitizeHostConfig` survive.
  const parsed = parseTransferRows([{ ip: shown.ip, port: shown.port, token: 'x', name: shown.name, look: shown.look }]).rows[0]
  if (!parsed) return 'invalid_address'
  return { host, name: parsed.name, ip: parsed.ip, port: parsed.port, daemonId: host.daemonId, look: parsed.look ?? {} }
}

/**
 * The one rule for which hosts a pairing can carry — everything knowable BEFORE minting (unknown host, no token, no valid
 * daemon id, an address a phone cannot take). `mintAndPackage` and the dialog's setup view both call this, so the setup list
 * and the ready list cannot drift; only what happens while minting (`mint_failed`) is added later. Duplicates are skipped.
 */
export function classifyHostsForPairing(
  hosts: readonly HostConfig[],
  lookOf: (id: string) => HostLook,
): { usable: HostConfig[]; leftOut: LeftOut[]; plans: Plan[] } {
  const plans: Plan[] = []
  const leftOut: LeftOut[] = []
  const seen = new Set<string>()
  for (const host of hosts) {
    if (seen.has(host.id)) continue
    seen.add(host.id)
    const p = planOf(host, lookOf)
    if (typeof p === 'string') leftOut.push({ hostId: host.id, reason: p })
    else plans.push(p)
  }
  return { usable: plans.map((p) => p.host), leftOut, plans }
}

function failed(reason: PairingFailureReason, revokeFailed: string[] = [], pairingId?: string): PairingFailure {
  return pairingId === undefined ? { kind: 'failed', reason, revokeFailed } : { kind: 'failed', reason, revokeFailed, pairingId }
}

/**
 * Spec §2 steps 2–3: mint on every usable host (in parallel; `use_within_s` computed at each call), abort and revoke
 * when the SOT host fails or too little time is left, create the entry on the relay.
 */
export async function mintAndPackage(input: PairingInput, opts: { isCancelled?: () => boolean } = {}): Promise<PackageResult> {
  const clock = input.clock ?? Date.now
  const { profile, relay } = input
  if (!useHostStore.getState().hosts[relay.id]) return failed('unknown_host')
  const deadline = clock() + PAIRING_TTL_MS
  const lookOf = input.lookOf ?? ((id: string) => hostLookOf(id)) // the look of a host is read through the resolver, never off its config

  const { plans, leftOut } = classifyHostsForPairing(input.hosts, lookOf)
  if (!plans.some((p) => p.host.id === profile.sotHostId)) return failed('sot_failed')

  const pairingId = crypto.randomUUID()
  const outcomes = await Promise.all(
    plans.map(async (plan): Promise<MintOutcome> => {
      const body: Record<string, unknown> = {
        pairing_id: pairingId,
        label: input.label,
        client: { kind: 'app', label: 'Purdex.app' },
        use_within_s: Math.ceil((deadline - clock()) / 1000) + PAIRING_TOKEN_MARGIN_S,
      }
      if (plan.host.id === profile.sotHostId) body.profile_id = profile.profileId
      const r = await call(plan.host.id, 'POST', '/api/devices', body)
      if (r.kind === 'failed') return { kind: 'unknown' }
      if (r.status === 201) {
        const j = r.json
        if (isPlainObject(j) && typeof j.id === 'string' && j.id !== '' && typeof j.token === 'string' && j.token !== '') {
          return { kind: 'ok', id: j.id, token: j.token }
        }
        return { kind: 'unknown' }
      }
      return { kind: 'rejected' }
    }),
  )

  // Every host a token may exist on (a definite refusal means none).
  const touched = plans.filter((_, i) => outcomes[i].kind !== 'rejected').map((p) => p.host.id)
  const abort = async (reason: PairingFailureReason): Promise<PairingFailure> => failed(reason, await revokeHosts(touched, pairingId), pairingId)

  const rows: Record<string, unknown>[] = []
  const mintedHostIds: string[] = []
  const strayed: string[] = []
  let sotOk = false
  plans.forEach((plan, i) => {
    const o = outcomes[i]
    if (o.kind === 'ok') {
      rows.push({
        v: 1,
        kind: 'pair',
        name: plan.name,
        ip: plan.ip,
        port: plan.port,
        daemonId: plan.daemonId,
        look: plan.look,
        token: o.token,
        deviceId: o.id,
        pairingId,
        profile: { hostDaemonId: '', profileId: profile.profileId, name: profile.profileName },
      })
      mintedHostIds.push(plan.host.id)
      if (plan.host.id === profile.sotHostId) sotOk = true
    } else {
      leftOut.push({ hostId: plan.host.id, reason: 'mint_failed' })
      if (o.kind === 'unknown') strayed.push(plan.host.id)
    }
  })
  if (!sotOk) return abort('sot_failed')
  const sotPlan = plans.find((p) => p.host.id === profile.sotHostId)!
  for (const row of rows) (row.profile as { hostDaemonId: string }).hostDaemonId = sotPlan.daemonId

  if (opts.isCancelled?.()) return abort('cancelled')
  const expiresInS = Math.floor((deadline - clock()) / 1000)
  if (expiresInS < PAIRING_MIN_ENTRY_S) return abort('too_late')

  const created = await call(relay.id, 'POST', '/api/host-transfer/pairings', { rows, expires_in_s: expiresInS })
  if (created.kind === 'failed') return abort(created.reason)
  if (created.status !== 200) return abort(CREATE_STATUS[created.status] ?? 'malformed')
  const j = created.json
  if (!isPlainObject(j) || typeof j.code !== 'string' || j.code === '' || typeof j.expiresAt !== 'number' || !Number.isFinite(j.expiresAt)) {
    return abort('malformed')
  }

  // A left-out host whose mint outcome was unknown may hold a token nobody will use: clean it up now.
  const revokeFailed = strayed.length > 0 ? await revokeHosts(strayed, pairingId) : []
  // The colon stays literal, exactly as the spec's `relay=<ip:port>`; everything else a URL could trip on is encoded.
  const relayAt = encodeURIComponent(`${relay.ip}:${relay.port}`).replace(/%3A/gi, ':')
  return {
    kind: 'ok',
    code: j.code,
    expiresAt: j.expiresAt,
    qrUrl: `purdex://pair?v=1&relay=${relayAt}&code=${encodeURIComponent(j.code)}`,
    leftOut,
    mintedHostIds,
    pairingId,
    deadline,
    revokeFailed,
  }
}

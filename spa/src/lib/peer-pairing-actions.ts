// spa/src/lib/peer-pairing-actions.ts — the three write flows of the Peers page
// (Phase D spec §7.1 pair, §7.2 unpair, §7.3 rotate), as async functions over
// an injected API so they are testable without React. Each flow reports the
// step it is on through a callback and returns an outcome the component
// renders; neither ever carries a token value.
//
// A rotation from the page (`rotateDirection`) is mint → push and nothing
// more: the page refreshes, and the post-dial re-read of the row is what
// offers Commit / Cancel / neither for the operator to click (spec §7.3, D-7).
// The one flow that commits is the §7.1 repair path of `pairHosts`, because the
// spec ends that path with "then commit on Y" — and it commits the same way a
// click would: after its own evidence dial and a fresh read of Y's row, only
// when that row says the peer is on the new token. Never from its memory of
// the push (§6.4 residual, §8.4 mutation).
//
// Spec D-8: a token value (`inbound_token` from add or rotate) is a local
// `const` of the flow that consumes it. It is never put in an outcome, a step,
// an error message or a log.
import { HostApiError, type PeerHostAdded, type PeerHostRow, type PeerHostVerify } from './host-api'
import { rotationOffer, type RotationOffer } from './peer-pairing'

export interface ActionApi {
  add: (hostId: string, body: { alias?: string; url: string; token?: string }) => Promise<PeerHostAdded>
  update: (hostId: string, alias: string, patch: { alias?: string; token?: string }) => Promise<PeerHostRow>
  delete: (hostId: string, alias: string) => Promise<void>
  rotate: (hostId: string, alias: string) => Promise<{ alias: string; inbound_token: string }>
  list: (hostId: string) => Promise<PeerHostRow[]>
  verify: (hostId: string, alias: string) => Promise<PeerHostVerify>
  /** Used by the repair path only, after its own dial + fresh read. Never with `force`. */
  commit: (hostId: string, alias: string) => Promise<PeerHostRow>
}

/** Every step a flow reports. Never carries a token. */
export type FlowStep =
  | 'mint' | 'push'                                                          // rotateDirection
  | 'create-on-y' | 'rotate-on-y' | 'create-on-x' | 'push-to-y'              // pairHosts
  | 'check-undo' | 'undo-on-y'                                               //   step-2 failure, non-repair
  | 'verify' | 'read' | 'commit'                                             //   repair finish (§7.1 "then commit on Y")
  | 'delete-x' | 'delete-y'                                                  // unpairHosts
export type Report = (step: FlowStep) => void
export interface Ref { hostId: string; alias: string }

export type RotateOutcome =
  | { kind: 'pushed' }
  | { kind: 'push-failed'; error: string }     // holder accepts both tokens; the refresh decides
  | { kind: 'rotate-failed'; error: string }   // nothing changed

export type PairOutcome =
  | { kind: 'paired'; aliasOnX: string; aliasOnY: string }                       // repair path: Y's rotation committed too
  | { kind: 'alias-conflict'; side: 'x' | 'y'; error: string }                  // nothing left behind on the non-repair path
  | { kind: 'step-failed'; step: 'create-on-y' | 'rotate-on-y' | 'create-on-x'; error: string; undoError: string }
  | { kind: 'return-failed'; aliasOnX: string; aliasOnY: string; error: string }   // both entries exist → one-way
  /** Repair path, both entries stored, but Y's fresh row did not say `current` (or the commit was refused): Y's rotation stays pending; the page's row offers Commit/Cancel by the ordinary rule. */
  | { kind: 'repair-pending'; aliasOnX: string; aliasOnY: string; offer: RotationOffer; commitError: string }

export type UnpairOutcome = { xError: string; yError: string }   // '' = deleted or was already gone (404)

// Outcome errors carry the daemon's own `{error}` text when there is one.
const msg = (e: unknown) => (e instanceof HostApiError ? e.detail : e instanceof Error ? e.message : String(e))
const status = (e: unknown) => (e instanceof HostApiError ? e.status : 0)

type Settled<T> = { ok: true; value: T } | { ok: false; error: unknown }
const settle = <T,>(p: Promise<T>): Promise<Settled<T>> =>
  p.then((value) => ({ ok: true as const, value }), (error: unknown) => ({ ok: false as const, error }))

/**
 * Spec §6.4 steps 1–2 with the roles named: `holder` is the entry whose inbound
 * token is rotated; `push(token)` stores the new token on the presenter (a PUT on
 * its existing entry, or — "Create return entry" — a POST that creates it). What
 * the peer then presents, and therefore which of Commit/Cancel is safe, is NOT
 * this function's business: the caller refreshes and reads the row (§7.3).
 */
export async function rotateDirection(
  holder: Ref,
  push: (token: string) => Promise<void>,
  api: ActionApi,
  report: Report,
): Promise<RotateOutcome> {
  report('mint')
  const minted = await settle(api.rotate(holder.hostId, holder.alias))
  if (!minted.ok) return { kind: 'rotate-failed', error: msg(minted.error) }
  const tok = minted.value.inbound_token
  report('push')
  const pushed = await settle(push(tok))
  if (!pushed.ok) return { kind: 'push-failed', error: msg(pushed.error) }
  return { kind: 'pushed' }
}

/**
 * Spec §7.1. Non-repair: create X's entry on Y (tY) → create Y's entry on X
 * with tY (tX) → push tX to Y's entry. Repair (Y already holds an entry for X):
 * step 1 is a `rotate` on that entry instead, which gives a readable tY; there
 * is no undo on that path (it would need `force`), so a step-2 failure leaves
 * Y's rotation pending and the page's candidate line offers Commit/Cancel by
 * the row rule. Step 2 failing on the non-repair path deletes the step-1
 * entry first (404 = already gone), so a failed pair leaves nothing behind;
 * `alias-conflict` promises exactly that, so a 409 whose undo failed is
 * reported as `step-failed` with the undo error instead.
 *
 * The undo is not blind (codex A-1): Y's DELETE is by alias only, so the entry
 * is re-listed and deleted only if the alias still carries the url and
 * host_id step 1 created it with; a different entry under that alias (another
 * admin deleted and re-created it in the window) is left alone and reported.
 * A list-then-delete still has a window; closing it needs a conditional
 * delete on the daemon (follow-up issue).
 *
 * The repair path ends as spec §7.1 says — "then commit on Y" — by the §7.3
 * protocol: X dials Y once more (the evidence), Y's row is re-read, and the
 * commit is sent only when that fresh row says `current`. Anything else leaves
 * the rotation pending and is reported as `repair-pending`.
 */
export async function pairHosts(
  x: { hostId: string; url: string; selfAlias: string },
  y: { hostId: string; url: string; returnEntry: PeerHostRow | null },
  aliases: { onY?: string; onX?: string },
  api: ActionApi,
  report: Report,
): Promise<PairOutcome> {
  // Step 1: obtain tY — the token X will present to Y.
  const repair = y.returnEntry !== null
  const step1 = repair ? 'rotate-on-y' : 'create-on-y'
  report(step1)
  const minted = await settle(y.returnEntry
    ? api.rotate(y.hostId, y.returnEntry.alias)
    : api.add(y.hostId, { alias: aliases.onY ?? x.selfAlias, url: x.url }))
  if (!minted.ok) {
    if (!repair && status(minted.error) === 409) return { kind: 'alias-conflict', side: 'y', error: msg(minted.error) }
    return { kind: 'step-failed', step: step1, error: msg(minted.error), undoError: '' }
  }
  const aliasOnY = minted.value.alias
  const tY = minted.value.inbound_token

  // Step 2: X verifies X→Y with tY and mints tX — the token Y will present to X.
  report('create-on-x')
  const created = await settle(api.add(x.hostId, { url: y.url, token: tY, ...(aliases.onX ? { alias: aliases.onX } : {}) }))
  if (!created.ok) {
    let undoError = ''
    if (!repair) undoError = await undoOnY(y.hostId, minted.value as PeerHostAdded, api, report)
    if (!repair && undoError === '' && status(created.error) === 409) {
      return { kind: 'alias-conflict', side: 'x', error: msg(created.error) }
    }
    return { kind: 'step-failed', step: 'create-on-x', error: msg(created.error), undoError }
  }
  const aliasOnX = created.value.alias
  const tX = created.value.inbound_token

  // Step 3: Y verifies Y→X with tX and stores it. A failure leaves both entries → one-way.
  report('push-to-y')
  const pushed = await settle(api.update(y.hostId, aliasOnY, { token: tX }))
  if (!pushed.ok) return { kind: 'return-failed', aliasOnX, aliasOnY, error: msg(pushed.error) }
  if (!repair) return { kind: 'paired', aliasOnX, aliasOnY }

  // Repair finish (§7.1 "then commit on Y"), by the §7.3 protocol: X dials Y
  // with what it STORED (the result itself is not evidence — the dial is), then
  // Y's row is read fresh, and only `current` commits. Never from the memory
  // that step 2 succeeded.
  report('verify')
  await settle(api.verify(x.hostId, aliasOnX))
  report('read')
  const rows = await settle(api.list(y.hostId))
  if (!rows.ok) return { kind: 'repair-pending', aliasOnX, aliasOnY, offer: 'none', commitError: msg(rows.error) }
  const fresh = rows.value.find((r) => r.alias === aliasOnY)
  const offer = fresh ? rotationOffer(fresh) ?? 'none' : 'none'
  if (offer !== 'commit') return { kind: 'repair-pending', aliasOnX, aliasOnY, offer, commitError: '' }
  report('commit')
  const committed = await settle(api.commit(y.hostId, aliasOnY))
  if (!committed.ok) return { kind: 'repair-pending', aliasOnX, aliasOnY, offer, commitError: msg(committed.error) }
  return { kind: 'paired', aliasOnX, aliasOnY }
}

/**
 * Deletes the entry step 1 created on Y — only if the alias still IS that
 * entry (same url and host_id as the 201 reported). Returns the undo error
 * text, '' when the entry is gone (deleted here, or already absent).
 */
async function undoOnY(hostId: string, made: PeerHostAdded, api: ActionApi, report: Report): Promise<string> {
  report('check-undo')
  const rows = await settle(api.list(hostId))
  if (!rows.ok) return `could not re-read the peer before undoing; entry "${made.alias}" left in place: ${msg(rows.error)}`
  const now = rows.value.find((r) => r.alias === made.alias)
  if (!now) return ''
  if (now.url !== made.url || now.host_id !== made.host_id) {
    return `entry "${made.alias}" on the peer changed since it was created; left in place`
  }
  report('undo-on-y')
  const undo = await settle(api.delete(hostId, made.alias))
  return undo.ok || status(undo.error) === 404 ? '' : msg(undo.error)
}

/**
 * Spec §7.2: both deletes are attempted whatever happens to the other — they
 * are started together, so a side whose request never settles cannot stop the
 * other from being sent (codex A-2). 404 counts as done.
 */
export async function unpairHosts(x: Ref, y: Ref | null, api: ActionApi, report: Report): Promise<UnpairOutcome> {
  const del = async (r: Ref): Promise<string> => {
    const s = await settle(api.delete(r.hostId, r.alias))
    return s.ok || status(s.error) === 404 ? '' : msg(s.error)
  }
  report('delete-x')
  const px = del(x)
  let py: Promise<string> = Promise.resolve('')
  if (y) {
    report('delete-y')
    py = del(y)
  }
  const [xError, yError] = await Promise.all([px, py])
  return { xError, yError }
}

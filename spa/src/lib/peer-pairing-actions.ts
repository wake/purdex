// spa/src/lib/peer-pairing-actions.ts — the three write flows of the Peers page
// (Phase D spec §7.1 pair, §7.2 unpair, §7.3 rotate), as async functions over
// an injected API so they are testable without React. Each flow reports the
// step it is on through a callback and returns an outcome the component
// renders; neither ever carries a token value.
//
// No flow commits a rotation. A rotation is mint → push, then the page
// refreshes, and the post-dial re-read of the row is what offers Commit /
// Cancel / neither (spec §6.4 "Commit offered and accepted", §7.3, D-7). The
// `ActionApi` type has no commit/cancel member on purpose: the button handlers
// call `commitRotation`/`cancelRotation` directly, a flow cannot.
//
// Spec D-8: a token value (`inbound_token` from add or rotate) is a local
// `const` of the flow that consumes it. It is never put in an outcome, a step,
// an error message or a log.
import { HostApiError, type PeerHostAdded, type PeerHostRow } from './host-api'

export interface ActionApi {
  add: (hostId: string, body: { alias?: string; url: string; token?: string }) => Promise<PeerHostAdded>
  update: (hostId: string, alias: string, patch: { alias?: string; token?: string }) => Promise<PeerHostRow>
  delete: (hostId: string, alias: string) => Promise<void>
  rotate: (hostId: string, alias: string) => Promise<{ alias: string; inbound_token: string }>
}

/** Every step a flow reports. Never carries a token. */
export type FlowStep =
  | 'mint' | 'push'                                                          // rotateDirection
  | 'create-on-y' | 'rotate-on-y' | 'create-on-x' | 'push-to-y' | 'undo-on-y' // pairHosts
  | 'delete-x' | 'delete-y'                                                  // unpairHosts
export type Report = (step: FlowStep) => void
export interface Ref { hostId: string; alias: string }

export type RotateOutcome =
  | { kind: 'pushed' }
  | { kind: 'push-failed'; error: string }     // holder accepts both tokens; the refresh decides
  | { kind: 'rotate-failed'; error: string }   // nothing changed

export type PairOutcome =
  | { kind: 'paired'; aliasOnX: string; aliasOnY: string }                       // on the repair path Y's rotation is still pending; the refresh offers Commit
  | { kind: 'alias-conflict'; side: 'x' | 'y'; error: string }                  // nothing left behind on the non-repair path
  | { kind: 'step-failed'; step: 'create-on-y' | 'rotate-on-y' | 'create-on-x'; error: string; undoError: string }
  | { kind: 'return-failed'; aliasOnX: string; aliasOnY: string; error: string }   // both entries exist → one-way

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
 * the row rule. Step 2 failing on the non-repair path always deletes the
 * step-1 entry first (404 = already gone), so a failed pair leaves nothing
 * behind; `alias-conflict` promises exactly that, so a 409 whose undo failed
 * is reported as `step-failed` with the undo error instead.
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
    if (!repair) {
      report('undo-on-y')
      const undo = await settle(api.delete(y.hostId, aliasOnY))
      if (!undo.ok && status(undo.error) !== 404) undoError = msg(undo.error)
    }
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
  return { kind: 'paired', aliasOnX, aliasOnY }
}

/** Spec §7.2: both deletes are attempted whatever the first one did; 404 counts as done. */
export async function unpairHosts(x: Ref, y: Ref | null, api: ActionApi, report: Report): Promise<UnpairOutcome> {
  const del = async (r: Ref): Promise<string> => {
    const s = await settle(api.delete(r.hostId, r.alias))
    return s.ok || status(s.error) === 404 ? '' : msg(s.error)
  }
  report('delete-x')
  const xError = await del(x)
  let yError = ''
  if (y) {
    report('delete-y')
    yError = await del(y)
  }
  return { xError, yError }
}

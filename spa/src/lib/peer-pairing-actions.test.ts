import { describe, it, expect, vi } from 'vitest'
import {
  pairHosts, rotateDirection, unpairHosts,
  type ActionApi, type FlowStep, type PairOutcome, type Report, type RotateOutcome, type UnpairOutcome,
} from './peer-pairing-actions'
import { HostApiError, type PeerHostAdded, type PeerHostRow } from './host-api'

// Tokens are `pdxp_` + 32 hex (internal/config/config.go:94) so a leak is greppable.
const TOK_A = 'pdxp_' + 'a1'.repeat(16)   // minted by A (add / rotate on A)
const TOK_X = 'pdxp_' + 'b2'.repeat(16)   // minted by X (add on X)
const TOK_R = 'pdxp_' + 'c3'.repeat(16)   // minted by a rotate on X (rotateDirection)

const X_URL = 'http://100.64.0.2:7860'
const A_URL = 'http://100.64.0.4:7860'

const row = (p: Partial<PeerHostRow>): PeerHostRow => ({
  alias: 'mini-lab', url: X_URL, host_id: 'mini-lab:278cbm',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true,
  rotation_pending: false, last_inbound_auth: '', ...p,
})
const added = (alias: string, url: string, host_id: string, inbound_token: string): PeerHostAdded =>
  ({ alias, url, host_id, inbound_token, verified: true })

const err = (status: number, detail: string) => new HostApiError(status, '', detail)

interface FakeSpec {
  add?: Record<string, PeerHostAdded | Error>                                  // by hostId
  update?: Record<string, PeerHostRow | Error>                                 // by `${hostId}/${alias}`
  delete?: Record<string, true | Error>                                        // by `${hostId}/${alias}`
  rotate?: Record<string, { alias: string; inbound_token: string } | Error>    // by `${hostId}/${alias}`
}

/**
 * An `ActionApi` of vi.fns plus a `calls` log. The log records WHETHER a token
 * was passed and whether it equals one the fake minted (`token=same:<minter>`),
 * `token=yes` for an unknown value, `token=no` for no key — never the value.
 * The fake deliberately has no `commit`/`cancel`/`verify`/`list` member: the
 * `ActionApi` type is what keeps a flow from committing (codex F1, spec §7.3).
 */
function fake(spec: FakeSpec) {
  const calls: string[] = []
  const steps: FlowStep[] = []
  const report: Report = (s) => { steps.push(s) }
  const minted = new Map<string, string>()   // token value → hostId that minted it
  const pick = <T,>(m: Record<string, T | Error> | undefined, k: string): Promise<T> => {
    const v = m?.[k]
    if (v === undefined) return Promise.reject(new Error(`unexpected ${k}`))
    return v instanceof Error ? Promise.reject(v) : Promise.resolve(v)
  }
  const tag = (o: object, key: 'token' | 'alias'): string => {
    if (!(key in o)) return key === 'token' ? 'no' : 'none'
    const v = (o as Record<string, unknown>)[key]
    if (key === 'alias') return String(v)
    return typeof v === 'string' && minted.has(v) ? `same:${minted.get(v)}` : 'yes'
  }
  const api: ActionApi = {
    add: vi.fn((h: string, body: { alias?: string; url: string; token?: string }) => {
      calls.push(`add:${h}:alias=${tag(body, 'alias')}:url=${body.url}:token=${tag(body, 'token')}`)
      return pick(spec.add, h).then((r) => { minted.set(r.inbound_token, h); return r })
    }),
    update: vi.fn((h: string, alias: string, patch: { alias?: string; token?: string }) => {
      calls.push(`update:${h}:${alias}:token=${tag(patch, 'token')}`)
      return pick(spec.update, `${h}/${alias}`)
    }),
    delete: vi.fn((h: string, alias: string) => {
      calls.push(`delete:${h}:${alias}`)
      return pick(spec.delete, `${h}/${alias}`).then(() => undefined)
    }),
    rotate: vi.fn((h: string, alias: string) => {
      calls.push(`rotate:${h}:${alias}`)
      return pick(spec.rotate, `${h}/${alias}`).then((r) => { minted.set(r.inbound_token, h); return r })
    }),
  }
  return { api, calls, steps, report }
}

/** Spec D-8: no token value in any outcome, step or call log — asserted after EVERY test. */
const noLeak = (outcome: RotateOutcome | PairOutcome | UnpairOutcome, steps: FlowStep[], calls: string[]) =>
  expect(JSON.stringify({ outcome, steps, calls })).not.toMatch(/pdxp_/)

/* ─── rotateDirection ─── */

describe('rotateDirection — mint → push, the refresh decides (spec §6.4 steps 1–2, §7.3)', () => {
  const holder = { hostId: 'X', alias: 'air' }

  it('happy: rotate on the holder, then push with the minted token; pushed; steps mint,push', async () => {
    const f = fake({
      rotate: { 'X/air': { alias: 'air', inbound_token: TOK_R } },
      update: { 'A/mini-lab': row({ has_token: true }) },
    })
    const push = (token: string) => f.api.update('A', 'mini-lab', { token }).then(() => undefined)
    const outcome = await rotateDirection(holder, push, f.api, f.report)
    expect(outcome).toEqual({ kind: 'pushed' })
    expect(f.steps).toEqual(['mint', 'push'])
    expect(f.calls).toEqual(['rotate:X:air', 'update:A:mini-lab:token=same:X'])
    // The type forbids a flow from committing: the fake has no such member and the flow compiles.
    expect('commit' in f.api).toBe(false)
    expect('cancel' in f.api).toBe(false)
    expect('verify' in f.api).toBe(false)
    expect('list' in f.api).toBe(false)
    noLeak(outcome, f.steps, f.calls)
  })

  it('push throws (409 entry changed concurrently) → push-failed with that detail; rotate called once; nothing else', async () => {
    const f = fake({
      rotate: { 'X/air': { alias: 'air', inbound_token: TOK_R } },
      update: { 'A/mini-lab': err(409, 'entry changed concurrently') },
    })
    const push = (token: string) => f.api.update('A', 'mini-lab', { token }).then(() => undefined)
    const outcome = await rotateDirection(holder, push, f.api, f.report)
    expect(outcome).toEqual({ kind: 'push-failed', error: 'entry changed concurrently' })
    expect(f.api.rotate).toHaveBeenCalledTimes(1)
    expect(f.calls).toEqual(['rotate:X:air', 'update:A:mini-lab:token=same:X'])
    expect(f.steps).toEqual(['mint', 'push'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('rotate 409 rotation already pending → rotate-failed; push never called', async () => {
    const f = fake({ rotate: { 'X/air': err(409, 'rotation already pending') } })
    const push = vi.fn(() => Promise.resolve())
    const outcome = await rotateDirection(holder, push, f.api, f.report)
    expect(outcome).toEqual({ kind: 'rotate-failed', error: 'rotation already pending' })
    expect(push).not.toHaveBeenCalled()
    expect(f.calls).toEqual(['rotate:X:air'])
    expect(f.steps).toEqual(['mint'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('a non-HostApiError thrown by push is reported by its message', async () => {
    const f = fake({ rotate: { 'X/air': { alias: 'air', inbound_token: TOK_R } } })
    const outcome = await rotateDirection(holder, () => Promise.reject(new TypeError('Failed to fetch')), f.api, f.report)
    expect(outcome).toEqual({ kind: 'push-failed', error: 'Failed to fetch' })
    noLeak(outcome, f.steps, f.calls)
  })
})

/* ─── pairHosts ─── */

const X = { hostId: 'X', url: X_URL, selfAlias: 'mini-lab' }
const A = { hostId: 'A', url: A_URL, returnEntry: null }
const A_REPAIR = { hostId: 'A', url: A_URL, returnEntry: row({ alias: 'mini-lab' }) }

describe('pairHosts — non-repair path (spec §7.1 steps 1–3)', () => {
  const happy = () => fake({
    add: {
      A: added('mini-lab', X_URL, 'mini-lab:278cbm', TOK_A),
      X: added('air26', A_URL, 'wakes-air-2026:oa6drb', TOK_X),
    },
    update: { 'A/mini-lab': row({}) },
  })

  it('happy: add on Y (self alias, no token) → add on X (Y url, Y token, no alias) → PUT Y with X token; paired', async () => {
    const f = happy()
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'paired', aliasOnX: 'air26', aliasOnY: 'mini-lab' })
    expect(f.calls).toEqual([
      `add:A:alias=mini-lab:url=${X_URL}:token=no`,
      `add:X:alias=none:url=${A_URL}:token=same:A`,
      'update:A:mini-lab:token=same:X',
    ])
    expect(f.steps).toEqual(['create-on-y', 'create-on-x', 'push-to-y'])
    expect(f.api.delete).not.toHaveBeenCalled()
    expect(f.api.rotate).not.toHaveBeenCalled()
    noLeak(outcome, f.steps, f.calls)
  })

  it('aliases.onX → X add carries alias; aliases.onY → Y add uses it instead of selfAlias', async () => {
    const f = fake({
      add: {
        A: added('mlab-2', X_URL, 'mini-lab:278cbm', TOK_A),
        X: added('air-2', A_URL, 'wakes-air-2026:oa6drb', TOK_X),
      },
      update: { 'A/mlab-2': row({ alias: 'mlab-2' }) },
    })
    const outcome = await pairHosts(X, A, { onY: 'mlab-2', onX: 'air-2' }, f.api, f.report)
    expect(outcome).toEqual({ kind: 'paired', aliasOnX: 'air-2', aliasOnY: 'mlab-2' })
    expect(f.calls).toEqual([
      `add:A:alias=mlab-2:url=${X_URL}:token=no`,
      `add:X:alias=air-2:url=${A_URL}:token=same:A`,
      'update:A:mlab-2:token=same:X',
    ])
    noLeak(outcome, f.steps, f.calls)
  })

  it('aliasOnY comes from the daemon response, not the request (Y may normalise it)', async () => {
    const f = fake({
      add: {
        A: added('mini-lab', X_URL, 'mini-lab:278cbm', TOK_A),
        X: added('air26', A_URL, 'wakes-air-2026:oa6drb', TOK_X),
      },
      update: { 'A/mini-lab': row({}) },
    })
    const outcome = await pairHosts({ ...X, selfAlias: 'Mini-Lab' }, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'paired', aliasOnX: 'air26', aliasOnY: 'mini-lab' })
    expect(f.calls[2]).toBe('update:A:mini-lab:token=same:X')
    noLeak(outcome, f.steps, f.calls)
  })

  it('step 1 409 → alias-conflict / y; nothing else called', async () => {
    const f = fake({ add: { A: err(409, 'alias "mini-lab" is already used by another host; pass alias') } })
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'alias-conflict', side: 'y', error: 'alias "mini-lab" is already used by another host; pass alias' })
    expect(f.calls).toEqual([`add:A:alias=mini-lab:url=${X_URL}:token=no`])
    expect(f.steps).toEqual(['create-on-y'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('step 1 non-409 (502 verify failed) → step-failed / create-on-y, undoError empty; nothing else called', async () => {
    const f = fake({ add: { A: err(502, 'dial tcp 100.64.0.2:7860: i/o timeout') } })
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'step-failed', step: 'create-on-y', error: 'dial tcp 100.64.0.2:7860: i/o timeout', undoError: '' })
    expect(f.calls).toHaveLength(1)
    noLeak(outcome, f.steps, f.calls)
  })

  it('step 2 502 → undo delete on Y, step-failed / create-on-x, undoError empty; no update on Y', async () => {
    const f = fake({
      add: { A: added('mini-lab', X_URL, 'mini-lab:278cbm', TOK_A), X: err(502, 'verify failed: 401 Unauthorized') },
      delete: { 'A/mini-lab': true },
    })
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'step-failed', step: 'create-on-x', error: 'verify failed: 401 Unauthorized', undoError: '' })
    expect(f.calls).toEqual([
      `add:A:alias=mini-lab:url=${X_URL}:token=no`,
      `add:X:alias=none:url=${A_URL}:token=same:A`,
      'delete:A:mini-lab',
    ])
    expect(f.steps).toEqual(['create-on-y', 'create-on-x', 'undo-on-y'])
    expect(f.api.update).not.toHaveBeenCalled()
    noLeak(outcome, f.steps, f.calls)
  })

  it('step 2 409 → undo delete on Y first, then alias-conflict / x', async () => {
    const f = fake({
      add: { A: added('mini-lab', X_URL, 'mini-lab:278cbm', TOK_A), X: err(409, 'alias "air26" is already used by another host; pass alias') },
      delete: { 'A/mini-lab': true },
    })
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'alias-conflict', side: 'x', error: 'alias "air26" is already used by another host; pass alias' })
    expect(f.calls.slice(2)).toEqual(['delete:A:mini-lab'])
    expect(f.steps).toEqual(['create-on-y', 'create-on-x', 'undo-on-y'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('undo delete 404 → undoError empty (already gone counts as undone)', async () => {
    const f = fake({
      add: { A: added('mini-lab', X_URL, 'mini-lab:278cbm', TOK_A), X: err(502, 'verify failed') },
      delete: { 'A/mini-lab': err(404, 'unknown alias') },
    })
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'step-failed', step: 'create-on-x', error: 'verify failed', undoError: '' })
    noLeak(outcome, f.steps, f.calls)
  })

  it('undo delete 500 → undoError set, kind still step-failed', async () => {
    const f = fake({
      add: { A: added('mini-lab', X_URL, 'mini-lab:278cbm', TOK_A), X: err(502, 'verify failed') },
      delete: { 'A/mini-lab': err(500, 'write config: disk full') },
    })
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'step-failed', step: 'create-on-x', error: 'verify failed', undoError: 'write config: disk full' })
    noLeak(outcome, f.steps, f.calls)
  })

  it('step 2 409 with a failed undo → step-failed carrying undoError (alias-conflict promises nothing was left behind)', async () => {
    const f = fake({
      add: { A: added('mini-lab', X_URL, 'mini-lab:278cbm', TOK_A), X: err(409, 'alias "air26" is already used by another host') },
      delete: { 'A/mini-lab': err(500, 'write config: disk full') },
    })
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({
      kind: 'step-failed', step: 'create-on-x',
      error: 'alias "air26" is already used by another host', undoError: 'write config: disk full',
    })
    noLeak(outcome, f.steps, f.calls)
  })

  it('step 3 502 → return-failed with both aliases; no delete anywhere', async () => {
    const f = fake({
      add: {
        A: added('mini-lab', X_URL, 'mini-lab:278cbm', TOK_A),
        X: added('air26', A_URL, 'wakes-air-2026:oa6drb', TOK_X),
      },
      update: { 'A/mini-lab': err(502, 'verify failed: dial tcp: connection refused') },
    })
    const outcome = await pairHosts(X, A, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'return-failed', aliasOnX: 'air26', aliasOnY: 'mini-lab', error: 'verify failed: dial tcp: connection refused' })
    expect(f.api.delete).not.toHaveBeenCalled()
    expect(f.steps).toEqual(['create-on-y', 'create-on-x', 'push-to-y'])
    noLeak(outcome, f.steps, f.calls)
  })
})

describe('pairHosts — repair path (Y already holds an entry for X, spec §7.1 last paragraph)', () => {
  it('rotate on Y\'s entry instead of add; add on X with that token; PUT Y; paired', async () => {
    const f = fake({
      rotate: { 'A/mini-lab': { alias: 'mini-lab', inbound_token: TOK_A } },
      add: { X: added('air26', A_URL, 'wakes-air-2026:oa6drb', TOK_X) },
      update: { 'A/mini-lab': row({ rotation_pending: true }) },
    })
    const outcome = await pairHosts(X, A_REPAIR, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'paired', aliasOnX: 'air26', aliasOnY: 'mini-lab' })
    expect(f.calls).toEqual([
      'rotate:A:mini-lab',
      `add:X:alias=none:url=${A_URL}:token=same:A`,
      'update:A:mini-lab:token=same:X',
    ])
    expect(f.steps).toEqual(['rotate-on-y', 'create-on-x', 'push-to-y'])
    expect(f.api.delete).not.toHaveBeenCalled()
    // No flow commits: Y's rotation stays pending; the refresh offers Commit by the row rule.
    expect('commit' in f.api).toBe(false)
    noLeak(outcome, f.steps, f.calls)
  })

  it('repair, rotate 409 rotation already pending → step-failed / rotate-on-y; nothing else', async () => {
    const f = fake({ rotate: { 'A/mini-lab': err(409, 'rotation already pending') } })
    const outcome = await pairHosts(X, A_REPAIR, {}, f.api, f.report)
    // A 409 here is not an alias conflict: the repair path never asks for an alias.
    expect(outcome).toEqual({ kind: 'step-failed', step: 'rotate-on-y', error: 'rotation already pending', undoError: '' })
    expect(f.calls).toEqual(['rotate:A:mini-lab'])
    expect(f.steps).toEqual(['rotate-on-y'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('repair, step 2 fails → step-failed / create-on-x, no delete (no undo without force), undoError empty', async () => {
    const f = fake({
      rotate: { 'A/mini-lab': { alias: 'mini-lab', inbound_token: TOK_A } },
      add: { X: err(502, 'verify failed: 401 Unauthorized') },
    })
    const outcome = await pairHosts(X, A_REPAIR, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'step-failed', step: 'create-on-x', error: 'verify failed: 401 Unauthorized', undoError: '' })
    expect(f.api.delete).not.toHaveBeenCalled()
    expect(f.steps).toEqual(['rotate-on-y', 'create-on-x'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('repair, step 2 409 → still step-failed / create-on-x (no undo, so no alias-conflict promise)', async () => {
    const f = fake({
      rotate: { 'A/mini-lab': { alias: 'mini-lab', inbound_token: TOK_A } },
      add: { X: err(409, 'alias "air26" is already used by another host') },
    })
    const outcome = await pairHosts(X, A_REPAIR, {}, f.api, f.report)
    expect(outcome).toEqual({ kind: 'step-failed', step: 'create-on-x', error: 'alias "air26" is already used by another host', undoError: '' })
    expect(f.api.delete).not.toHaveBeenCalled()
    noLeak(outcome, f.steps, f.calls)
  })
})

/* ─── unpairHosts ─── */

describe('unpairHosts — both deletes attempted regardless (spec §7.2)', () => {
  const x = { hostId: 'X', alias: 'air' }
  const y = { hostId: 'A', alias: 'mini-lab' }

  it('X 404 + Y 204 → both errors empty', async () => {
    const f = fake({ delete: { 'X/air': err(404, 'unknown alias'), 'A/mini-lab': true } })
    const outcome = await unpairHosts(x, y, f.api, f.report)
    expect(outcome).toEqual({ xError: '', yError: '' })
    expect(f.calls).toEqual(['delete:X:air', 'delete:A:mini-lab'])
    expect(f.steps).toEqual(['delete-x', 'delete-y'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('X 500 → xError set AND Y still deleted', async () => {
    const f = fake({ delete: { 'X/air': err(500, 'write config: disk full'), 'A/mini-lab': true } })
    const outcome = await unpairHosts(x, y, f.api, f.report)
    expect(outcome).toEqual({ xError: 'write config: disk full', yError: '' })
    expect(f.calls).toEqual(['delete:X:air', 'delete:A:mini-lab'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('Y 500 → yError set, xError empty', async () => {
    const f = fake({ delete: { 'X/air': true, 'A/mini-lab': err(500, 'write config: disk full') } })
    const outcome = await unpairHosts(x, y, f.api, f.report)
    expect(outcome).toEqual({ xError: '', yError: 'write config: disk full' })
    noLeak(outcome, f.steps, f.calls)
  })

  it('y === null → one call, yError empty', async () => {
    const f = fake({ delete: { 'X/air': true } })
    const outcome = await unpairHosts(x, null, f.api, f.report)
    expect(outcome).toEqual({ xError: '', yError: '' })
    expect(f.calls).toEqual(['delete:X:air'])
    expect(f.steps).toEqual(['delete-x'])
    noLeak(outcome, f.steps, f.calls)
  })

  it('a non-HostApiError (network) is reported by its message, not treated as 404', async () => {
    const f = fake({ delete: { 'X/air': new TypeError('Failed to fetch'), 'A/mini-lab': true } })
    const outcome = await unpairHosts(x, y, f.api, f.report)
    expect(outcome).toEqual({ xError: 'Failed to fetch', yError: '' })
    noLeak(outcome, f.steps, f.calls)
  })
})

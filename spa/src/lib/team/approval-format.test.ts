// spa/src/lib/team/approval-format.test.ts — the pdx address the dialog prints for the requesting session, including
// an origin an unexpected daemon left incomplete: the formatter must not throw on a missing `ref` (F2).
import { describe, it, expect, beforeEach } from 'vitest'
import { approvalKindLabel, approvalSessionLabel, closedToastText, formatOriginAddress, memberRelayNames } from './approval-format'
import type { Origin } from './types'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { makeCollection, makePersona } from '../names/__fixtures__/personas'

const origin = (over: Partial<Origin> = {}): Origin => ({
  session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: '',
  ...over,
})

describe('formatOriginAddress', () => {
  it('prints `<host>/<name> [<ref6>]` for a routable name and `<host>/_<ref6>` without one; the daemon\'s address wins', () => {
    expect(formatOriginAddress('mlab', origin())).toBe('mlab/purdex-7c [40iueq]')
    expect(formatOriginAddress('mlab', origin({ name: '' }))).toBe('mlab/_40iueq')
    expect(formatOriginAddress('mlab', origin({ ref: '40iueq' }))).toBe('mlab/purdex-7c [40iueq]')
    expect(formatOriginAddress('mlab', origin({ address: 'air26/purdex-7c' }))).toBe('air26/purdex-7c')
  })

  it('does not throw when `ref` is not a string: falls back to the name, else `?`', () => {
    const noRef = origin({ ref: undefined as unknown as string })
    expect(() => formatOriginAddress('mlab', noRef)).not.toThrow()
    expect(formatOriginAddress('mlab', noRef)).toBe('mlab/purdex-7c')
    expect(formatOriginAddress('mlab', origin({ ref: null as unknown as string, name: '' }))).toBe('mlab/?')
    expect(() => approvalSessionLabel(noRef)).not.toThrow()
  })
})

describe('adopt (U24)', () => {
  const t = (k: string, vars?: Record<string, unknown>) => (vars ? `${k} ${JSON.stringify(vars)}` : k)
  it('has its own kind label', () => {
    expect(approvalKindLabel(t, 'adopt')).toBe('approval.kind.adopt')
    expect(approvalKindLabel(t, 'self_relay')).toBe('approval.kind.self_relay')
    expect(approvalKindLabel(t, 'lead')).toBe('approval.kind.lead')
  })
  it('a cancelled adopt toasts its state, naming the kind', () => {
    const a = {
      id: 'r', kind: 'adopt', host_id: 'd', origin: origin(), payload: {}, state: 'cancelled', close_reason: 'adopt_target_is_lead',
      created_at: 1, deadline_at: 2, lease_until: 3,
    } as const
    expect(closedToastText(t, 'mlab', a)).toContain('approval.kind.adopt')
    expect(closedToastText(t, 'mlab', a)).toContain('approval.state.cancelled')
  })
})

describe('member_relay', () => {
  const t = (k: string, vars?: Record<string, unknown>) => (vars ? `${k} ${JSON.stringify(vars)}` : k)
  const base = { id: 'r', kind: 'member_relay', host_id: 'd', origin: origin(), state: 'open', created_at: 1, deadline_at: 2, lease_until: 3 } as const
  it('has its own kind label', () => {
    expect(approvalKindLabel(t, 'member_relay')).toBe('approval.kind.member_relay')
  })
  it('names the lead and the member from the payload, falling back to the origin and the member ref', () => {
    expect(memberRelayNames({ ...base, payload: { lead_title: ' L ', member_title: ' M ', member_ref: '_b', used_percentage: 41.6 } }))
      .toMatchObject({ lead: 'L', member: 'M', pct: 42 })
    expect(memberRelayNames({ ...base, payload: { lead_title: '', member_title: '', member_ref: '_b', used_percentage: 0 } }))
      .toMatchObject({ lead: approvalSessionLabel(origin()), member: '_b', pct: 0 })
  })
  it('an alias made only of control or direction characters falls back to the trusted name (it must not display blank)', () => {
    const n = memberRelayNames({ ...base, payload: { lead_title: '\u202e', member_title: '\u200b \u2066', member_ref: '_b', used_percentage: 5 } })
    expect(n.lead).toBe(approvalSessionLabel(origin()))
    expect(n.member).toBe('_b')
  })
  it('a session-written origin label of only controls falls through to the ref', () => {
    const n = memberRelayNames({ ...base, origin: origin({ title: '\u202e', name: '\u200b', ref: '_aaaaaa' }), payload: { lead_title: '', member_title: 'm', member_ref: '_b', used_percentage: 5 } })
    expect(n.lead).toBe('_aaaaaa')
  })
  it('direction characters inside an alias are removed from what is shown', () => {
    const n = memberRelayNames({ ...base, payload: { lead_title: 'a\u202eb', member_title: 'c\u2066d', member_ref: '_b', used_percentage: 5 } })
    expect(n.lead).toBe('ab')
    expect(n.member).toBe('cd')
  })
  it('a payload of the wrong shape reads as empty strings and 0%', () => {
    const n = memberRelayNames({ ...base, payload: { lead_title: 5, member_title: null, used_percentage: Number.NaN } as never })
    expect(n.member).toBe('')
    expect(n.pct).toBe(0)
  })
  it('a closed member_relay toasts its state, naming the kind', () => {
    expect(closedToastText(t, 'mlab', { ...base, payload: {}, state: 'timeout' })).toContain('approval.kind.member_relay')
  })
})

// Relay v2 App contract 2 (`team.person.v1`): a person's terminal command decides a card (`decided_by.kind = "person"`), and the
// daemon cancels the lead / adopt cards 無人值守 refuses (`close_reason: "unattended_refused"`). Neither reads as a failure.
describe('closedToastText: a person\'s command and the unattended refusal', () => {
  const t = (k: string, vars?: Record<string, unknown>) => (vars ? `${k} ${JSON.stringify(vars)}` : k)
  const base = { id: 'r', kind: 'lead', host_id: 'd', origin: origin(), payload: {}, created_at: 1, deadline_at: 2, lease_until: 3 } as const

  it('a card a person approved from the terminal says so, in its own words (not through the app/terminal wording)', () => {
    const text = closedToastText(t, 'mlab', { ...base, state: 'approved', decided_by: { kind: 'person', label: '使用者（終端機指令）' } })
    expect(text).toContain('approval.toast.decided_by_person')
    expect(text).toContain('"decision":"approval.decision.approved"')
    expect(text).not.toContain('approval.toast.decided_elsewhere')
  })

  it('a denied card keeps the same wording with its own decision', () => {
    const text = closedToastText(t, 'mlab', { ...base, state: 'denied', decided_by: { kind: 'person', label: '使用者（終端機指令）' } })
    expect(text).toContain('approval.toast.decided_by_person')
    expect(text).toContain('"decision":"approval.decision.denied"')
  })

  it('an app or terminal decider is worded as before', () => {
    expect(closedToastText(t, 'mlab', { ...base, state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' } })).toContain('approval.toast.decided_elsewhere')
    expect(closedToastText(t, 'mlab', { ...base, state: 'approved', decided_by: { kind: 'terminal', label: 'terminal' } })).toContain('approval.toast.decided_elsewhere')
  })

  it('a lead card cancelled with unattended_refused says unattended mode refused it', () => {
    const text = closedToastText(t, 'mlab', { ...base, state: 'cancelled', close_reason: 'unattended_refused' })
    expect(text).toContain('approval.toast.unattended_refused')
    expect(text).not.toContain('approval.toast.ended')
  })

  it('an adopt card cancelled with unattended_refused too; any other close_reason keeps the plain ended wording', () => {
    expect(closedToastText(t, 'mlab', { ...base, kind: 'adopt', state: 'cancelled', close_reason: 'unattended_refused' })).toContain('approval.toast.unattended_refused')
    expect(closedToastText(t, 'mlab', { ...base, kind: 'adopt', state: 'cancelled', close_reason: 'adopt_target_is_lead' })).toContain('approval.toast.ended')
  })

  it('unattended_refused on a card that is not cancelled is not that wording (a stray field never rewrites a decision)', () => {
    expect(closedToastText(t, 'mlab', { ...base, state: 'timeout', close_reason: 'unattended_refused' })).toContain('approval.toast.ended')
  })
})

// Relay v2 App contract 5 (`relay.half.v1`): a relay op may end `cancelled` / `half` — the handoff file is written and the session was
// not cleared. The card closes `cancelled` with `close_reason: "half"`; that is the result, not a failure.
describe('closedToastText: a relay that ended half', () => {
  const t = (k: string, vars?: Record<string, unknown>) => (vars ? `${k} ${JSON.stringify(vars)}` : k)
  const base = { id: 'r', kind: 'self_relay', host_id: 'd', origin: origin(), payload: {}, created_at: 1, deadline_at: 2, lease_until: 3 } as const

  it('cancelled + half says the handoff file is written, in its own words, and never the cancelled / ended wording', () => {
    for (const kind of ['self_relay', 'member_relay'] as const) {
      const text = closedToastText(t, 'mlab', { ...base, kind, state: 'cancelled', close_reason: 'half' })
      expect(text).toContain('relay.half.toast')
      expect(text).not.toContain('approval.toast.ended')
      expect(text).not.toContain('approval.state.cancelled')
    }
  })

  it('cancelled with any other reason, or none, keeps the plain ended wording', () => {
    expect(closedToastText(t, 'mlab', { ...base, state: 'cancelled', close_reason: 'something_else' })).toContain('approval.toast.ended')
    expect(closedToastText(t, 'mlab', { ...base, state: 'cancelled' })).toContain('approval.state.cancelled')
  })

  it('half on a card of a kind that is not a relay (lead, adopt, a hook) keeps the plain ended wording', () => {
    for (const kind of ['lead', 'adopt', 'hook_ask', 'hook_permission'] as const) {
      const text = closedToastText(t, 'mlab', { ...base, kind, state: 'cancelled', close_reason: 'half' })
      expect(text).toContain('approval.toast.ended')
      expect(text).not.toContain('relay.half.toast')
    }
  })

  it('half on a card that is not cancelled is not that wording (a stray field never rewrites a state)', () => {
    expect(closedToastText(t, 'mlab', { ...base, state: 'timeout', close_reason: 'half' })).toContain('approval.toast.ended')
    expect(closedToastText(t, 'mlab', { ...base, state: 'abandoned', close_reason: 'half' })).toContain('approval.state.abandoned')
  })
})

// A session that is a persona is called by its name, the title after it (name pool v2 spec §9.3); the dialog's address row (formatOriginAddress) is untouched.
describe('approvalSessionLabel of a persona', () => {
  beforeEach(() => usePersonaStore.setState({ byHost: {} }))
  const seed = () => {
    const c = makeCollection(['kenji'])
    c.items[0] = makePersona('kenji', { name: 'Kenji' })
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
  }

  it('is the name with the title after it, or the name alone', () => {
    seed()
    expect(approvalSessionLabel(origin({ name: 'purdex-1f-kenji', address: 'mlab/purdex-1f-kenji', title: 'deploy' }))).toBe('Kenji · deploy')
    expect(approvalSessionLabel(origin({ name: 'purdex-1f-kenji', address: 'mlab/purdex-1f-kenji' }))).toBe('Kenji')
  })

  // `name` is the registry's raw name, which the session sets itself: only the daemon's resolved `address` may name a persona
  // (mutation: resolve from `o.name` again → a session called `anything-kenji` reads as Kenji in the dialog).
  it('never resolves a persona from the registry name: an older daemon sends no address, and a forged name stays the name', () => {
    seed()
    expect(approvalSessionLabel(origin({ name: 'purdex-1f-kenji' }))).toBe('purdex-1f-kenji')
    expect(approvalSessionLabel(origin({ name: 'anything-kenji', title: 'deploy' }))).toBe('deploy')
  })

  it('a session that is no persona reads as it always did: the title, else the name, else the ref', () => {
    seed()
    expect(approvalSessionLabel(origin({ title: 'deploy' }))).toBe('deploy')
    expect(approvalSessionLabel(origin())).toBe('purdex-7c')
    expect(approvalSessionLabel(origin({ name: '' }))).toBe('_40iueq')
  })

  it('leaves the address row as it was', () => {
    seed()
    expect(formatOriginAddress('mlab', origin({ name: 'purdex-1f-kenji' }))).toBe('mlab/purdex-1f-kenji [40iueq]')
  })
})

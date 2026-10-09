// spa/src/lib/team/approval-format.ts — the strings the approval dialog and its toasts are built from
// (lead-team spec §6.3, §6.5). Pure; the i18n `t` is passed in so lib code and tests do not depend on the
// store's locale.
import { clipForDisplay, memberRelayPayloadOf, type Approval, type ApprovalKind, type MemberRelayPayload, type Origin } from './types'

export type T = (key: string, params?: Record<string, string | number>) => string

/** What to call the requesting session: its title (when the wire carries one), else its registry name, else its ref. */
export function approvalSessionLabel(o: Origin): string {
  const title = o.title?.trim() ?? ''
  if (title !== '') return title
  return o.name !== '' ? o.name : o.ref
}

/**
 * The pdx address, as `pdx peers` prints it: `<host>/<name> [<ref>]` with the ref's underscore dropped inside the
 * brackets, or `<host>/_<ref>` for a session without a routable name. The daemon's own `address` wins when present.
 * Defensive on `ref` (the WS boundary validates it, but this renders inside the dialog, where a throw is a blank
 * screen): a non-string ref falls back to `<host>/<name>`, or `<host>/?` with no name either.
 */
export function formatOriginAddress(host: string, o: Origin): string {
  if (o.address) return o.address
  const name = typeof o.name === 'string' ? o.name : ''
  if (typeof o.ref !== 'string') return `${host}/${name !== '' ? name : '?'}`
  const ref6 = o.ref.startsWith('_') ? o.ref.slice(1) : o.ref
  return name !== '' ? `${host}/${name} [${ref6}]` : `${host}/_${ref6}`
}

export function approvalKindLabel(t: T, kind: ApprovalKind): string {
  return t(kind === 'self_relay' ? 'approval.kind.self_relay'
    : kind === 'member_relay' ? 'approval.kind.member_relay'
      : kind === 'adopt' ? 'approval.kind.adopt' : 'approval.kind.lead')
}

/**
 * Who a member-relay card names: the lead's title (else the origin's own label), the member's title (else its ref), and the
 * context usage as a whole percent. The strings are written by the sessions: callers clip them for display.
 */
export function memberRelayNames(a: Approval): { lead: string; member: string; pct: number; payload: MemberRelayPayload } {
  const payload = memberRelayPayloadOf(a)
  // The aliases are written by the sessions: control and direction characters are taken out BEFORE asking whether one is
  // empty, or an alias made of nothing but them (an RLO) would win over the trusted fallback and then display as blank.
  const plain = (s: string): string => clipForDisplay(s, Number.MAX_SAFE_INTEGER).trim()
  const lead = plain(payload.lead_title) || plain(approvalSessionLabel(a.origin)) || plain(a.origin.ref)
  const member = plain(payload.member_title) || plain(payload.member_ref)
  return { lead, member, pct: Math.round(payload.used_percentage), payload }
}

/**
 * The toast for a request closed by someone else (spec §6.3 / §6.5): `<主機>：<session> 的 <kind> 已由 <client> 核准／拒絕`;
 * a timeout, cancel or abandonment names the state instead.
 */
export function closedToastText(t: T, host: string, a: Approval): string {
  const session = approvalSessionLabel(a.origin)
  const kind = approvalKindLabel(t, a.kind)
  if ((a.state === 'approved' || a.state === 'denied') && a.decided_by) {
    const decision = t(a.state === 'approved' ? 'approval.decision.approved' : 'approval.decision.denied')
    return t('approval.toast.decided_elsewhere', { host, session, kind, client: a.decided_by.label, decision })
  }
  return t('approval.toast.ended', { host, session, kind, state: t(`approval.state.${a.state}`) })
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

/** `m:ss` to the deadline, never below `0:00`; ceil so the last second shows `0:01`, not `0:00` early. */
export function formatCountdown(ms: number): string {
  const s = Number.isFinite(ms) && ms > 0 ? Math.ceil(ms / 1000) : 0
  return `${Math.floor(s / 60)}:${pad2(s % 60)}`
}

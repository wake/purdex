// spa/src/components/team/member-lines.ts — the 2nd and 3rd line of a member row in the full team panel (JV-2, journal-v5 spec
// §3.1 U3): the member's current task and state; its last report to the leader. Both come from the LEADER's journal host
// (`GET /api/journal/members`, gated by `journal.members.v1`), so this reads the lead seat's host. Without the capability the hook
// answers null and a row stays as it was. A part the daemon sent as null is left out.
import { useEffect, useMemo, useRef } from 'react'
import { taskKeyOfTask } from '../../lib/journal/tasks'
import type { JournalMember } from '../../lib/journal/types'
import { membersKey, staleKey, useJournalMembersStore } from '../../stores/useJournalMembersStore'

type T = (key: string, params?: Record<string, string | number>) => string

export const MEMBER_REFRESH_DEBOUNCE_MS = 300

/**
 * The leader's member lines keyed by member session id, or null where the leader's host does not declare `journal.members.v1`.
 * Reads once when the panel shows; again when the team's member set changes (`rosterKey`: a join, a leave or a relay to a new session
 * makes no journal.changed), and again (debounced) when THIS leader's unit is marked stale by a journal.changed.
 */
export function useMemberFeed(leadHostId: string, leadSessionId: string, rosterKey: string, unitKey: string): ReadonlyMap<string, JournalMember> | null {
  const supported = useJournalMembersStore((s) => s.support[leadHostId] === true)
  const list = useJournalMembersStore((s) => s.byLead[membersKey(leadHostId, leadSessionId)])
  // Moves only when a journal.changed named this leader's unit (on this host): another host or unit never changes it. It counts the
  // events itself, so a team whose unit the journal loader never loaded (not the active tab's) is still seen.
  const staleToken = useJournalMembersStore((s) => s.stale[staleKey(leadHostId, unitKey)] ?? 0)
  const last = useRef<{ key: string; token: number } | null>(null)
  useEffect(() => {
    if (!supported) return
    const key = `${leadHostId}\u0000${leadSessionId}\u0000${rosterKey}`
    // Only a moved stale token waits out the debounce; showing the panel or a changed member set reads at once.
    const debounce = last.current !== null && last.current.key === key && last.current.token !== staleToken
    last.current = { key, token: staleToken }
    const timer = setTimeout(() => { void useJournalMembersStore.getState().load(leadHostId, leadSessionId) }, debounce ? MEMBER_REFRESH_DEBOUNCE_MS : 0)
    return () => clearTimeout(timer)
  }, [supported, leadHostId, leadSessionId, rosterKey, staleToken])
  return useMemo(() => (supported ? new Map((list ?? []).map((m) => [m.sessionId, m])) : null), [supported, list])
}

/** How long ago, as the row says it: 剛剛 / N 分前 / N 小時前 / N 天前. A time in the future (clock skew) reads as 剛剛. */
export function ageText(t: T, at: number, now: number): string {
  const min = Math.floor(Math.max(0, now - at) / 60_000)
  if (min < 1) return t('team.member.age.now')
  if (min < 60) return t('team.member.age.min', { n: min })
  const hour = Math.floor(min / 60)
  if (hour < 24) return t('team.member.age.hour', { n: hour })
  return t('team.member.age.day', { n: Math.floor(hour / 24) })
}

const REPORT_KINDS: ReadonlySet<string> = new Set(['ack', 'progress', 'question', 'ready', 'merged', 'blocked', 'done'])

/** Line 2: 「#12 title・進行中」. */
export function memberTaskLine(t: T, task: NonNullable<JournalMember['task']>): string {
  return t('team.member.task', { number: taskKeyOfTask(task), title: task.title, state: t(`team.member.state.${task.state}`) })
}

/** Line 3: 「3 分前 完成：text」. */
export function memberLastLine(t: T, last: NonNullable<JournalMember['last']>, now: number): string {
  const kind = REPORT_KINDS.has(last.kind) ? t(`team.member.kind.${last.kind}`) : t('team.member.kind.other')
  return t('team.member.last', { age: ageText(t, last.at, now), kind, text: last.text })
}

// spa/src/components/team/usePersonalActions.ts — what the personal panel's settings do (task groups spec §5, §7, TG-5c): move the session to a group, start a group from it, take it out, rename and recolour its group.
// Every write goes through `session-actions.ts` (which finds the session's root and refuses a changed tab) and `manual-actions.ts`; after a move the tab is the group's last tab (R18, `placeLastInGroup`). A refusal is a notice line.
import { useCallback, useMemo, useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { failureText } from '../../lib/task-groups/row-menu'
import { defaultManualGroupName, recolorManualGroup, renameManualGroup, type ManualActionResult } from '../../lib/task-groups/manual-actions'
import { fileSession, startGroupWithSession, takeSessionOut, type SessionActionResult, type SessionRef } from '../../lib/task-groups/session-actions'
import { placeLastInGroup } from '../../lib/task-groups/place-last'
import type { PersonalTarget } from '../../lib/task-groups/personal'

export interface PersonalActions {
  /** The last refusal in words, or null. */
  notice: string | null
  /** A move / new group / take-out is out: the next one waits for it (a root read can take a moment), so a double click does not start two. */
  busy: boolean
  move: (groupId: string) => Promise<boolean>
  /** Starts 「任務編組 N」 with this session in it; the new group's id, or null when it refused. */
  startGroup: () => Promise<string | null>
  takeOut: () => Promise<boolean>
  rename: (groupId: string, name: string) => boolean
  recolor: (groupId: string, color: string) => boolean
}

export function usePersonalActions(target: PersonalTarget): PersonalActions {
  const t = useI18nStore((s) => s.t)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false)
  const { hostId, agentSessionId, tabId } = target
  const ref = useMemo<SessionRef>(() => ({ hostId, agentSessionId, tabId }), [hostId, agentSessionId, tabId])
  const report = useCallback((r: SessionActionResult | ManualActionResult): boolean => {
    setNotice(r.ok ? null : failureText(t, r))
    return r.ok
  }, [t])
  const settle = useCallback((r: SessionActionResult): boolean => {
    if (report(r)) placeLastInGroup(tabId) // R18: the session ends up last in the group it was moved to
    return r.ok
  }, [report, tabId])
  /** Runs one session write at a time; a second one while it is out is dropped, not queued (the person can click again). */
  const once = async <T,>(run: () => Promise<T>, dropped: T): Promise<T> => {
    if (inFlight.current) return dropped
    inFlight.current = true
    setBusy(true)
    try { return await run() } finally { inFlight.current = false; setBusy(false) }
  }
  return {
    notice,
    busy,
    move: (groupId) => once(async () => settle(await fileSession(ref, groupId)), false),
    startGroup: () => once(async () => {
      const r = await startGroupWithSession(ref, () => defaultManualGroupName(t('taskgroups.default_name')))
      return settle(r) && r.ok ? r.id : null
    }, null),
    takeOut: () => once(async () => settle(await takeSessionOut(ref)), false),
    rename: (groupId, name) => report(renameManualGroup(groupId, name)),
    recolor: (groupId, color) => report(recolorManualGroup(groupId, color)),
  }
}

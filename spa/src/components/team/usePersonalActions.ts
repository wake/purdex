// spa/src/components/team/usePersonalActions.ts — what the personal panel's settings do (task groups spec §5, §7, TG-5c): move the session to a group, start a group from it, take it out, rename and recolour its group.
// Every write goes through `session-actions.ts` (which finds the session's root and refuses a changed tab) and `manual-actions.ts`; after a move the tab is the group's last tab (R18, `placeLastInGroup`). A refusal is a notice line.
import { useCallback, useMemo, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { failureText } from '../../lib/task-groups/row-menu'
import { defaultManualGroupName, recolorManualGroup, renameManualGroup, type ManualActionResult } from '../../lib/task-groups/manual-actions'
import { fileSession, startGroupWithSession, takeSessionOut, type SessionActionResult, type SessionRef } from '../../lib/task-groups/session-actions'
import { placeLastInGroup } from '../../lib/task-groups/place-last'
import type { PersonalTarget } from '../../lib/task-groups/personal'

export interface PersonalActions {
  /** The last refusal in words, or null. */
  notice: string | null
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
  return {
    notice,
    move: async (groupId) => settle(await fileSession(ref, groupId)),
    startGroup: async () => {
      const r = await startGroupWithSession(ref, defaultManualGroupName(t('taskgroups.default_name')))
      return settle(r) && r.ok ? r.id : null
    },
    takeOut: async () => settle(await takeSessionOut(ref)),
    rename: (groupId, name) => report(renameManualGroup(groupId, name)),
    recolor: (groupId, color) => report(recolorManualGroup(groupId, color)),
  }
}

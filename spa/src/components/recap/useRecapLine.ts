// spa/src/components/recap/useRecapLine.ts — what a row's / card's second line says about a session (plan T14b, prototype decision 1 = B):
//   * the newest recap → its headline and when it was written (`截至 HH:MM`);
//   * no recap, but a turn summary → 「回合 N」 and that summary's title, until the first recap comes;
//   * a recap being written for a session with none → say so;
//   * nothing (or a host without `dialogue.v1`) → null, and the host is never asked.
// The hook reads each session once (the newest recap; the newest turn summary when there is no recap). Later changes arrive through the store
// (`dialogue.recap` events, the history view's own loads).
import { useEffect } from 'react'
import { selectDialogueEntry, selectDialogueSupport, useDialogueStore } from '../../stores/useDialogueStore'

export type RecapLineData =
  | { kind: 'recap'; headline: string; at: number; generating: boolean }
  | { kind: 'first_generating' }
  | { kind: 'turn'; no: number; title: string }

export function useRecapLine(hostId: string, sessionId: string): RecapLineData | null {
  const usable = useDialogueStore((s) => hostId !== '' && sessionId !== '' && selectDialogueSupport(hostId, s))
  const entry = useDialogueStore((s) => selectDialogueEntry(s, hostId, sessionId))
  const read = entry !== undefined
  const noRecap = entry?.recaps != null && entry.recaps.recaps.length === 0
  useEffect(() => {
    if (!usable || useDialogueStore.getState().byHost[hostId]?.[sessionId] !== undefined) return
    void useDialogueStore.getState().loadRecaps(hostId, sessionId, 1)
  }, [usable, read, hostId, sessionId])
  useEffect(() => {
    if (!usable || !noRecap) return
    const cached = useDialogueStore.getState().byHost[hostId]?.[sessionId]
    if (cached !== undefined && Object.keys(cached.summaries).length > 0) return
    void useDialogueStore.getState().loadLatestSummaries(hostId, sessionId, 1)
  }, [usable, noRecap, hostId, sessionId])

  if (!usable || entry === undefined) return null
  const newest = entry.recaps?.recaps[0]
  if (newest !== undefined) return { kind: 'recap', headline: newest.headline, at: newest.at, generating: entry.generating }
  if (entry.generating) return { kind: 'first_generating' }
  const nos = Object.keys(entry.summaries).map(Number)
  if (nos.length === 0) return null
  const latest = entry.summaries[Math.max(...nos)]
  return { kind: 'turn', no: latest.turn_no, title: latest.title }
}

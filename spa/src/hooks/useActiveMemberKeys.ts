// spa/src/hooks/useActiveMemberKeys.ts — the active team members' `hostId:code` keys for React: the App's one shared
// snapshot (lib/team/member-set.ts). It keeps its identity while the membership is unchanged, so a session-list refresh
// or a roster frame that changes no membership re-renders no consumer.
import { useMemberSetStore } from '../lib/team/member-set'

export function useActiveMemberKeys(): ReadonlySet<string> {
  return useMemberSetStore((s) => s.keys)
}

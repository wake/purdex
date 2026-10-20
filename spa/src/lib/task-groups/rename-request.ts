// spa/src/lib/task-groups/rename-request.ts — a request, from outside the header, to start renaming a manual group in place: the header's context menu 「重新命名」 and 「新增任務編組」 (which creates a group and starts its rename).
// Transient (memory only): the header whose group id matches opens its name input — also one that mounts after the request was made (a group just created) — and then `consume`s the request, so a later mount of the same header
// (another view, another window of the list) does not open it again. A counter, so asking twice in a row opens it twice.
import { create } from 'zustand'

interface RenameRequest {
  groupId: string | null
  nonce: number
  request: (groupId: string) => void
  /** The header took the request. */
  consume: () => void
}

export const useRenameRequestStore = create<RenameRequest>((set) => ({
  groupId: null,
  nonce: 0,
  request: (groupId) => set((s) => ({ groupId, nonce: s.nonce + 1 })),
  consume: () => set((s) => (s.groupId === null ? s : { groupId: null })),
}))

export const requestRename = (groupId: string): void => useRenameRequestStore.getState().request(groupId)

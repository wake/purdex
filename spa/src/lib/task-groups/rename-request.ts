// spa/src/lib/task-groups/rename-request.ts — a request, from outside the header, to start renaming a manual group in place: the header's context menu 「重新命名」 and 「新增任務編組」 (which creates a group and starts its rename).
// Transient (memory only): the header whose group id matches opens its name input when `nonce` changes. A counter, not a flag, so asking twice in a row opens it twice.
import { create } from 'zustand'

interface RenameRequest {
  groupId: string | null
  nonce: number
  request: (groupId: string) => void
}

export const useRenameRequestStore = create<RenameRequest>((set) => ({
  groupId: null,
  nonce: 0,
  request: (groupId) => set((s) => ({ groupId, nonce: s.nonce + 1 })),
}))

export const requestRename = (groupId: string): void => useRenameRequestStore.getState().request(groupId)

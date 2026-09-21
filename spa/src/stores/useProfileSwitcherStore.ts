// spa/src/stores/useProfileSwitcherStore.ts — whether the profile switcher (the Home button's menu) is open.
//
// A store and not the button's own state because two things open it: a click on the Home button, and the
// `switch-workspace-home` shortcut, which lives in a hook with no way to reach a component (useShortcuts.ts).
// One flag serves both bars — only one of them is rendered at a time. Never persisted: like `useUndoToast`, it
// is what is on screen right now, not a preference.
//
// THE SWITCHER EXISTS ONLY WHERE THERE IS A SLAVE (P3 plan, P3d-1). With none, the Home button does what it
// always did and nothing here is ever set — a user who never touched the feature sees today's app.
import { useEffect } from 'react'
import { create } from 'zustand'
import { useLocalProfilesStore } from './useLocalProfilesStore'

interface ProfileSwitcherState {
  open: boolean
  setOpen: (open: boolean) => void
}

export const useProfileSwitcherStore = create<ProfileSwitcherState>()((set) => ({
  open: false,
  setOpen: (open) => set({ open }),
}))

/** `slaveOrder` is a permutation of the keys of `slaves` (the store's invariant): its length is the count. */
export function hasLocalSlaves(): boolean {
  return useLocalProfilesStore.getState().slaveOrder.length > 0
}

/** What a Home button needs: is it a menu trigger, is the menu open, and what a click does. */
export function useProfileSwitcherTrigger(onSelectHome: () => void): {
  enabled: boolean
  open: boolean
  onClick: () => void
  /** Spread on the button. Empty without a slave: a plain button must not announce a menu. */
  triggerProps: { 'aria-haspopup'?: 'menu'; 'aria-expanded'?: boolean }
} {
  const enabled = useLocalProfilesStore((s) => s.slaveOrder.length > 0)
  const requested = useProfileSwitcherStore((s) => s.open)
  const setOpen = useProfileSwitcherStore((s) => s.setOpen)
  const open = enabled && requested

  // A request nobody can honour is dropped — the last slave was deleted under an open menu (another window),
  // or the flag was set with none. Left standing, it would open the menu by itself when a slave next appears.
  useEffect(() => {
    if (requested && !enabled) setOpen(false)
  }, [requested, enabled, setOpen])

  return {
    enabled,
    open,
    onClick: enabled ? () => setOpen(!open) : onSelectHome,
    triggerProps: enabled ? { 'aria-haspopup': 'menu', 'aria-expanded': open } : {},
  }
}

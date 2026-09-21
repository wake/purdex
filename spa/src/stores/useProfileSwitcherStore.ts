// spa/src/stores/useProfileSwitcherStore.ts — the profile switcher (the Home button's menu): whether it is open,
// and the switch that is under way.
//
// WHY A STORE AND NOT THE BUTTON'S OWN STATE. Two things open the menu: a click on the Home button, and the
// `switch-workspace-home` shortcut, which lives in a hook with no way to reach a component (useShortcuts.ts).
// One flag serves both bars — only one of them is rendered at a time. Never persisted: like `useUndoToast`, it
// is what is on screen right now, not a preference.
//
// WHY THE SWITCH UNDER WAY LIVES HERE TOO. The two bars are two components, and changing the bar's width
// unmounts one and mounts the other — with `open` still true. Were the attempt the component's, the new menu
// would come up knowing nothing of it and let a SECOND switch start beside the first. So there is one owner that
// no remount touches: `pending`, its retry timer, and what is done with every answer. A component only reads.
//
// `generation` — every `chooseProfile` takes the next one, and an answer is acted on only while `pending` still
// carries the generation it was asked under. Anything else is an answer to a question nobody is asking any more.
//
// CLOSING THE MENU IS "STOP TRYING", NOT "UNDO". A `switchActiveProfile` that was already called cannot be
// called back: it runs to its end and its answer is handled like any other — a success is a fact (the world on
// screen HAS changed), a real failure is still said. What closing ends is the silent `busy` retrying: a pending
// retry timer is cleared, and a `busy` that arrives afterwards is dropped without a word — the user walked away
// from the one thing it would have been retried for. Until that answer arrives `pending` stays, so that a menu
// re-opened meanwhile cannot start a second switch either.
//
// IDLE MEANS IDLE. No timer, no listener and no subscription exists unless a switch is pending.
//
// THE SWITCHER EXISTS ONLY WHERE THERE IS A SLAVE (P3 plan, P3d-1). With none, the Home button does what it
// always did and nothing here is ever set — a user who never touched the feature sees today's app.
import { useEffect } from 'react'
import { create } from 'zustand'
import { useI18nStore } from './useI18nStore'
import { useLocalProfilesStore, MASTER_PROFILE_ID } from './useLocalProfilesStore'
import { useUndoToast } from './useUndoToast'
import { switchActiveProfile, type SwitchResult } from '../lib/profile/switch-active'

/** `busy` is the 3 s ownership gate or another window holding the world lock (P3 plan, "What the UI must say"):
 *  it ends by itself, so it is retried in silence — this often, until this long has passed SINCE THE CLICK (a
 *  refusal can itself have taken 3 s to arrive, so attempts are not what is counted) — and only then said. */
export const BUSY_RETRY_MS = 250
export const BUSY_RETRY_TOTAL_MS = 4_000

export interface PendingSwitch {
  targetId: string
  generation: number
  /** Epoch ms of the click: what the `busy` budget is measured from. */
  startedAt: number
  /** The menu was closed since: a `busy` answer is no longer retried (nor said). */
  abandoned: boolean
}

interface ProfileSwitcherState {
  open: boolean
  pending: PendingSwitch | null
  setOpen: (open: boolean) => void
  /** Start switching to `targetId`. Refused (false) while another switch is pending. */
  chooseProfile: (targetId: string) => boolean
}

let generation = 0
/** Non-null only between a `busy` answer and its retry. */
let retryTimer: ReturnType<typeof setTimeout> | null = null

function clearRetryTimer(): boolean {
  if (retryTimer === null) return false
  clearTimeout(retryTimer)
  retryTimer = null
  return true
}

export const useProfileSwitcherStore = create<ProfileSwitcherState>()((set, get) => {
  const say = (key: string, params?: Record<string, string>) => useUndoToast.getState().show(useI18nStore.getState().t(key, params))

  /** The switch is over. The menu stays unless told otherwise: nothing was written, the user is where they were. */
  const end = (closeMenu: boolean) => set(closeMenu ? { pending: null, open: false } : { pending: null })

  const settle = (r: SwitchResult, mine: number) => {
    const pending = get().pending
    if (pending === null || pending.generation !== mine) return
    if (r.ok || r.reason === 'already-on-screen') return end(true)
    switch (r.reason) {
      case 'busy':
        if (pending.abandoned) return end(false)
        if (Date.now() - pending.startedAt < BUSY_RETRY_TOTAL_MS) {
          retryTimer = setTimeout(() => {
            retryTimer = null
            attempt(mine)
          }, BUSY_RETRY_MS)
          return
        }
        say('profile.switch.busy')
        return end(false)
      case 'unsettled':
        // Not an error: the refusal has already asked the stores to catch up, the same click works a moment later.
        say('profile.switch.try_again')
        return end(false)
      case 'superseded':
        // Another window's switch won and this window is about to show ITS world: not retried, and the menu goes.
        say('profile.switch.try_again')
        return end(true)
      case 'write-failed':
        say('profile.switch.write_failed', { detail: r.detail })
        return end(false)
      case 'not-found':
        say('profile.switch.not_found')
        return end(false)
      default:
        say('profile.switch.failed', { reason: r.reason })
        return end(false)
    }
  }

  const attempt = (mine: number) => {
    const pending = get().pending
    if (pending === null || pending.generation !== mine) return
    switchActiveProfile(pending.targetId).then(
      (r) => settle(r, mine),
      (e: unknown) => {
        if (get().pending?.generation !== mine) return
        say('profile.switch.failed', { reason: e instanceof Error ? e.message : String(e) })
        end(false)
      },
    )
  }

  return {
    open: false,
    pending: null,
    setOpen: (open) => {
      const { pending } = get()
      if (open || pending === null) return set({ open })
      // Closed with a switch pending — see CLOSING THE MENU IS "STOP TRYING". Between two attempts there is
      // nothing in flight and the switch simply ends; otherwise the call in flight is left to answer.
      set(clearRetryTimer() ? { open, pending: null } : { open, pending: { ...pending, abandoned: true } })
    },
    chooseProfile: (targetId) => {
      if (get().pending !== null) return false
      const mine = ++generation
      set({ pending: { targetId, generation: mine, startedAt: Date.now(), abandoned: false } })
      attempt(mine)
      return true
    },
  }
})

/** Back to the state of a fresh page load. */
export function __resetProfileSwitcherForTest(): void {
  clearRetryTimer()
  useProfileSwitcherStore.setState({ open: false, pending: null })
}

/** `slaveOrder` is a permutation of the keys of `slaves` (the store's invariant): its length is the count. */
export function hasLocalSlaves(): boolean {
  return useLocalProfilesStore.getState().slaveOrder.length > 0
}

/** What a Home button needs: is it a menu trigger, is the menu open, what a click does, and what it says. */
export function useProfileSwitcherTrigger(onSelectHome: () => void): {
  enabled: boolean
  open: boolean
  onClick: () => void
  /** The name of the profile on screen — null without a slave: there is one world, and the button is `Home`. */
  currentName: string | null
  /** The on-screen profile is a slave: a world that never syncs, which is what a user most needs to see. */
  onSlave: boolean
  /** Spread on the button. Empty without a slave: a plain button must not announce a menu. */
  triggerProps: { 'aria-haspopup'?: 'menu'; 'aria-expanded'?: boolean; 'aria-label'?: string }
} {
  const t = useI18nStore((s) => s.t)
  const enabled = useLocalProfilesStore((s) => s.slaveOrder.length > 0)
  const activeProfileId = useLocalProfilesStore((s) => s.activeProfileId)
  const slaveName = useLocalProfilesStore((s) => s.slaves[s.activeProfileId]?.name)
  const requested = useProfileSwitcherStore((s) => s.open)
  const setOpen = useProfileSwitcherStore((s) => s.setOpen)
  const open = enabled && requested

  // A request nobody can honour is dropped — the last slave was deleted under an open menu (another window),
  // or the flag was set with none. Left standing, it would open the menu by itself when a slave next appears.
  useEffect(() => {
    if (requested && !enabled) setOpen(false)
  }, [requested, enabled, setOpen])

  const onSlave = enabled && activeProfileId !== MASTER_PROFILE_ID
  // A slave's name is the user's own text, never through t(). (A pointer at a slave that is not there is the
  // store's `merge` to heal; until then the master's name is the honest fallback for a label.)
  const currentName = !enabled ? null : onSlave && slaveName !== undefined ? slaveName : t('profile.master')

  return {
    enabled,
    open,
    onClick: enabled ? () => setOpen(!open) : onSelectHome,
    currentName,
    onSlave,
    triggerProps: currentName === null
      ? {}
      : { 'aria-haspopup': 'menu', 'aria-expanded': open, 'aria-label': t('profile.switcher.trigger', { name: currentName }) },
  }
}

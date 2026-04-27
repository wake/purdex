/**
 * Slot identifiers for the Quick Commands v2 capability/binding/slot model
 * (spec §2.2). Settings UI, <CommandSlot>, and binding sanitizer must read
 * from this constant — string literals are forbidden so a typo can't drift
 * away from the registered slots.
 *
 * If/when external modules need to register new slots, upgrade to a
 * `registerQuickCommandSlot()` registry (Phase 2+; YAGNI for now).
 */
export const QUICK_COMMAND_SLOTS = {
  WORKSPACE_ACTIONS: 'workspace.actions',
  HOST_ACTIONS: 'host.actions',
} as const

export type QuickCommandSlotId =
  (typeof QUICK_COMMAND_SLOTS)[keyof typeof QUICK_COMMAND_SLOTS]

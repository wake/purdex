// spa/src/lib/names/avatar-options-memory.ts — the half-typed DiceBear options text of the avatar picker, kept outside the
// component. The text is only valid JSON now and then, so the recipe (which holds the parsed options) cannot carry it, and
// a settings page that unmounts when its tab is switched away would lose what the user was in the middle of typing (see
// the tab-hosted checklist in CLAUDE.md). Memory only, one entry per persona slot; it is never sent anywhere.
const held = new Map<string, string>()

export function recallAvatarOptionsText(slot: string): string | undefined {
  return held.get(slot)
}

export function rememberAvatarOptionsText(slot: string, text: string): void {
  held.set(slot, text)
}

/** A persona being opened afresh starts from its own recipe, not from what an earlier one with the same slot left. */
export function forgetAvatarOptionsText(slot: string): void {
  held.delete(slot)
}

export function clearAvatarOptionsMemory(): void {
  held.clear()
}

// spa/src/components/approval-dialog-helpers.ts — the dialog's pure helpers and shared class strings, moved verbatim out of
// ApprovalDialogHost.tsx (#2322): focus-trap stops, the roots textarea parser, the team-name rule, the field / button classes.
import { TEAM_NAME_CHARS } from '../lib/team/label'

export const FOCUSABLE_SELECTOR = 'input, button, textarea, [tabindex]:not([tabindex="-1"])'

export function tabStops(panel: HTMLElement): HTMLElement[] {
  return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
    .filter((el) => !(el as HTMLButtonElement).disabled && el.tabIndex >= 0)
}

export function parseRoots(text: string): string[] {
  return text.split('\n').map((line) => line.trim()).filter((line) => line !== '')
}

// A team name, as the daemon judges it (D-N2, peers.ValidateTitle): at most 64 UTF-8 bytes and nothing but what Go's
// unicode.IsPrint accepts — letters, marks, numbers, punctuation, symbols and the ASCII space. So controls, U+200B and
// other Cf characters, NBSP and U+3000 are refused here as the daemon refuses them (it is the authority; this only
// keeps 核准 from sending a name that comes back 400). Leading / trailing white space is trimmed before the check.
export const TEAM_NAME_MAX_BYTES = 64
// goTrim and TEAM_NAME_CHARS live in lib/team/label.ts, where the label rules (the same character set, plus a width)
// are; the trim is Go's, not JS's.
export const teamNameOk = (name: string) => new TextEncoder().encode(name).length <= TEAM_NAME_MAX_BYTES && TEAM_NAME_CHARS.test(name)

export const fieldClass = 'rounded-md border border-border-default bg-surface-input px-2 py-1 text-xs text-text-primary disabled:opacity-50'
// `aria-disabled` dims like RestartDaemonButton's counting state: the button still takes the click (it queues).
export const buttonBase = 'px-3 py-1 rounded-md text-xs cursor-pointer disabled:opacity-50 disabled:cursor-default aria-disabled:opacity-50 flex items-center gap-1.5'

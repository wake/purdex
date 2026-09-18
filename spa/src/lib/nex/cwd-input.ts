// spa/src/lib/nex/cwd-input.ts — client-side shape check for the Headless
// launcher's "sub-path under a root" field (P-C spec §4.2). A hint for the
// user, not a security boundary: the daemon's `state: 'rejected'` is the
// authority for anything the client cannot know (existence, symlink escape).
// Nothing here is expanded or normalised beyond trimming one trailing slash,
// so what the user typed is what goes on the wire.

export type SubPathReason =
  | 'absolute'
  | 'tilde'
  | 'dotdot'
  | 'dot_segment'
  | 'empty_segment'
  | 'backslash'
  | 'whitespace'

export type SubPathVerdict =
  | { ok: true; value: string }
  | { ok: false; reason: SubPathReason }

export function validateSubPath(sub: string): SubPathVerdict {
  if (sub === '') return { ok: true, value: '' }
  if (sub !== sub.trim()) return { ok: false, reason: 'whitespace' }
  if (sub.startsWith('/')) return { ok: false, reason: 'absolute' }
  if (sub.startsWith('~')) return { ok: false, reason: 'tilde' }
  if (sub.includes('\\')) return { ok: false, reason: 'backslash' }
  const value = sub.endsWith('/') ? sub.slice(0, -1) : sub
  for (const segment of value.split('/')) {
    if (segment === '..') return { ok: false, reason: 'dotdot' }
    if (segment === '.') return { ok: false, reason: 'dot_segment' }
    if (segment === '') return { ok: false, reason: 'empty_segment' }
  }
  return { ok: true, value }
}

/** `root` is a canonical absolute path from capabilities; `sub` a validated sub-path or `''`. */
export function joinCwd(root: string, sub: string): string {
  const base = root.length > 1 && root.endsWith('/') ? root.slice(0, -1) : root
  return sub === '' ? base : `${base}/${sub}`
}

const encoder = new TextEncoder()

/** The daemon's brief limit is in UTF-8 bytes (spec F1), not characters. */
export function utf8ByteLength(s: string): number {
  return encoder.encode(s).length
}

// spa/src/lib/command-word.ts — the shell "command word" of a template, the
// only thing a resolve-command probe is ever sent.

/**
 * A POSIX simple command may be preceded by variable assignments —
 * `OPENCODE_YOLO=true opencode -s <id>` is one command, not two — and the shell
 * grammar's "command word" is what follows them. Probing the first token
 * instead would ask the daemon to resolve `OPENCODE_YOLO=true`, which is not a
 * command and never resolves, so a working template would report itself broken.
 *
 * `NAME=` is the whole test: the value may contain anything, `=` included. A
 * flag or a path can never match, because neither starts with an identifier
 * followed by `=`.
 *
 * Returns '' for a template that is nothing but assignments, which leaves the
 * Test button disabled rather than probing something meaningless.
 */
const ASSIGNMENT = /^[A-Za-z_][A-Za-z0-9_]*=/

export function commandWordOf(template: string): string {
  for (const token of template.trim().split(/\s+/)) {
    if (token && !ASSIGNMENT.test(token)) return token
  }
  return ''
}

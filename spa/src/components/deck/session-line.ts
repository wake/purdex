// spa/src/components/deck/session-line.ts — the text of the session status row, shaped like Claude Code's terminal status line:
// `<repo> [<worktree>] [<model · effort>]`. Pure: the caller hands over whatever it knows; a missing part is left out.

export interface SessionLineInput {
  /** The session's working directory (absolute path) or null. */
  cwd: string | null | undefined
  /** A worktree name the statusLine snapshot reported, if any (wins over the path). */
  worktree?: string | null
  /** A git branch, shown only when the data already exists in the App. */
  branch?: string | null
  model: string | null | undefined
  effort: string | null | undefined
}

export interface SessionLineParts { repo: string | null; worktree: string | null; branch: string | null; model: string | null }

const base = (p: string): string => p.replace(/\/+$/, '').split('/').pop() ?? ''
const WORKTREE = /^(.*?)\/\.claude\/worktrees\/([^/]+)/

/** repo + worktree from a path: `/a/purdex/.claude/worktrees/iface-u1-3[/spa]` → purdex + iface-u1-3; any other path → its basename. */
export function repoOfCwd(cwd: string | null | undefined): { repo: string | null; worktree: string | null } {
  if (typeof cwd !== 'string' || cwd.trim() === '') return { repo: null, worktree: null }
  const m = WORKTREE.exec(cwd.trim())
  if (m) return { repo: base(m[1]) || null, worktree: m[2] }
  return { repo: base(cwd.trim()) || null, worktree: null }
}

export function sessionLineParts(i: SessionLineInput): SessionLineParts {
  const { repo, worktree } = repoOfCwd(i.cwd)
  const model = i.model?.trim() ? (i.effort?.trim() ? `${i.model.trim()} · ${i.effort.trim()}` : i.model.trim()) : null
  return { repo, worktree: i.worktree?.trim() || worktree, branch: i.branch?.trim() || null, model }
}

/** `purdex [iface-u1-3] (main) [Opus 5.5 · xhigh]`; parts that are unknown are left out. */
export function formatSessionLine(i: SessionLineInput): string {
  const p = sessionLineParts(i)
  return [p.repo, p.worktree && `[${p.worktree}]`, p.branch && `(${p.branch})`, p.model && `[${p.model}]`].filter(Boolean).join(' ')
}

const str = (v: unknown): string | null => (typeof v === 'string' && v.trim() !== '' ? v.trim() : null)
const obj = (v: unknown): Record<string, unknown> | null => (typeof v === 'object' && v !== null ? (v as Record<string, unknown>) : null)

/** Claude Code's statusLine JSON: `workspace.current_dir` / `cwd`, and `worktree.{name,branch}` when it runs in a worktree. */
export function readWhere(raw: Record<string, unknown> | undefined): { cwd: string | null; worktree: string | null; branch: string | null } {
  const ws = obj(raw?.workspace)
  const wt = obj(raw?.worktree)
  return { cwd: str(ws?.current_dir) ?? str(raw?.cwd), worktree: str(wt?.name), branch: str(wt?.branch) }
}

// spa/src/lib/nex/session-slug.ts — the `{slug}` a take-to-terminal uses to
// name the tmux session it creates (exec-to-terminal spec §4.2). The launcher
// names sessions `{HostProject.slug}-{N}`; an execution only knows its cwd, so
// the slug is looked up from the host's projects — the one whose `path` is the
// cwd itself or its nearest ancestor (a worktree under the repo lands on the
// repo's slug). With no project to name it after, the cwd basename is cleaned
// to the launcher's character set so the daemon's session-name regex accepts
// it.
import type { HostProject } from '../host-config-api'

const FALLBACK = 'nex'

function stripTrailingSlashes(path: string): string {
  let end = path.length
  while (end > 0 && path[end - 1] === '/') end--
  return path.slice(0, end)
}

/**
 * The cwd basename with every character outside `[a-zA-Z0-9_-]` replaced by
 * `-`, runs collapsed, ends trimmed; `nex` when nothing survives.
 */
export function fallbackSlugFor(cwd: string): string {
  const base = stripTrailingSlashes(cwd).split('/').pop() ?? ''
  const cleaned = base.replace(/[^a-zA-Z0-9_-]+/g, '-').replace(/-+/g, '-').replace(/^-+|-+$/g, '')
  return cleaned || FALLBACK
}

/**
 * The slug of the project whose path equals `cwd` or is its nearest ancestor
 * (segment-wise: `/a/b` is not an ancestor of `/a/bc`); a project with an
 * empty slug does not count. Otherwise `fallbackSlugFor(cwd)`.
 */
export function slugForCwd(cwd: string, projects: readonly HostProject[]): string {
  const target = stripTrailingSlashes(cwd)
  let best: HostProject | undefined
  let bestLen = -1
  for (const p of projects) {
    if (!p.slug) continue
    const path = stripTrailingSlashes(p.path)
    if (path === '') continue
    if (ancestorOf(path, target) && path.length > bestLen) {
      best = p
      bestLen = path.length
    }
  }
  return best ? best.slug : fallbackSlugFor(cwd)
}

/**
 * Is `path` the cwd itself or a segment-wise ancestor of it? A project path
 * is stored as the user typed it, so `~/…` is common (the daemon expands it
 * only when it checks the path, `hostconfig/checkpath.go`), while an
 * execution cwd is always absolute. The SPA does not know the host's home,
 * so a `~/rest` project matches when `/rest` is a segment-aligned suffix of
 * some prefix of the cwd — i.e. the cwd is `<home>/rest` or below it.
 */
function ancestorOf(path: string, target: string): boolean {
  if (path === '~' ) return false
  if (path.startsWith('~/')) {
    const rest = path.slice(1) // "/Workspace/wake/ploom" — leads with "/", so every hit is segment-aligned
    for (let at = target.indexOf(rest, 1); at > 0; at = target.indexOf(rest, at + 1)) {
      const after = target.slice(at + rest.length)
      if (after === '' || after.startsWith('/')) return true
    }
    return false
  }
  return target === path || target.startsWith(`${path}/`)
}

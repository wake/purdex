// spa/src/lib/journal/refs.ts — an entry's refs as links (spec §5.1). `#N` / `PR #N` -> `<project.remote_url>/issues/<N>` (GitHub
// forwards a PR number from /issues/). No remote URL, or a ref that is not exactly `#digits` (a task number like `7bcf08-12`):
// plain text. Only http(s) is ever opened.

const REF = /^(?:PR\s*)?#(\d+)$/i

/** The remote as a clean https/http base (no trailing slash, no `.git`), or null when it is not one. */
function baseOf(remoteUrl: string): string | null {
  const s = remoteUrl.trim()
  if (s === '') return null
  let u: URL
  try { u = new URL(s) } catch { return null }
  if (u.protocol !== 'https:' && u.protocol !== 'http:') return null
  return `${u.origin}${u.pathname.replace(/\/+$/, '').replace(/\.git$/, '')}`
}

export function refUrl(ref: string, remoteUrl: string): string | null {
  const m = REF.exec(ref.trim())
  const base = m ? baseOf(remoteUrl) : null
  return m && base ? `${base}/issues/${m[1]}` : null
}

/** The shell's browser when there is one, else a new window that cannot reach back (`noopener`). */
export function openExternal(url: string): void {
  if (!/^https?:\/\//i.test(url)) return
  if (window.electronAPI?.openExternalUrl) void window.electronAPI.openExternalUrl(url)
  else window.open(url, '_blank', 'noopener')
}

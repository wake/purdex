// spa/src/lib/nex/test-cwd.ts — whether a conversation/worker cwd is a "test" one: under /private/tmp (and /tmp, its
// macOS alias). Same semantics as the daemon's conversations.IsTestCwd; both read internal/conversations/testdata/
// test-cwd-cases.json. Hand-rolled POSIX normalisation (no Node `path` in the renderer).

const TMP = '/private/tmp'

/** Resolves `.`/`..`, collapses `//`, drops a trailing slash. Input must be absolute. */
function normalize(abs: string): string {
  const out: string[] = []
  for (const seg of abs.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') { out.pop(); continue }
    out.push(seg)
  }
  return '/' + out.join('/')
}

export function isTestCwd(cwd?: string): boolean {
  if (!cwd || !cwd.startsWith('/')) return false
  let p = normalize(cwd)
  if (p === '/tmp' || p.startsWith('/tmp/')) p = TMP + p.slice('/tmp'.length)
  return p === TMP || p.startsWith(TMP + '/')
}

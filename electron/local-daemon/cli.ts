// The CLI half of the local-daemon module: the shape `pdx path --json`
// returns, how it is read, and what the app says when `pdx` is not
// reachable (spec §4.1/§4.5/§4.6).
//
// Nothing here imports ./index. The daemon owns the binary path, the home
// directory and the launch environment; every function below is handed the
// ones it needs.
import type { ExecFn, LaunchEnv } from './launch-env'

const PATH_TIMEOUT_MS = 5000

/**
 * `pdx path --json` (cmd/pdx/path.go `pathReport`). The TypeScript side never
 * walks PATH itself — `exec.LookPath` semantics, dangling symlinks and "is
 * this my own binary?" all live in Go, where the syscalls are (spec §4.1).
 */
export interface PathReport {
  self: string
  selfNote?: string
  resolved: string | null
  resolvedReal?: string
  isSelf: boolean
  localBin: string
  localBinExists: boolean
  localBinOnPath: boolean
  link: 'ok' | 'missing' | 'conflict' | 'error'
  linkTarget?: string
  linkError?: string
  fixes: string[]
  ok: boolean
}

/**
 * Four distinct answers, deliberately not collapsed into each other:
 * a report (exit 0 *or* exit 1 — `pdx path` exits 1 precisely when `pdx` is
 * not reachable, which is data, not a failure), output we could not read, a
 * binary that would not run, and no binary at all.
 */
export type CliState =
  | { kind: 'report'; code: number | null; report: PathReport }
  | { kind: 'unparseable'; code: number | null; stdout: string; stderr: string }
  | { kind: 'exec-failed'; error: string }
  | { kind: 'not-installed' }

function asPathReport(stdout: string): PathReport | null {
  let j: unknown
  try {
    j = JSON.parse(stdout.trim())
  } catch {
    return null
  }
  if (j === null || typeof j !== 'object' || Array.isArray(j)) return null
  const o = j as Record<string, unknown>
  const ok = typeof o.self === 'string'
    && (o.resolved === null || typeof o.resolved === 'string')
    && typeof o.isSelf === 'boolean'
    && typeof o.localBin === 'string'
    && typeof o.localBinOnPath === 'boolean'
    && typeof o.link === 'string'
    && Array.isArray(o.fixes)
    && typeof o.ok === 'boolean'
  return ok ? (o as unknown as PathReport) : null
}

/**
 * Ask the managed binary how the CLI is reachable, using the PATH the daemon
 * would be launched with (spec §4.1).
 */
export async function readCliState(
  deps: { exec: ExecFn; exists: (p: string) => Promise<boolean> },
  binPath: string,
  env: NodeJS.ProcessEnv,
): Promise<CliState> {
  if (!(await deps.exists(binPath))) return { kind: 'not-installed' }
  let r
  try {
    r = await deps.exec(binPath, ['path', '--json'], { env, timeoutMs: PATH_TIMEOUT_MS })
  } catch (e) {
    return { kind: 'exec-failed', error: e instanceof Error ? e.message : String(e) }
  }
  // A timeout or a spawn failure (code null) is not an answer about PATH.
  if (r.timedOut) return { kind: 'exec-failed', error: `pdx path timed out after ${PATH_TIMEOUT_MS}ms` }
  if (r.code === null) return { kind: 'exec-failed', error: `pdx path did not run: ${r.stderr.trim() || 'spawn failed'}` }
  const report = asPathReport(r.stdout)
  if (!report) return { kind: 'unparseable', code: r.code, stdout: r.stdout, stderr: r.stderr }
  return { kind: 'report', code: r.code, report }
}

/**
 * Spec §4.5. The message *is* the feature, so it is pinned here rather than
 * left to a caller: it names the binary, both commands, and what to do next.
 * Which command comes first follows `pdx path`'s own `fixes` — but both are
 * always shown, because guessing wrong and hiding the one that was needed is
 * the failure mode to avoid.
 */
export function pathRefusalMessage(report: PathReport, home: string): string {
  // ~/.config/pdx/bin/pdx reads better than the absolute path in a message
  // the user is meant to act on; both name the same file.
  const tilde = (p: string) => (p === home ? '~' : p.startsWith(home + '/') ? '~' + p.slice(home.length) : p)
  const bin = tilde(report.self)
  const localBin = tilde(report.localBin)
  const fixes = [
    { kind: 'link', line: `  ${bin} path link           create ${localBin}/pdx` },
    { kind: 'add-to-shell', line: `  ${bin} path add-to-shell   put ${localBin} on PATH` },
  ]
  const helps = (kind: string) => (report.fixes.includes(kind) ? 0 : 1)
  const ordered = [...fixes].sort((a, b) => helps(a.kind) - helps(b.kind))
  return [
    'Refusing to start: the command `pdx` is not on PATH, so agents following',
    'CLAUDE.md will get "command not found".',
    '',
    `The daemon binary is installed at ${bin}.`,
    '',
    'Fix it with either (both are also buttons in Settings → Development):',
    '',
    ...ordered.map((f) => f.line),
    '',
    'Then open a new terminal and run `pdx path` to confirm.',
  ].join('\n')
}

/**
 * The `cli` block of a status poll (spec §4.6), or nothing when the binary
 * did not give an answer. It is a projection, not a copy: `isManagedBinary`
 * is the name the UI uses for the report's `isSelf`, and `pathSource` comes
 * from the probe rather than from the report, because it describes how the
 * PATH was obtained and not what was found on it.
 */
export function cliStatusFields(cli: CliState, launch: LaunchEnv) {
  return cli.kind === 'report' ? {
    cli: {
      resolved: cli.report.resolved,
      isManagedBinary: cli.report.isSelf,
      pathSource: launch.pathSource,
      localBinOnPath: cli.report.localBinOnPath,
      link: cli.report.link,
    },
  } : {}
}

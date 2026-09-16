// A Finder-launched app inherits /usr/bin:/bin:/usr/sbin:/sbin. The daemon
// execs `tmux` (and agents exec `claude`, `codex`…) from PATH, so we launch
// it with the user's *shell* PATH. Spec §3.1 "Launch PATH".
import { join } from 'node:path'

export interface ExecResult { code: number | null; stdout: string; stderr: string; timedOut: boolean }
export type ExecFn = (
  file: string,
  args: string[],
  opts: { env?: NodeJS.ProcessEnv; cwd?: string; timeoutMs: number },
) => Promise<ExecResult>

export const SHELL_PROBE_TIMEOUT_MS = 5000

export function buildShellProbeScript(sentinel: string): string {
  // Newline framing + a per-call random sentinel: no NUL anywhere in argv,
  // and banner/plugin output on either side cannot leak into the value.
  return `printf '\\n%s%s%s\\n' '${sentinel}' "$PATH" '${sentinel}'`
}

export function extractBetweenSentinels(stdout: string, sentinel: string): string | null {
  const start = stdout.indexOf(sentinel)
  if (start < 0) return null
  const from = start + sentinel.length
  const end = stdout.indexOf(sentinel, from)
  if (end < 0) return null
  const value = stdout.slice(from, end)
  return value.length > 0 ? value : null
}

/** The probe's answer, plus why it failed — spec §4.4 turns on knowing that. */
export interface ShellProbeResult { path: string | null; error?: string }

export async function resolveShellPath(exec: ExecFn, shell: string, sentinel: string): Promise<ShellProbeResult> {
  const script = buildShellProbeScript(sentinel)
  const failures: string[] = []
  for (const flag of ['-ilc', '-lc']) {
    try {
      const r = await exec(shell, [flag, script], { timeoutMs: SHELL_PROBE_TIMEOUT_MS })
      if (r.timedOut) { failures.push(`${flag}: timed out after ${SHELL_PROBE_TIMEOUT_MS}ms`); continue }
      const value = extractBetweenSentinels(r.stdout, sentinel)
      if (value) return { path: value }
      const detail = r.stderr.trim() ? `: ${r.stderr.trim()}` : ''
      failures.push(`${flag}: exit ${r.code}, no PATH between sentinels${detail}`)
    } catch (e) {
      failures.push(`${flag}: ${e instanceof Error ? e.message : String(e)}`)
    }
  }
  return { path: null, error: `${shell} ${failures.join('; ')}` }
}

export function fallbackPath(basePath: string | undefined, home: string): string {
  const prefix = ['/opt/homebrew/bin', '/usr/local/bin', join(home, '.local/bin')]
  return basePath ? [...prefix, basePath].join(':') : prefix.join(':')
}

/**
 * How the launch PATH was obtained. 'fallback' means the login-shell probe
 * failed and `fallbackPath` *injected* ~/.local/bin — which proves what the
 * daemon's own env contains, never what the user's terminal has (spec §4.4).
 */
export type PathSource = 'shell' | 'fallback'

export interface LaunchEnv {
  env: NodeJS.ProcessEnv
  pathSource: PathSource
  /** Why the shell probe failed; only set when pathSource is 'fallback'. */
  probeError?: string
}

export async function buildLaunchEnv(deps: {
  exec: ExecFn
  shell: string | undefined
  baseEnv: NodeJS.ProcessEnv
  home: string
  sentinel: () => string
}): Promise<LaunchEnv> {
  const probe = await resolveShellPath(deps.exec, deps.shell || '/bin/zsh', deps.sentinel())
  if (probe.path !== null) {
    return { env: { ...deps.baseEnv, PATH: probe.path, PDX_DEV_MODE: '1' }, pathSource: 'shell' }
  }
  const PATH = fallbackPath(deps.baseEnv.PATH, deps.home)
  return { env: { ...deps.baseEnv, PATH, PDX_DEV_MODE: '1' }, pathSource: 'fallback', probeError: probe.error }
}

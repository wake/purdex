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

export async function resolveShellPath(exec: ExecFn, shell: string, sentinel: string): Promise<string | null> {
  const script = buildShellProbeScript(sentinel)
  for (const flag of ['-ilc', '-lc']) {
    try {
      const r = await exec(shell, [flag, script], { timeoutMs: SHELL_PROBE_TIMEOUT_MS })
      if (r.timedOut) continue
      const value = extractBetweenSentinels(r.stdout, sentinel)
      if (value) return value
    } catch {
      // try the next form
    }
  }
  return null
}

export function fallbackPath(basePath: string | undefined, home: string): string {
  const prefix = ['/opt/homebrew/bin', '/usr/local/bin', join(home, '.local/bin')]
  return basePath ? [...prefix, basePath].join(':') : prefix.join(':')
}

export async function buildLaunchEnv(deps: {
  exec: ExecFn
  shell: string | undefined
  baseEnv: NodeJS.ProcessEnv
  home: string
  sentinel: () => string
}): Promise<NodeJS.ProcessEnv> {
  const fromShell = await resolveShellPath(deps.exec, deps.shell || '/bin/zsh', deps.sentinel())
  const PATH = fromShell ?? fallbackPath(deps.baseEnv.PATH, deps.home)
  return { ...deps.baseEnv, PATH, PDX_DEV_MODE: '1' }
}

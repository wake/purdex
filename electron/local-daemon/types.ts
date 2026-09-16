// Types for the app-managed local pdx daemon (spec §3.1). Interfaces only —
// the factory `createLocalDaemon` lives in ./index.ts.
import type { ExecFn, PathSource } from './launch-env'
import type { Iface } from './config'

export interface LocalDaemonStatus {
  managed: 'none' | 'managed' | 'external'
  reason?: string
  binPath: string
  installed: { version: string; hash: string; goos: string; goarch: string } | null
  alive: { pid: number } | null
  running: { version: string; hash: string; url: string } | null
  config: { bind: string; port: number; token: string | null } | null
  /** os.hostname(); registerLocalHost needs it, so it is required. */
  hostname: string
  target: { goos: 'darwin' | 'linux'; goarch: 'arm64' | 'amd64' }
  tools: { tmux: string | null }
  /**
   * How `pdx` is reachable on the PATH the daemon is launched with (spec
   * §4.6). Absent when there is no binary to ask, or when the installed one
   * could not answer `path --json`. Every field but `pathSource` comes from
   * that answer; `pathSource` is this side's own knowledge of how it built
   * the PATH it passed in. `link` describes what is observed *now* — there is
   * no 'created', because status is recomputed on every poll and has no
   * memory of actions.
   */
  cli?: {
    resolved: string | null
    isManagedBinary: boolean
    pathSource: PathSource
    localBinOnPath: boolean
    link: 'ok' | 'missing' | 'conflict' | 'error'
  }
}

/**
 * 'path-unresolved' is deliberately not folded into 'failed': it is not a
 * failure to diagnose but a specific, actionable state, and app launch is
 * where a user first meets it (spec §4.3).
 */
export type EnsureRunningOutcome = 'started' | 'already-running' | 'not-installed' | 'external' | 'failed' | 'path-unresolved'

/** `pdx path <sub>`'s own exit status and streams, passed through untouched. */
export interface LocalDaemonPathResult { code: number | null; stdout: string; stderr: string }

export interface LocalDaemonResult { url: string; token: string; hash: string; version: string; hostname: string; bindNote?: string }

export interface WriteHandle { write(chunk: Uint8Array): Promise<void>; close(): Promise<void> }

export interface LocalDaemonDeps {
  home: string
  hostname: () => string
  platform: NodeJS.Platform
  arch: string
  shell: string | undefined
  baseEnv: NodeJS.ProcessEnv
  exec: ExecFn
  fetch: (url: string, init: { headers: Record<string, string>; signal: AbortSignal }) => Promise<Response>
  fs: {
    exists(p: string): Promise<boolean>
    readFile(p: string): Promise<string>
    writeFile(p: string, data: string, mode: number): Promise<void>
    rename(a: string, b: string): Promise<void>
    unlink(p: string): Promise<void>
    mkdir(p: string): Promise<void> // recursive
    chmod(p: string, mode: number): Promise<void>
    realpath(p: string): Promise<string>
    openWrite(p: string): Promise<WriteHandle>
    sha256(p: string): Promise<string>
  }
  kill0: (pid: number) => boolean
  // One TCP connect attempt to host:port. 'open' = connect succeeded;
  // 'refused' = ECONNREFUSED (nothing listens — the only answer that proves
  // the port is free); 'unknown' = timed out or any other socket error
  // (EHOSTUNREACH, ENETDOWN, …) — the caller must not treat it as free.
  probePort: (host: string, port: number, timeoutMs: number) => Promise<'open' | 'refused' | 'unknown'>
  networkInterfaces: () => Iface[]
  randomBytes: (n: number) => Buffer
  sleep: (ms: number) => Promise<void>
  now: () => number
  log: (msg: string) => void
}

export interface LocalDaemon {
  status(): Promise<LocalDaemonStatus>
  install(daemonUrl: string, token: string | undefined, onProgress: (step: string) => void): Promise<LocalDaemonResult>
  start(): Promise<LocalDaemonResult>
  restart(): Promise<LocalDaemonResult>
  ensureRunning(): Promise<EnsureRunningOutcome>
  /** Run `pdx path link` / `pdx path add-to-shell` and return its own output. */
  pathCommand(kind: 'link' | 'add-to-shell', opts?: { force?: boolean }): Promise<LocalDaemonPathResult>
  withLock<T>(fn: () => Promise<T>): Promise<T>
}

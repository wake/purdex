// Types for the app-managed local pdx daemon (spec §3.1). Interfaces only —
// the factory `createLocalDaemon` lives in ./index.ts.
import type { ExecFn } from './launch-env'
import type { Iface } from './config'

export interface LocalDaemonStatus {
  managed: 'none' | 'managed' | 'external'
  reason?: string
  binPath: string
  installed: { version: string; hash: string; goos: string; goarch: string } | null
  alive: { pid: number } | null
  running: { version: string; hash: string; url: string } | null
  config: { bind: string; port: number; hasToken: boolean } | null
  target: { goos: 'darwin' | 'linux'; goarch: 'arm64' | 'amd64' }
  tools: { tmux: string | null }
}

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
  ensureRunning(): Promise<'started' | 'already-running' | 'not-installed' | 'external' | 'failed'>
  withLock<T>(fn: () => Promise<T>): Promise<T>
}

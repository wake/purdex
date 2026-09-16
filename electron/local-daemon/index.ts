// The app manages a pdx daemon on the machine it runs on (spec §3.1).
// Every side effect comes through `deps` so the logic is unit-tested with
// an in-memory harness. All public operations run through one promise
// queue; private helpers (suffix `Unlocked`) never enqueue.
import { join } from 'node:path'
import type { EnsureRunningOutcome, LocalDaemon, LocalDaemonDeps, LocalDaemonPathResult, LocalDaemonResult, LocalDaemonStatus } from './types'
import { parseLsofF0, txtPaths, listenersOn, decideOwnership, type Ownership } from './lsof'
import { buildLaunchEnv, type ExecFn, type LaunchEnv } from './launch-env'
import { parseDaemonConfig, pickBindAddress, renderInitialConfig, generateToken, DEFAULT_DATA_DIR, type DaemonConfig } from './config'

// lsof lives in /usr/sbin on macOS and /usr/bin on Linux (`target()` accepts
// both). A wrong path would spawn-fail → OwnershipUnavailable on every call.
const LSOF_BY_PLATFORM: Partial<Record<NodeJS.Platform, string>> = { darwin: '/usr/sbin/lsof', linux: '/usr/bin/lsof' }
const LSOF_TIMEOUT_MS = 5000
const OWNERSHIP_BUDGET_MS = 10_000
const STOP_SETTLE_MS = 5000
const HEALTH_TIMEOUT_MS = 1500
const VERSION_TIMEOUT_MS = 2000
const STOP_TIMEOUT_MS = 35_000
const START_TIMEOUT_MS = 70_000
const DOWNLOAD_TIMEOUT_MS = 6 * 60_000
const PATH_TIMEOUT_MS = 5000
// `add-to-shell` waits up to 5 s on its own lockfile before giving up, so the
// command can legitimately take a few seconds without being stuck.
const PATH_CMD_TIMEOUT_MS = 30_000
// A machine whose `pdx` does not resolve is re-probed on status, because its
// user is running the fix commands in another window and watching the panel
// clear. A healthy machine is never re-probed: `resolveShellPath` runs a login
// shell with two 5 s timeouts, and status is polled by the UI (spec §4.2).
const CLI_REPROBE_INTERVAL_MS = 5000

// Thrown when an ownership-determining lsof call could not be trusted —
// either it timed out or it never ran at all (spawn failure). The caller
// must never fall back to "no processes" in either case (spec §3.1).
class OwnershipUnavailable extends Error {
  constructor(msg?: string) { super(msg ?? 'ownership check timed out') }
}

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

export function createLocalDaemon(deps: LocalDaemonDeps): LocalDaemon {
  const dataDir = DEFAULT_DATA_DIR(deps.home)
  const binDir = join(dataDir, 'bin')
  const binPath = join(binDir, 'pdx')
  const newPath = join(binDir, 'pdx.new')
  const cfgPath = join(dataDir, 'config.toml')
  const lsofPath = LSOF_BY_PLATFORM[deps.platform] ?? '/usr/sbin/lsof'

  // ---- queue -------------------------------------------------------------
  let tail: Promise<unknown> = Promise.resolve()
  function withLock<T>(fn: () => Promise<T>): Promise<T> {
    const run = tail.then(fn, fn)
    tail = run.catch(() => {})
    return run
  }

  // ---- env ---------------------------------------------------------------
  // Two accessors, because "always re-probe" is as wrong as "never" (spec
  // §4.2): the gate decisions refresh, everything else reuses the memoized
  // probe. `lastEnvProbeAt` is the wall clock of the *last actual probe*,
  // the cached one included, so a broken machine re-probes at most once per 5 s.
  let envCache: LaunchEnv | null = null
  let envPromise: Promise<LaunchEnv> | null = null
  let lastEnvProbeAt = Number.NEGATIVE_INFINITY
  // undefined until a status has asked the binary; null means `pdx` did not
  // resolve (or could not be determined), which is what makes status re-probe.
  let lastCliResolved: string | null | undefined

  function probeLaunchEnv(): Promise<LaunchEnv> {
    lastEnvProbeAt = deps.now()
    return buildLaunchEnv({
      exec: deps.exec, shell: deps.shell, baseEnv: deps.baseEnv, home: deps.home,
      sentinel: () => 'PDX_PATH_' + deps.randomBytes(16).toString('hex'),
    })
  }

  function cachedLaunchEnv(): Promise<LaunchEnv> {
    envPromise ??= probeLaunchEnv().then((r) => { envCache = r; return r })
    return envPromise
  }

  // A failed re-probe must never turn a working machine into a refusing one:
  // it keeps the last shell-derived PATH instead of dropping to the fallback.
  async function refreshLaunchEnv(): Promise<LaunchEnv> {
    const fresh = await probeLaunchEnv()
    if (fresh.pathSource !== 'shell' && envCache?.pathSource === 'shell') return envCache
    envCache = fresh
    envPromise = Promise.resolve(fresh)
    return fresh
  }

  // The PATH a status poll judges on. Spec §4.2's table: cache when the last
  // known cli.resolved was non-null, refresh (rate-limited) when it was null.
  function statusLaunchEnv(binExists: boolean): Promise<LaunchEnv> {
    const due = deps.now() - lastEnvProbeAt >= CLI_REPROBE_INTERVAL_MS
    if (binExists && lastCliResolved === null && due) return refreshLaunchEnv()
    return cachedLaunchEnv()
  }

  function cachedEnv(): Promise<NodeJS.ProcessEnv> {
    return cachedLaunchEnv().then((r) => r.env)
  }

  async function readCli(env: NodeJS.ProcessEnv): Promise<CliState> {
    const cli = await readCliState({ exec: deps.exec, exists: deps.fs.exists }, binPath, env)
    // "Could not tell" is recorded as not-resolved: such a machine is broken
    // in some other way and is exactly the one worth re-checking.
    lastCliResolved = cli.kind === 'not-installed' ? undefined : cli.kind === 'report' ? cli.report.resolved : null
    return cli
  }

  // ---- the gate (spec §4.3) ---------------------------------------------
  /**
   * The refusal, or null to proceed. The criterion is `resolved === null` —
   * never the JSON's `ok`, never the exit code. `pdx path` exits 1 and
   * reports ok:false when the winner on PATH is a *different* pdx, which for
   * the CLI's own question is a failure and for the gate's is not: the app's
   * job is that `pdx` works, not that `pdx` is its own copy. Keying on `ok`
   * would refuse to start on every machine with a hand-made symlink to a repo
   * build. An answer we could not read at all is likewise not a refusal: a
   * gate the user has no command to satisfy is worse than one that admits
   * what it does not know.
   */
  function pathRefusal(cli: CliState): string | null {
    if (cli.kind !== 'report' || cli.report.resolved !== null) return null
    return pathRefusalMessage(cli.report, deps.home)
  }

  /** Resolve `pdx` against a freshly probed launch PATH (spec §4.2). */
  async function gateUnlocked(): Promise<{ launch: LaunchEnv; refusal: string | null }> {
    const launch = await refreshLaunchEnv()
    return { launch, refusal: pathRefusal(await readCli(launch.env)) }
  }

  // ---- primitives --------------------------------------------------------
  function target(): LocalDaemonStatus['target'] {
    const goos = deps.platform === 'darwin' ? 'darwin' : deps.platform === 'linux' ? 'linux' : null
    const goarch = deps.arch === 'arm64' ? 'arm64' : deps.arch === 'x64' ? 'amd64' : null
    if (!goos || !goarch) throw new Error(`unsupported platform ${deps.platform}/${deps.arch}`)
    return { goos, goarch }
  }

  async function readConfig(): Promise<DaemonConfig | null> {
    if (!(await deps.fs.exists(cfgPath))) return null
    return parseDaemonConfig(await deps.fs.readFile(cfgPath), deps.home)
  }

  // `version --json` output as an object, or null when it is not a JSON object
  // (parse error, `null`, a number, an array).
  function parseIdentityJson(stdout: string): Record<string, unknown> | null {
    try {
      const j: unknown = JSON.parse(stdout.trim())
      return j !== null && typeof j === 'object' && !Array.isArray(j) ? (j as Record<string, unknown>) : null
    } catch {
      return null
    }
  }

  async function readIdentity(bin: string): Promise<LocalDaemonStatus['installed']> {
    const unknown = { version: 'unknown', hash: 'unknown', goos: 'unknown', goarch: 'unknown' }
    try {
      const r = await deps.exec(bin, ['version', '--json'], { env: await cachedEnv(), timeoutMs: VERSION_TIMEOUT_MS })
      if (r.code !== 0) return unknown
      const j = parseIdentityJson(r.stdout)
      if (!j) return unknown
      const s = (k: string) => (typeof j[k] === 'string' ? (j[k] as string) : 'unknown')
      return { version: s('version'), hash: s('hash'), goos: s('goos'), goarch: s('goarch') }
    } catch {
      return unknown
    }
  }

  async function health(bind: string, port: number): Promise<LocalDaemonStatus['running']> {
    const url = `http://${bind}:${port}`
    const ctl = new AbortController()
    const timer = setTimeout(() => ctl.abort(), HEALTH_TIMEOUT_MS)
    try {
      const r = await deps.fetch(`${url}/api/health`, { headers: {}, signal: ctl.signal })
      if (!r.ok) return null
      const j = (await r.json()) as Record<string, unknown>
      if (j.ok !== true) return null
      const s = (k: string) => (typeof j[k] === 'string' ? (j[k] as string) : 'unknown')
      return { version: s('version'), hash: s('hash'), url }
    } catch {
      return null
    } finally {
      clearTimeout(timer)
    }
  }

  // Every lsof in one ownership pass shares a 10 s budget (5 s per call).
  async function lsof(args: string[], deadline: number): Promise<string> {
    const remaining = deadline - deps.now()
    if (remaining <= 0) throw new OwnershipUnavailable()
    const r = await deps.exec(lsofPath, args, { timeoutMs: Math.min(LSOF_TIMEOUT_MS, remaining) })
    if (r.timedOut) throw new OwnershipUnavailable()
    // A spawn failure (ENOENT, etc.) reports code: null without timing out;
    // that is not the same as lsof legitimately exiting 1 with no matches.
    if (r.code === null) throw new OwnershipUnavailable('ownership check failed: lsof did not run')
    // lsof exits 0 with matches and 1 with none; anything else (bad option,
    // permission problem, …) is a probe failure whose empty stdout must not
    // be read as "no processes".
    if (r.code !== 0 && r.code !== 1) throw new OwnershipUnavailable(`ownership check failed: lsof exited ${r.code}`)
    return r.stdout
  }

  async function readCandidatePid(pidPath: string): Promise<number | null> {
    if (!(await deps.fs.exists(pidPath))) return null
    const raw = (await deps.fs.readFile(pidPath)).trim()
    if (!/^\d+$/.test(raw)) return null // '123junk' is not a pid, not even partially
    const n = Number(raw)
    return Number.isSafeInteger(n) && n > 0 ? n : null
  }

  async function sameBinary(path: string): Promise<boolean> {
    try {
      return (await deps.fs.realpath(path)) === (await deps.fs.realpath(binPath))
    } catch {
      return false
    }
  }

  // Spec §3.1 "Ownership / liveness": resolveOwner.
  async function resolveOwner(cfg: DaemonConfig, binExists: boolean): Promise<Ownership> {
    const deadline = deps.now() + OWNERSHIP_BUDGET_MS
    const candidatePid = await readCandidatePid(join(cfg.dataDir, 'pdx.pid'))
    let candidateIsOurs = false
    if (candidatePid !== null && deps.kill0(candidatePid)) {
      const procs = parseLsofF0(await lsof(['-nP', '-a', '-p', String(candidatePid), '-d', 'txt', '-F0pfn'], deadline))
      for (const p of txtPaths(procs, candidatePid)) {
        if (await sameBinary(p)) { candidateIsOurs = true; break }
      }
    }
    const listenProcs = parseLsofF0(await lsof(['-nP', '-a', `-iTCP:${cfg.port}`, '-sTCP:LISTEN', '-F0pfn'], deadline))
    const listenerPids = listenersOn(listenProcs, cfg.bind, cfg.port)
    const listenerBinaries: Record<number, string | undefined> = {}
    for (const pid of listenerPids) {
      if (candidateIsOurs && pid === candidatePid) continue
      const procs = parseLsofF0(await lsof(['-nP', '-a', '-p', String(pid), '-d', 'txt', '-F0pfn'], deadline))
      // Only name a foreign binary when a txt entry is clearly a pdx
      // executable; the first txt entry can be a dylib, and a wrong path
      // in the reason is worse than the pid alone.
      const pdxLike = txtPaths(procs, pid).find((p) => p.split('/').pop() === 'pdx')
      listenerBinaries[pid] = pdxLike ? await deps.fs.realpath(pdxLike).catch(() => pdxLike) : undefined
    }
    return decideOwnership({ candidatePid, candidateIsOurs, listenerPids, listenerBinaries, binExists })
  }

  async function statusUnlocked(): Promise<LocalDaemonStatus> {
    const tgt = target()
    const binExists = await deps.fs.exists(binPath)
    const launch = await statusLaunchEnv(binExists)
    const env = launch.env
    const cli = await readCli(env)
    const which = await deps.exec('/usr/bin/which', ['tmux'], { env, timeoutMs: VERSION_TIMEOUT_MS }).catch(() => null)
    const tmux = which && which.code === 0 ? which.stdout.trim() || null : null
    const cfgFile = await readConfig()
    const cfg = cfgFile ?? parseDaemonConfig('', deps.home)
    const installed = binExists ? await readIdentity(binPath) : null
    const running = await health(cfg.bind, cfg.port)
    const base = {
      binPath, installed, running, target: tgt, tools: { tmux }, hostname: deps.hostname(),
      config: cfgFile ? { bind: cfgFile.bind, port: cfgFile.port, token: cfgFile.token } : null,
      ...(cli.kind === 'report' ? {
        cli: {
          resolved: cli.report.resolved,
          isManagedBinary: cli.report.isSelf,
          pathSource: launch.pathSource,
          localBinOnPath: cli.report.localBinOnPath,
          link: cli.report.link,
        },
      } : {}),
    }
    if (cfgFile && cfgFile.dataDir !== dataDir) {
      return { ...base, managed: 'external', reason: 'custom data_dir', alive: null }
    }
    let own: Ownership
    try {
      own = await resolveOwner(cfg, binExists)
    } catch (e) {
      if (e instanceof OwnershipUnavailable) return { ...base, managed: 'external', reason: e.message, alive: null }
      throw e
    }
    if (own.managed === 'external') return { ...base, managed: 'external', reason: own.reason, alive: own.alive }
    if (running && own.alive === null) {
      // Something answered /api/health on our endpoint yet lsof found no
      // listener we could attribute — never treat that as installable.
      return { ...base, managed: 'external', reason: 'health answered but no listener found', alive: null }
    }
    return { ...base, managed: own.managed, alive: own.alive }
  }

  // ---- download + verify -------------------------------------------------
  async function download(daemonUrl: string, token: string | undefined, tgt: LocalDaemonStatus['target']): Promise<{ hash: string; version: string }> {
    const ctl = new AbortController()
    const timer = setTimeout(() => ctl.abort(), DOWNLOAD_TIMEOUT_MS)
    try {
      const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {}
      const url = `${daemonUrl}/api/dev/daemon/download?goos=${tgt.goos}&goarch=${tgt.goarch}`
      const resp = await deps.fetch(url, { headers, signal: ctl.signal })
      if (!resp.ok) {
        let msg = `download failed: HTTP ${resp.status}`
        try {
          const j = (await resp.json()) as { error?: string; detail?: string }
          if (j.error) msg = `download failed: ${j.error}${j.detail ? `\n${j.detail}` : ''}`
        } catch { /* non-JSON body */ }
        throw new Error(msg)
      }
      // Only a full 200 carries the binary; 204/206 etc. are 2xx yet not a download.
      if (resp.status !== 200) throw new Error(`download failed: unexpected status ${resp.status}`)
      const expectLen = Number(resp.headers.get('content-length'))
      const expectSha = resp.headers.get('x-pdx-sha256') ?? ''
      const hash = resp.headers.get('x-pdx-hash') ?? ''
      const version = resp.headers.get('x-pdx-version') ?? 'unknown'
      if (!Number.isFinite(expectLen) || expectLen <= 0 || !/^[0-9a-f]{64}$/.test(expectSha) || hash === '') {
        throw new Error('download failed: missing integrity header (Content-Length, X-Pdx-Sha256, X-Pdx-Hash are required)')
      }
      const out = await deps.fs.openWrite(newPath)
      let written = 0
      try {
        try {
          if (!resp.body) throw new Error('download failed: empty body')
          const reader = resp.body.getReader()
          for (;;) {
            const { done, value } = await reader.read()
            if (done) break
            await out.write(value)
            written += value.byteLength
          }
        } finally {
          await out.close()
        }
        if (written !== expectLen) {
          throw new Error(`download failed: content-length ${expectLen}, received ${written}`)
        }
        if ((await deps.fs.sha256(newPath)) !== expectSha) {
          throw new Error('download failed: sha256 mismatch')
        }
        await deps.fs.chmod(newPath, 0o755)
        return { hash, version }
      } catch (e) {
        // Any failure past this point (stream error/abort, length or hash
        // mismatch) must never leave a partial/wrong pdx.new behind.
        await deps.fs.unlink(newPath).catch(() => {})
        throw e
      }
    } finally {
      clearTimeout(timer)
    }
  }

  async function verifyNew(tgt: LocalDaemonStatus['target'], expectedHash: string): Promise<void> {
    const r = await deps.exec(newPath, ['version', '--json'], { env: await cachedEnv(), timeoutMs: VERSION_TIMEOUT_MS }).catch((e: Error) => ({ code: 1, stdout: '', stderr: e.message, timedOut: false }))
    if (r.code !== 0) {
      await deps.fs.unlink(newPath)
      throw new Error(`downloaded binary does not run: ${r.stderr.trim() || `exit ${r.code}`}`)
    }
    const id = parseIdentityJson(r.stdout)
    if (!id) {
      await deps.fs.unlink(newPath)
      throw new Error(`downloaded binary does not run: \`version --json\` printed no identity object: ${r.stdout.trim().slice(0, 80) || '(empty)'}`)
    }
    if (id.goos !== tgt.goos || id.goarch !== tgt.goarch || id.hash !== expectedHash) {
      await deps.fs.unlink(newPath)
      throw new Error(`identity mismatch: got ${id.goos}/${id.goarch} ${id.hash}, want ${tgt.goos}/${tgt.goarch} ${expectedHash}`)
    }
  }

  // ---- stop / start ------------------------------------------------------
  // After `pdx stop` returns, wait (≤ 5 s total, probes included) until the
  // pid is gone AND the port is *known* to refuse TCP connections. A plain
  // health probe cannot tell "refused" from "500/timeout", hence the
  // dedicated probePort dep; its 'unknown' (timeout / other socket error)
  // keeps us polling exactly like 'open' does — only 'refused' proves free.
  async function stopUnlocked(cfg: DaemonConfig, pid: number): Promise<void> {
    const r = await deps.exec(binPath, ['stop'], { env: await cachedEnv(), cwd: deps.home, timeoutMs: STOP_TIMEOUT_MS })
    if (r.timedOut) throw new Error('pdx stop did not finish within 35s — the old binary was not replaced (the old process may or may not still be running)')
    const deadline = deps.now() + STOP_SETTLE_MS
    for (;;) {
      let remaining = deadline - deps.now()
      if (remaining <= 0) break
      const gone = !deps.kill0(pid) && (await deps.probePort(cfg.bind, cfg.port, Math.min(500, remaining))) === 'refused'
      if (gone) return
      remaining = deadline - deps.now()
      if (remaining <= 0) break
      await deps.sleep(Math.min(500, remaining))
    }
    throw new Error('pdx stop returned but the daemon is still alive or the port is still open — the old binary was not replaced')
  }

  async function startDaemon(cfg: DaemonConfig): Promise<void> {
    const r = await deps.exec(binPath, ['start'], { env: await cachedEnv(), cwd: deps.home, timeoutMs: START_TIMEOUT_MS })
    if (r.timedOut) throw new Error('pdx start did not finish within 70s')
    if (r.code !== 0) throw new Error(`pdx start failed: ${(r.stderr || r.stdout).trim()}`)
    const onDisk = await readIdentity(binPath)
    const h = await health(cfg.bind, cfg.port)
    if (!h) throw new Error('pdx start returned but /api/health is not answering')
    const realHash = (v: string | undefined) => { const t = (v ?? '').trim(); return t !== '' && t !== 'unknown' ? t : null }
    const diskHash = realHash(onDisk?.hash)
    const liveHash = realHash(h.hash)
    if (!diskHash) throw new Error('installed binary reports no build hash; refusing to trust the start')
    if (!liveHash) throw new Error(`port ${cfg.port} answered health with no build hash — not the binary we started`)
    if (liveHash !== diskHash) {
      throw new Error(`port ${cfg.port} is served by something else (health hash ${h.hash}, binary ${diskHash})`)
    }
  }

  async function register(bindNote?: string): Promise<LocalDaemonResult> {
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    const id = await readIdentity(binPath)
    return {
      url: `http://${cfg.bind}:${cfg.port}`, token: cfg.token ?? '', hash: id?.hash ?? 'unknown', version: id?.version ?? 'unknown',
      hostname: deps.hostname(), ...(bindNote ? { bindNote } : {}),
    }
  }

  // A repair command, not a poll: it is run because the user pressed a button
  // on the machine whose PATH is being changed, so it judges on a fresh probe
  // rather than a cache that may predate the fix they just applied.
  async function pathCommandUnlocked(kind: 'link' | 'add-to-shell', opts?: { force?: boolean }): Promise<LocalDaemonPathResult> {
    const { env, pathSource } = await refreshLaunchEnv()
    // Never hand a SYNTHESIZED PATH to a command that decides what to do by
    // looking at PATH. When the shell probe fails, fallbackPath() injects
    // ~/.local/bin — so `add-to-shell` would see the directory it exists to
    // add, call itself a no-op, and leave the rc file untouched on precisely
    // the machine whose real PATH we could not read. The button would report
    // success and change nothing.
    //
    // So on a fallback PATH we pass the process's own inherited PATH: not the
    // user's shell PATH either, but at least something real rather than
    // something this file made up. Writing a block that turns out to have
    // been unnecessary is harmless — the marker makes a second run a no-op —
    // whereas skipping a needed one is the bug this feature exists to fix.
    const cmdEnv = pathSource === 'fallback' ? { ...env, PATH: deps.baseEnv.PATH ?? '' } : env
    const args = ['path', kind, ...(kind === 'link' && opts?.force ? ['--force'] : [])]
    const r = await deps.exec(binPath, args, { env: cmdEnv, cwd: deps.home, timeoutMs: PATH_CMD_TIMEOUT_MS })
    if (r.timedOut) throw new Error(`pdx path ${kind} did not finish within ${PATH_CMD_TIMEOUT_MS / 1000}s`)
    // Verbatim, refusals included: a conflict's whole value is the path it
    // names, and reducing it to a red "failed" would throw that away.
    return { code: r.code, stdout: r.stdout, stderr: r.stderr }
  }

  // ---- public operations -------------------------------------------------
  async function installUnlocked(daemonUrl: string, token: string | undefined, progress: (s: string) => void): Promise<LocalDaemonResult> {
    const st = await statusUnlocked()
    if (st.managed === 'external') throw new Error(`refusing to install: external daemon (${st.reason})`)
    const tgt = st.target
    progress('prepare')
    await deps.fs.mkdir(binDir)
    progress('download')
    const { hash } = await download(daemonUrl, token, tgt)
    progress('verify')
    await verifyNew(tgt, hash)
    let bindNote: string | undefined
    if (!(await deps.fs.exists(cfgPath))) {
      progress('configure')
      const pick = pickBindAddress(deps.networkInterfaces(), deps.platform)
      bindNote = pick.note
      const tmp = cfgPath + '.tmp'
      // writeFile applies `mode` only when it creates the file; a leftover
      // .tmp from an interrupted run would keep its old (possibly 0644) mode
      // and carry the token world-readable through the rename.
      await deps.fs.unlink(tmp).catch(() => {})
      await deps.fs.writeFile(tmp, renderInitialConfig(pick.bind, generateToken(deps.randomBytes)), 0o600)
      await deps.fs.rename(tmp, cfgPath)
    }
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    // Re-resolve ownership immediately before the destructive step.
    const own = await resolveOwner(cfg, await deps.fs.exists(binPath))
    if (own.managed === 'external') throw new Error(`refusing to stop: external daemon (${own.reason})`)
    if (own.alive) {
      progress('stop')
      await stopUnlocked(cfg, own.alive.pid)
    }
    progress('swap')
    await deps.fs.rename(newPath, binPath)
    // The binary is what the user asked for and is now correctly installed;
    // only the start is refused. statusUnlocked reads installed-and-stopped.
    const refusal = (await gateUnlocked()).refusal
    if (refusal) throw new Error(refusal)
    progress('start')
    await startDaemon(cfg)
    progress('register')
    return register(bindNote)
  }

  async function startUnlocked(): Promise<LocalDaemonResult> {
    const refusal = (await gateUnlocked()).refusal
    if (refusal) throw new Error(refusal)
    const st = await statusUnlocked()
    if (st.managed !== 'managed') throw new Error(`cannot start: ${st.managed}${st.reason ? ` (${st.reason})` : ''}`)
    if (st.alive) throw new Error(`already running (pid ${st.alive.pid})`)
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    await startDaemon(cfg)
    return register()
  }

  async function restartUnlocked(): Promise<LocalDaemonResult> {
    const refusal = (await gateUnlocked()).refusal
    if (refusal) throw new Error(refusal)
    const st = await statusUnlocked()
    if (st.managed !== 'managed') throw new Error(`cannot restart: ${st.managed}${st.reason ? ` (${st.reason})` : ''}`)
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    const own = await resolveOwner(cfg, true)
    if (own.managed === 'external') throw new Error(`refusing to stop: external daemon (${own.reason})`)
    if (own.alive) await stopUnlocked(cfg, own.alive.pid)
    await startDaemon(cfg)
    return register()
  }

  // Never rejects (spec §3.1): every failure, including a broken config,
  // is logged and reported as 'failed'.
  async function ensureRunningUnlocked(): Promise<EnsureRunningOutcome> {
    try {
      const refusal = (await gateUnlocked()).refusal
      if (refusal) {
        deps.log(`[local-daemon] ${refusal}`)
        return 'path-unresolved'
      }
      const st = await statusUnlocked()
      if (st.managed === 'none') return 'not-installed'
      if (st.managed === 'external') return 'external'
      if (st.alive) return 'already-running'
      const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
      for (let attempt = 1; attempt <= 3; attempt++) {
        try {
          await startDaemon(cfg)
          return 'started'
        } catch (e) {
          deps.log(`[local-daemon] start attempt ${attempt} failed: ${e instanceof Error ? e.message : String(e)}`)
          if (attempt < 3) await deps.sleep(5000)
        }
      }
      return 'failed'
    } catch (e) {
      deps.log(`[local-daemon] ensureRunning: ${e instanceof Error ? e.message : String(e)}`)
      return 'failed'
    }
  }

  return {
    status: () => withLock(statusUnlocked),
    install: (u, t, p) => withLock(() => installUnlocked(u, t, p)),
    start: () => withLock(startUnlocked),
    restart: () => withLock(restartUnlocked),
    ensureRunning: () => withLock(ensureRunningUnlocked),
    pathCommand: (kind, opts) => withLock(() => pathCommandUnlocked(kind, opts)),
    withLock,
  }
}

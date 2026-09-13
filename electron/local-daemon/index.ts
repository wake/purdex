// The app manages a pdx daemon on the machine it runs on (spec §3.1).
// Every side effect comes through `deps` so the logic is unit-tested with
// an in-memory harness. All public operations run through one promise
// queue; private helpers (suffix `Unlocked`) never enqueue.
import { join } from 'node:path'
import type { LocalDaemon, LocalDaemonDeps, LocalDaemonResult, LocalDaemonStatus } from './types'
import { parseLsofF0, txtPaths, listenersOn, decideOwnership, type Ownership } from './lsof'
import { buildLaunchEnv } from './launch-env'
import { parseDaemonConfig, pickBindAddress, renderInitialConfig, generateToken, DEFAULT_DATA_DIR, type DaemonConfig } from './config'

const LSOF = '/usr/sbin/lsof'
const LSOF_TIMEOUT_MS = 5000
const OWNERSHIP_BUDGET_MS = 10_000
const STOP_SETTLE_MS = 5000
const HEALTH_TIMEOUT_MS = 1500
const VERSION_TIMEOUT_MS = 2000
const STOP_TIMEOUT_MS = 35_000
const START_TIMEOUT_MS = 70_000
const DOWNLOAD_TIMEOUT_MS = 6 * 60_000

class OwnershipTimeout extends Error {}

export function createLocalDaemon(deps: LocalDaemonDeps): LocalDaemon {
  const dataDir = DEFAULT_DATA_DIR(deps.home)
  const binDir = join(dataDir, 'bin')
  const binPath = join(binDir, 'pdx')
  const newPath = join(binDir, 'pdx.new')
  const cfgPath = join(dataDir, 'config.toml')

  // ---- queue -------------------------------------------------------------
  let tail: Promise<unknown> = Promise.resolve()
  function withLock<T>(fn: () => Promise<T>): Promise<T> {
    const run = tail.then(fn, fn)
    tail = run.catch(() => {})
    return run
  }

  // ---- env ---------------------------------------------------------------
  let envPromise: Promise<NodeJS.ProcessEnv> | null = null
  function launchEnv(): Promise<NodeJS.ProcessEnv> {
    envPromise ??= buildLaunchEnv({
      exec: deps.exec, shell: deps.shell, baseEnv: deps.baseEnv, home: deps.home,
      sentinel: () => 'PDX_PATH_' + deps.randomBytes(16).toString('hex'),
    })
    return envPromise
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

  async function readIdentity(bin: string): Promise<LocalDaemonStatus['installed']> {
    const unknown = { version: 'unknown', hash: 'unknown', goos: 'unknown', goarch: 'unknown' }
    try {
      const r = await deps.exec(bin, ['version', '--json'], { env: await launchEnv(), timeoutMs: VERSION_TIMEOUT_MS })
      if (r.code !== 0) return unknown
      const j = JSON.parse(r.stdout.trim()) as Record<string, unknown>
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
    if (remaining <= 0) throw new OwnershipTimeout()
    const r = await deps.exec(LSOF, args, { timeoutMs: Math.min(LSOF_TIMEOUT_MS, remaining) })
    if (r.timedOut) throw new OwnershipTimeout()
    return r.stdout // lsof exits 1 when nothing matched; empty output is fine
  }

  async function readCandidatePid(pidPath: string): Promise<number | null> {
    if (!(await deps.fs.exists(pidPath))) return null
    const n = Number.parseInt((await deps.fs.readFile(pidPath)).trim(), 10)
    return Number.isFinite(n) && n > 0 ? n : null
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
    const env = await launchEnv()
    const which = await deps.exec('/usr/bin/which', ['tmux'], { env, timeoutMs: VERSION_TIMEOUT_MS }).catch(() => null)
    const tmux = which && which.code === 0 ? which.stdout.trim() || null : null
    const cfgFile = await readConfig()
    const cfg = cfgFile ?? parseDaemonConfig('', deps.home)
    const binExists = await deps.fs.exists(binPath)
    const installed = binExists ? await readIdentity(binPath) : null
    const running = await health(cfg.bind, cfg.port)
    const base = {
      binPath, installed, running, target: tgt, tools: { tmux },
      config: cfgFile ? { bind: cfgFile.bind, port: cfgFile.port, hasToken: !!cfgFile.token } : null,
    }
    if (cfgFile && cfgFile.dataDir !== dataDir) {
      return { ...base, managed: 'external', reason: 'custom data_dir', alive: null }
    }
    let own: Ownership
    try {
      own = await resolveOwner(cfg, binExists)
    } catch (e) {
      if (e instanceof OwnershipTimeout) return { ...base, managed: 'external', reason: 'ownership check timed out', alive: null }
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

  // install/start/restart/ensureRunning are added in Task 6.
  const notYet = async (): Promise<never> => { throw new Error('not implemented') }

  return {
    status: () => withLock(statusUnlocked),
    install: notYet,
    start: notYet,
    restart: notYet,
    ensureRunning: notYet as unknown as LocalDaemon['ensureRunning'],
    withLock,
  }
}

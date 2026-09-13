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

// Thrown when an ownership-determining lsof call could not be trusted —
// either it timed out or it never ran at all (spawn failure). The caller
// must never fall back to "no processes" in either case (spec §3.1).
class OwnershipUnavailable extends Error {
  constructor(msg?: string) { super(msg ?? 'ownership check timed out') }
}

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
    if (remaining <= 0) throw new OwnershipUnavailable()
    const r = await deps.exec(LSOF, args, { timeoutMs: Math.min(LSOF_TIMEOUT_MS, remaining) })
    if (r.timedOut) throw new OwnershipUnavailable()
    // A spawn failure (ENOENT, etc.) reports code: null without timing out;
    // that is not the same as lsof legitimately exiting 1 with no matches.
    if (r.code === null) throw new OwnershipUnavailable('ownership check failed: lsof did not run')
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
    const r = await deps.exec(newPath, ['version', '--json'], { env: await launchEnv(), timeoutMs: VERSION_TIMEOUT_MS }).catch((e: Error) => ({ code: 1, stdout: '', stderr: e.message, timedOut: false }))
    if (r.code !== 0) {
      await deps.fs.unlink(newPath)
      throw new Error(`downloaded binary does not run: ${r.stderr.trim() || `exit ${r.code}`}`)
    }
    let id: { goos?: string; goarch?: string; hash?: string } = {}
    try { id = JSON.parse(r.stdout.trim()) } catch { /* handled below */ }
    if (id.goos !== tgt.goos || id.goarch !== tgt.goarch || id.hash !== expectedHash) {
      await deps.fs.unlink(newPath)
      throw new Error(`identity mismatch: got ${id.goos}/${id.goarch} ${id.hash}, want ${tgt.goos}/${tgt.goarch} ${expectedHash}`)
    }
  }

  // ---- stop / start ------------------------------------------------------
  // After `pdx stop` returns, wait (≤ 5 s total, probes included) until the
  // pid is gone AND the port refuses TCP connections. A plain health probe
  // cannot tell "refused" from "500/timeout", hence the dedicated portOpen dep.
  async function stopUnlocked(cfg: DaemonConfig, pid: number): Promise<void> {
    const r = await deps.exec(binPath, ['stop'], { env: await launchEnv(), cwd: deps.home, timeoutMs: STOP_TIMEOUT_MS })
    if (r.timedOut) throw new Error('pdx stop did not finish within 35s — the old binary was not replaced (the old process may or may not still be running)')
    const deadline = deps.now() + STOP_SETTLE_MS
    for (;;) {
      let remaining = deadline - deps.now()
      if (remaining <= 0) break
      const gone = !deps.kill0(pid) && !(await deps.portOpen(cfg.bind, cfg.port, Math.min(500, remaining)))
      if (gone) return
      remaining = deadline - deps.now()
      if (remaining <= 0) break
      await deps.sleep(Math.min(500, remaining))
    }
    throw new Error('pdx stop returned but the daemon is still alive or the port is still open — the old binary was not replaced')
  }

  async function startDaemon(cfg: DaemonConfig): Promise<void> {
    const r = await deps.exec(binPath, ['start'], { env: await launchEnv(), cwd: deps.home, timeoutMs: START_TIMEOUT_MS })
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
    progress('start')
    await startDaemon(cfg)
    progress('register')
    return register(bindNote)
  }

  async function startUnlocked(): Promise<LocalDaemonResult> {
    const st = await statusUnlocked()
    if (st.managed !== 'managed') throw new Error(`cannot start: ${st.managed}${st.reason ? ` (${st.reason})` : ''}`)
    if (st.alive) throw new Error(`already running (pid ${st.alive.pid})`)
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    await startDaemon(cfg)
    return register()
  }

  async function restartUnlocked(): Promise<LocalDaemonResult> {
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
  async function ensureRunningUnlocked(): Promise<'started' | 'already-running' | 'not-installed' | 'external' | 'failed'> {
    try {
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
    withLock,
  }
}

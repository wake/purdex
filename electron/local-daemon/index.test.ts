import { createHash } from 'node:crypto'
import { describe, expect, it, beforeEach } from 'vitest'
import { createLocalDaemon, readCliState } from './index'
import type { LocalDaemonDeps, WriteHandle } from './types'
import type { ExecFn, ExecResult } from './launch-env'

const NUL = '\0'
const HOME = '/Users/t'
const BIN = `${HOME}/.config/pdx/bin/pdx`
const CFG = `${HOME}/.config/pdx/config.toml`
const PID = `${HOME}/.config/pdx/pdx.pid`
// Matches deps.randomBytes below (0xcd × 16 → 'cd' × 16).
const S = 'PDX_PATH_' + 'cd'.repeat(16)

interface Fake {
  deps: LocalDaemonDeps
  files: Map<string, string | Uint8Array>
  modes: Map<string, number>
  dirs: string[]
  execLog: Array<{ file: string; args: string[]; env?: NodeJS.ProcessEnv }>
  health: null | { ok: boolean; hash?: string; version?: string }
  lsofTxt: Record<number, string>      // pid → lsof -d txt output
  lsofListen: string                   // lsof -iTCP output
  alivePids: Set<number>
  portProbe: 'open' | 'refused' | 'unknown' // scripted probePort result
  shellPath: string                    // what the login-shell probe answers
  // What `pdx path --json` answers for the PATH it was launched with.
  pathReport: (pathVar: string) => Record<string, unknown>
  onExec: (file: string, args: string[]) => ExecResult | undefined
  downloads: Array<{ status: number; headers: Record<string, string>; body: Uint8Array }>
  clock: number
}

// The Go `pdx path --json` report (cmd/pdx/path.go `pathReport`), computed
// from the PATH the binary was launched with — exactly what the real command
// does, so the fixture and the gate agree on what "reachable" means.
function defaultPathReport(pathVar: string): Record<string, unknown> {
  const localBin = `${HOME}/.local/bin`
  const onPath = pathVar.split(':').includes(localBin)
  const resolved = onPath ? `${localBin}/pdx` : null
  return {
    self: BIN, resolved, resolvedReal: BIN, isSelf: onPath,
    localBin, localBinExists: true, localBinOnPath: onPath,
    link: onPath ? 'ok' : 'missing',
    fixes: onPath ? [] : ['link', 'add-to-shell'],
    ok: onPath,
  }
}

function makeFake(overrides: Partial<Fake> = {}): Fake {
  const fake: Fake = {
    files: new Map(),
    modes: new Map(),
    dirs: [],
    execLog: [],
    health: null,
    lsofTxt: {},
    lsofListen: '',
    alivePids: new Set(),
    portProbe: 'refused',
    shellPath: `/opt/homebrew/bin:/usr/bin:${HOME}/.local/bin`,
    pathReport: defaultPathReport,
    onExec: () => undefined,
    downloads: [],
    clock: 0,
    deps: undefined as unknown as LocalDaemonDeps,
    ...overrides,
  }
  const text = (p: string) => {
    const v = fake.files.get(p)
    if (v === undefined) throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    return typeof v === 'string' ? v : Buffer.from(v).toString('utf8')
  }
  fake.deps = {
    home: HOME,
    hostname: () => 'air-2026',
    platform: 'darwin',
    arch: 'arm64',
    shell: '/bin/zsh',
    baseEnv: { HOME, PATH: '/usr/bin:/bin' },
    exec: async (file, args, opts) => {
      fake.execLog.push({ file, args, env: opts.env })
      const custom = fake.onExec(file, args)
      if (custom) return custom
      if (file === '/bin/zsh') return { code: 0, stdout: `${S}${fake.shellPath}${S}\n`, stderr: '', timedOut: false }
      if (file === '/usr/bin/which') return { code: 0, stdout: '/opt/homebrew/bin/tmux\n', stderr: '', timedOut: false }
      if (file === '/usr/sbin/lsof' || file === '/usr/bin/lsof') {
        if (args.includes('-d')) {
          const pid = Number(args[args.indexOf('-p') + 1])
          return { code: 0, stdout: fake.lsofTxt[pid] ?? '', stderr: '', timedOut: false }
        }
        return { code: 0, stdout: fake.lsofListen, stderr: '', timedOut: false }
      }
      if (args[0] === 'path' && args[1] === '--json') {
        if (!fake.files.has(file)) return { code: 127, stdout: '', stderr: 'not found', timedOut: false }
        const rep = fake.pathReport(opts.env?.PATH ?? '')
        return { code: rep.ok === true ? 0 : 1, stdout: JSON.stringify(rep) + '\n', stderr: '', timedOut: false }
      }
      if (args[0] === 'version') {
        if (!fake.files.has(file)) return { code: 127, stdout: '', stderr: 'not found', timedOut: false }
        const body = text(file)  // fake binaries are JSON identity strings
        return { code: 0, stdout: body + '\n', stderr: '', timedOut: false }
      }
      if (args[0] === 'start') {
        // A real `pdx start` returns only after /api/health answers, with the
        // daemon holding the pid file and listening on bind:port.
        const bindLine = (() => { const c = fake.files.get(CFG); const m = typeof c === 'string' ? /bind = "([^"]+)"/.exec(c) : null; return m ? m[1] : '127.0.0.1' })()
        fake.health = { ok: true, hash: JSON.parse(text(file)).hash, version: '9' }
        fake.alivePids.add(4242); fake.files.set(PID, '4242'); fake.portProbe = 'open'
        fake.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
        fake.lsofListen = `p4242${NUL}\nf8${NUL}n${bindLine}:7860${NUL}\n`
        return { code: 0, stdout: 'started', stderr: '', timedOut: false }
      }
      if (args[0] === 'stop') { fake.health = null; fake.alivePids.clear(); fake.lsofListen = ''; fake.portProbe = 'refused'; return { code: 0, stdout: 'stopped', stderr: '', timedOut: false } }
      return { code: 0, stdout: '', stderr: '', timedOut: false }
    },
    fetch: async (url) => {
      if (url.endsWith('/api/health')) {
        if (!fake.health) throw new Error('ECONNREFUSED')
        return new Response(JSON.stringify(fake.health), { status: 200 })
      }
      // Only the download route consumes the scripted queue.
      const next = fake.downloads.shift()
      if (!next) throw new Error('no scripted download for ' + url)
      // A null-body status (204/205/304) cannot carry a body in the fetch spec.
      const body = [204, 205, 304].includes(next.status) ? null : Buffer.from(next.body)
      return new Response(body, { status: next.status, headers: next.headers })
    },
    fs: {
      exists: async (p) => fake.files.has(p),
      readFile: async (p) => text(p),
      // Like fs.writeFile: `mode` applies only when the file is created; an
      // existing file keeps whatever mode it already had.
      writeFile: async (p, d, mode) => { if (!fake.files.has(p)) fake.modes.set(p, mode); fake.files.set(p, d) },
      rename: async (a, b) => { const v = fake.files.get(a); if (v === undefined) throw new Error('ENOENT'); fake.files.set(b, v); fake.files.delete(a); const m = fake.modes.get(a); if (m !== undefined) { fake.modes.set(b, m); fake.modes.delete(a) } },
      unlink: async (p) => { if (!fake.files.has(p)) throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' }); fake.files.delete(p); fake.modes.delete(p) },
      mkdir: async (p) => { fake.dirs.push(p) },
      chmod: async (p, mode) => { fake.modes.set(p, mode) },
      realpath: async (p) => (p === `${HOME}/link-to-pdx` ? BIN : p),
      openWrite: async (p): Promise<WriteHandle> => { const chunks: Uint8Array[] = []; return { write: async (c) => { chunks.push(c) }, close: async () => { fake.files.set(p, Buffer.concat(chunks)) } } },
      sha256: async (p) => { const v = fake.files.get(p); return createHash('sha256').update(typeof v === 'string' ? Buffer.from(v) : Buffer.from(v ?? new Uint8Array())).digest('hex') },
    },
    kill0: (pid) => fake.alivePids.has(pid),
    probePort: async (_h, _p, timeoutMs) => { fake.clock += Math.min(timeoutMs, 100); return fake.portProbe },
    networkInterfaces: () => [{ name: 'utun4', address: '100.64.0.9', family: 'IPv4', internal: false }],
    randomBytes: (n) => Buffer.alloc(n, 0xcd),
    sleep: async (ms) => { fake.clock += ms },
    now: () => fake.clock,
    log: () => {},
  }
  return fake
}

const identity = (hash: string) => JSON.stringify({ version: '9', hash, goos: 'darwin', goarch: 'arm64' })

describe('status()', () => {
  let f: Fake
  beforeEach(() => { f = makeFake() })

  it('none when nothing is installed', async () => {
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('none')
    expect(st.installed).toBeNull()
    expect(st.alive).toBeNull()
    expect(st.target).toEqual({ goos: 'darwin', goarch: 'arm64' })
    expect(st.tools.tmux).toBe('/opt/homebrew/bin/tmux')
    expect(st.hostname).toBe('air-2026')
    expect(st.config).toBeNull()
  })

  it('managed + stopped when the binary exists and nothing listens; stale pid is ignored', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_x"\n')
    f.files.set(PID, '777')
    f.alivePids.add(777)
    f.lsofTxt[777] = `p777${NUL}\nftxt${NUL}n/usr/bin/some-other${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('managed')
    expect(st.alive).toBeNull()
    expect(st.installed).toEqual({ version: '9', hash: 'aaa', goos: 'darwin', goarch: 'arm64' })
    expect(st.config).toEqual({ bind: '100.64.0.9', port: 7860, token: 'purdex_x' })
    expect(st.hostname).toBe('air-2026')
  })

  it('managed + alive + running when our pid owns the listener', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\n')
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.lsofListen = `p4242${NUL}\nf8${NUL}n100.64.0.9:7860${NUL}\n`
    f.health = { ok: true, hash: 'aaa', version: '9' }
    const st = await createLocalDaemon(f.deps).status()
    expect(st).toMatchObject({ managed: 'managed', alive: { pid: 4242 }, running: { hash: 'aaa', version: '9', url: 'http://100.64.0.9:7860' } })
  })

  it('managed + alive but not running (no listener yet)', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('managed')
    expect(st.alive).toEqual({ pid: 4242 })
    expect(st.running).toBeNull()
  })

  it('external when a foreign pid serves the endpoint (repo daemon on the Mini)', async () => {
    f.files.set(CFG, 'bind = "100.64.0.2"\n')
    f.lsofListen = `p7520${NUL}\nf8${NUL}n100.64.0.2:7860${NUL}\n`
    f.lsofTxt[7520] = `p7520${NUL}\nftxt${NUL}n/repo/bin/pdx${NUL}\n`
    f.health = { ok: true }
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('running daemon is /repo/bin/pdx')
    expect(st.running?.hash).toBe('unknown')
  })

  it('external when health answers but nothing is found listening (no binary either)', async () => {
    f.files.set(CFG, 'bind = "100.64.0.9"\n')
    f.health = { ok: true, hash: 'zzz' }
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('health answered but no listener found')
  })

  it('ownership compares realpaths (symlinked txt entry still ours)', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${HOME}/link-to-pdx${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.alive).toEqual({ pid: 4242 })
  })

  it('foreign listener whose txt entries are all dylibs is reported by pid, not by a dylib path', async () => {
    f.files.set(CFG, 'bind = "100.64.0.2"\n')
    f.lsofListen = `p7520${NUL}\nf8${NUL}n100.64.0.2:7860${NUL}\n`
    f.lsofTxt[7520] = `p7520${NUL}\nftxt${NUL}n/usr/lib/dyld${NUL}\n`
    f.health = { ok: true }
    const st = await createLocalDaemon(f.deps).status()
    expect(st.reason).toBe('port is served by pid 7520')
  })

  it('external with custom data_dir', async () => {
    f.files.set(CFG, 'data_dir = "/Volumes/X/pdx"\n')
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('custom data_dir')
    expect(st.hostname).toBe('air-2026')
  })

  it('external when lsof times out', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.onExec = (file) => (file === '/usr/sbin/lsof' ? { code: null, stdout: '', stderr: '', timedOut: true } : undefined)
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('ownership check timed out')
  })

  it('external when lsof fails to spawn (code null, not timed out) — never treated as "no processes"', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.onExec = (file) => (file === '/usr/sbin/lsof' ? { code: null, stdout: '', stderr: '', timedOut: false } : undefined)
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toMatch(/lsof did not run/)
  })

  it('maps x64 → amd64 and unknown identity on parse failure', async () => {
    f.deps.arch = 'x64'
    f.files.set(BIN, 'garbage')
    const st = await createLocalDaemon(f.deps).status()
    expect(st.target.goarch).toBe('amd64')
    expect(st.installed).toEqual({ version: 'unknown', hash: 'unknown', goos: 'unknown', goarch: 'unknown' })
  })

  it('`version --json` printing a JSON null reads as unknown identity, not a crash', async () => {
    f.files.set(BIN, 'null')
    const st = await createLocalDaemon(f.deps).status()
    expect(st.installed).toEqual({ version: 'unknown', hash: 'unknown', goos: 'unknown', goarch: 'unknown' })
    expect(st.managed).toBe('managed')
  })

  it('uses /usr/bin/lsof on linux (lsof is not under /usr/sbin there)', async () => {
    f.deps.platform = 'linux'
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.target.goos).toBe('linux')
    const lsofCalls = f.execLog.filter((e) => e.file.endsWith('/lsof'))
    expect(lsofCalls.length).toBeGreaterThan(0)
    expect(lsofCalls.every((e) => e.file === '/usr/bin/lsof')).toBe(true)
    expect(st.alive).toEqual({ pid: 4242 })
  })

  it('lsof exiting 1 with empty output means "no processes" (managed, nothing alive)', async () => {
    f.files.set(BIN, identity('aaa'))
    f.onExec = (file) => (file === '/usr/sbin/lsof' ? { code: 1, stdout: '', stderr: '', timedOut: false } : undefined)
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('managed')
    expect(st.alive).toBeNull()
  })

  it('lsof exiting with any other code is a probe failure → external, never "no processes"', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.onExec = (file) => (file === '/usr/sbin/lsof' ? { code: 2, stdout: '', stderr: 'lsof: unsupported option', timedOut: false } : undefined)
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toMatch(/lsof exited 2/)
  })

  it('a pid file that is not purely digits yields no candidate', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '123junk'); f.alivePids.add(123)
    f.lsofTxt[123] = `p123${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.alive).toBeNull()
    expect(f.execLog.some((e) => e.file.endsWith('/lsof') && e.args.includes('-p'))).toBe(false)
  })

  it('a pid file with surrounding whitespace still parses', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, ' 4242\n'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.alive).toEqual({ pid: 4242 })
  })
})

function scriptedDownload(f: Fake, hash: string, opts: { status?: number; truncate?: boolean; badSha?: boolean; wrongArch?: boolean; bodyHash?: string; dropHeaders?: boolean } = {}) {
  const bodyHash = opts.bodyHash ?? hash
  const body = Buffer.from(opts.wrongArch ? JSON.stringify({ version: '9', hash: bodyHash, goos: 'darwin', goarch: 'amd64' }) : identity(bodyHash))
  const sha = createHash('sha256').update(body).digest('hex')
  const headers: Record<string, string> = opts.dropHeaders ? {} : {
    'content-length': String(body.length + (opts.truncate ? 5 : 0)),
    'x-pdx-hash': hash, 'x-pdx-version': '9',
    'x-pdx-sha256': opts.badSha ? 'deadbeef' : sha,
  }
  f.downloads.push({ status: opts.status ?? 200, headers, body })
}

describe('install()', () => {
  let f: Fake
  beforeEach(() => { f = makeFake() })

  it('fresh machine: mkdir, download, verify, configure, start, register', async () => {
    scriptedDownload(f, 'bbb')
    const steps: string[] = []
    const res = await createLocalDaemon(f.deps).install('http://100.64.0.2:7860', 'tok', (s) => steps.push(s))
    expect(steps).toEqual(['prepare', 'download', 'verify', 'configure', 'swap', 'start', 'register'])
    expect(f.dirs).toContain(`${HOME}/.config/pdx/bin`)
    expect(f.files.get(CFG)).toBe('bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_' + 'cd'.repeat(20) + '"\n\n[dev]\nupdate = false\n')
    expect(f.modes.get(CFG)).toBe(0o600)
    expect(f.modes.get(BIN)).toBe(0o755)
    expect(f.files.has(BIN)).toBe(true)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
    expect(res).toEqual({ url: 'http://100.64.0.9:7860', token: 'purdex_' + 'cd'.repeat(20), hash: 'bbb', version: '9', hostname: 'air-2026' })
    const start = f.execLog.find((e) => e.args[0] === 'start')!
    expect(start.file).toBe(BIN)
    expect(start.env?.PDX_DEV_MODE).toBe('1')
    expect(start.env?.PATH).toBe(`/opt/homebrew/bin:/usr/bin:${HOME}/.local/bin`)
    const dl = f.execLog.findIndex((e) => e.args[0] === 'version' && e.file === `${BIN}.new`)
    expect(dl).toBeGreaterThan(-1)
  })

  it('bindNote when no tailscale interface', async () => {
    f.deps.networkInterfaces = () => []
    scriptedDownload(f, 'bbb')
    const res = await createLocalDaemon(f.deps).install('http://src', 'tok', () => {})
    expect(res.url).toBe('http://127.0.0.1:7860')
    expect(res.bindNote).toMatch(/no tailscale/i)
  })

  it('refuses when external', async () => {
    f.files.set(CFG, 'data_dir = "/elsewhere"\n')
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/external/)
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(false)
  })

  it('short body vs Content-Length → throws, pdx.new removed, nothing stopped', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    scriptedDownload(f, 'bbb', { truncate: true })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/length/i)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(false)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
  })

  it('sha256 mismatch → throws and cleans up', async () => {
    scriptedDownload(f, 'bbb', { badSha: true })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/sha256/i)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('verify: wrong arch → throws and cleans up', async () => {
    scriptedDownload(f, 'bbb', { wrongArch: true })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/identity mismatch/)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('verify: binary hash ≠ X-Pdx-Hash → throws and cleans up', async () => {
    scriptedDownload(f, 'bbb', { bodyHash: 'ccc' })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/identity mismatch/)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('missing integrity headers → throws before writing anything durable', async () => {
    scriptedDownload(f, 'bbb', { dropHeaders: true })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/missing.*header/i)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('ownership changing between status and stop aborts before stop', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\n')
    scriptedDownload(f, 'bbb')
    // After the download, a foreign daemon appears on our endpoint.
    const origFetch = f.deps.fetch
    f.deps.fetch = async (url, init) => { const r = await origFetch(url, init); if (!url.endsWith('/api/health')) { f.lsofListen = `p9${NUL}\nf8${NUL}n100.64.0.9:7860${NUL}\n` }; return r }
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/refusing to stop/)
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(false)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
  })

  it('pre-stop ownership re-check timing out rejects with a readable message; binary and pdx.new untouched', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\n')
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.lsofListen = `p4242${NUL}\nf8${NUL}n100.64.0.9:7860${NUL}\n`
    f.health = { ok: true, hash: 'aaa', version: '9' }
    scriptedDownload(f, 'bbb')
    // Once the download has happened, the pre-stop lsof re-check times out.
    let downloadDone = false
    const origFetch = f.deps.fetch
    f.deps.fetch = async (url, init) => { const r = await origFetch(url, init); if (!url.endsWith('/api/health')) downloadDone = true; return r }
    f.onExec = (file) => (file === '/usr/sbin/lsof' && downloadDone ? { code: null, stdout: '', stderr: '', timedOut: true } : undefined)
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/ownership check timed out/)
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(false)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
    const newFile = f.files.get(`${BIN}.new`)
    expect(typeof newFile === 'string' ? newFile : Buffer.from(newFile ?? new Uint8Array()).toString('utf8')).toBe(identity('bbb'))
  })

  it('a body stream error mid-download cleans up pdx.new and rejects', async () => {
    const hash = 'bbb'
    const body = Buffer.from(identity(hash))
    const sha = createHash('sha256').update(body).digest('hex')
    const headers = { 'content-length': String(body.length), 'x-pdx-hash': hash, 'x-pdx-version': '9', 'x-pdx-sha256': sha }
    f.deps.fetch = async (url) => {
      if (url.endsWith('/api/health')) throw new Error('ECONNREFUSED')
      const stream = new ReadableStream<Uint8Array>({
        start(controller) { controller.enqueue(body.subarray(0, 2)) },
        pull() { throw new Error('network error mid-read') },
      })
      return new Response(stream, { status: 200, headers })
    }
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/mid-read/)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('two concurrent installs run one after the other', async () => {
    scriptedDownload(f, 'bbb'); scriptedDownload(f, 'bbb')
    const d = createLocalDaemon(f.deps)
    const steps: string[] = []
    await Promise.all([
      d.install('http://src', 'tok', (s) => steps.push('1:' + s)),
      d.install('http://src', 'tok', (s) => steps.push('2:' + s)),
    ])
    expect(steps).toEqual([
      '1:prepare', '1:download', '1:verify', '1:configure', '1:swap', '1:start', '1:register',
      '2:prepare', '2:download', '2:verify', '2:stop', '2:swap', '2:start', '2:register',
    ])
  })

  it('an install arriving during a withLock-wrapped app update waits for it', async () => {
    scriptedDownload(f, 'bbb')
    const d = createLocalDaemon(f.deps)
    const order: string[] = []
    let release!: () => void
    const gate = new Promise<void>((r) => { release = r })
    const upd = d.withLock(async () => { order.push('update-start'); await gate; order.push('update-end') })
    const inst = d.install('http://src', 'tok', (s) => { if (s === 'prepare') order.push('install-start') })
    await new Promise((r) => setTimeout(r, 5))
    expect(order).toEqual(['update-start'])
    release()
    await Promise.all([upd, inst])
    expect(order).toEqual(['update-start', 'update-end', 'install-start'])
  })

  it('non-200 surfaces the daemon error body', async () => {
    f.downloads.push({ status: 500, headers: { 'content-type': 'application/json' }, body: Buffer.from(JSON.stringify({ error: 'build failed', detail: 'boom' })) })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/build failed.*boom/s)
  })

  it('update: stops our alive daemon, swaps, starts; keeps existing config and token', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_old"\n\n[dev]\nupdate = false\n')
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.lsofListen = `p4242${NUL}\nf8${NUL}n100.64.0.9:7860${NUL}\n`
    f.health = { ok: true, hash: 'aaa', version: '9' }
    scriptedDownload(f, 'bbb')
    const steps: string[] = []
    const res = await createLocalDaemon(f.deps).install('http://src', 'tok', (s) => steps.push(s))
    expect(steps).toEqual(['prepare', 'download', 'verify', 'stop', 'swap', 'start', 'register'])
    expect(res.token).toBe('purdex_old')
    expect(res.hash).toBe('bbb')
    const order = f.execLog.filter((e) => ['stop', 'start'].includes(e.args[0])).map((e) => e.args[0])
    expect(order).toEqual(['stop', 'start'])
  })

  it('alive but unhealthy daemon is still stopped before swap', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    scriptedDownload(f, 'bbb')
    await createLocalDaemon(f.deps).install('http://src', 'tok', () => {})
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(true)
  })

  it('pdx stop timing out aborts before swap', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.onExec = (_file, args) => (args[0] === 'stop' ? { code: null, stdout: '', stderr: '', timedOut: true } : undefined)
    scriptedDownload(f, 'bbb')
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/stop/)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
  })

  it('post-start health hash mismatch → throws "served by something else"', async () => {
    scriptedDownload(f, 'bbb')
    f.onExec = (_file, args) => { if (args[0] === 'start') { f.health = { ok: true, hash: 'zzz' }; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/served by something else/)
  })

  it('post-start health without a hash is a failure, not a pass', async () => {
    scriptedDownload(f, 'bbb')
    f.onExec = (_file, args) => { if (args[0] === 'start') { f.health = { ok: true }; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/no build hash/)
  })

  it('empty or blank hashes on both sides are rejected, never "equal"', async () => {
    for (const blank of ['', '   ']) {
      const g = makeFake()
      // A binary whose identity carries a blank hash, served with a matching blank X-Pdx-Hash.
      const body = Buffer.from(JSON.stringify({ version: '9', hash: blank, goos: 'darwin', goarch: 'arm64' }))
      const sha = createHash('sha256').update(body).digest('hex')
      g.downloads.push({ status: 200, headers: { 'content-length': String(body.length), 'x-pdx-hash': blank, 'x-pdx-version': '9', 'x-pdx-sha256': sha }, body })
      await expect(createLocalDaemon(g.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/missing integrity header|no build hash/)
    }
  })

  it('stop returning while the port stays open → throws within 5 s wall time, old binary unreplaced', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.onExec = (_file, args) => { if (args[0] === 'stop') { f.alivePids.clear(); f.portProbe = 'open'; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    f.portProbe = 'open'
    scriptedDownload(f, 'bbb')
    const t0 = f.clock
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/old binary was not replaced/)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
    // Probe time (fake: 100 ms per probe) plus sleeps must fit the 5 s settle budget.
    expect(f.clock - t0).toBeLessThanOrEqual(5000)
  })

  it('a slow port probe cannot push the settle wait past its budget', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.onExec = (_file, args) => { if (args[0] === 'stop') { f.alivePids.clear(); f.portProbe = 'open'; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    f.portProbe = 'open'
    const timeouts: number[] = []
    f.deps.probePort = async (_h, _p, timeoutMs) => { timeouts.push(timeoutMs); f.clock += timeoutMs; return 'open' }
    scriptedDownload(f, 'bbb')
    const t0 = f.clock
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/old binary was not replaced/)
    expect(f.clock - t0).toBeLessThanOrEqual(5000)
    expect(Math.max(...timeouts)).toBeLessThanOrEqual(500)
  })

  it('settle: probe answering "unknown" forever → rejects within the 5 s budget, old binary unreplaced', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.onExec = (_file, args) => { if (args[0] === 'stop') { f.alivePids.clear(); f.portProbe = 'unknown'; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    scriptedDownload(f, 'bbb')
    const t0 = f.clock
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/old binary was not replaced/)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
    expect(f.clock - t0).toBeLessThanOrEqual(5000)
  })

  it('settle: probe "unknown" twice then "refused" → proceeds to swap', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    const script: Array<'open' | 'refused' | 'unknown'> = ['unknown', 'unknown', 'refused']
    const seen: string[] = []
    f.deps.probePort = async (_h, _p, timeoutMs) => { f.clock += Math.min(timeoutMs, 100); const r = script.shift() ?? 'refused'; seen.push(r); return r }
    scriptedDownload(f, 'bbb')
    const steps: string[] = []
    await createLocalDaemon(f.deps).install('http://src', 'tok', (s) => steps.push(s))
    expect(seen).toEqual(['unknown', 'unknown', 'refused'])
    expect(steps).toEqual(['prepare', 'download', 'verify', 'configure', 'stop', 'swap', 'start', 'register'])
    expect(Buffer.from(f.files.get(BIN) as Uint8Array).toString('utf8')).toBe(identity('bbb'))
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('pre-stop lsof exiting 2 rejects; stop never runs and the binary is not swapped', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\n')
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.lsofListen = `p4242${NUL}\nf8${NUL}n100.64.0.9:7860${NUL}\n`
    f.health = { ok: true, hash: 'aaa', version: '9' }
    scriptedDownload(f, 'bbb')
    let downloadDone = false
    const origFetch = f.deps.fetch
    f.deps.fetch = async (url, init) => { const r = await origFetch(url, init); if (!url.endsWith('/api/health')) downloadDone = true; return r }
    f.onExec = (file) => (file === '/usr/sbin/lsof' && downloadDone ? { code: 2, stdout: '', stderr: '', timedOut: false } : undefined)
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/lsof exited 2/)
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(false)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
  })

  it('a leftover world-readable config.toml.tmp does not leak the token: config ends up 0600', async () => {
    f.files.set(`${CFG}.tmp`, 'stale'); f.modes.set(`${CFG}.tmp`, 0o644)
    scriptedDownload(f, 'bbb')
    await createLocalDaemon(f.deps).install('http://src', 'tok', () => {})
    expect(f.modes.get(CFG)).toBe(0o600)
    expect(f.files.get(CFG)).toMatch(/^bind = /)
    expect(f.files.has(`${CFG}.tmp`)).toBe(false)
  })

  it('verify: `version --json` printing null → "does not run", pdx.new removed', async () => {
    scriptedDownload(f, 'bbb')
    f.onExec = (file, args) => (file === `${BIN}.new` && args[0] === 'version' ? { code: 0, stdout: 'null\n', stderr: '', timedOut: false } : undefined)
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/does not run|identity/)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
    expect(f.files.has(BIN)).toBe(false)
  })

  it('a 2xx other than 200 (e.g. 204) is not a download', async () => {
    scriptedDownload(f, 'bbb', { status: 204 })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/unexpected status 204/)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('pdx start failure surfaces its stderr', async () => {
    scriptedDownload(f, 'bbb')
    f.onExec = (_file, args) => (args[0] === 'start' ? { code: 1, stdout: '', stderr: 'pdx: bind: address not available', timedOut: false } : undefined)
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/address not available/)
    expect(f.files.has(BIN)).toBe(true) // swapped; UI offers Start
  })
})

describe('start() / restart() / ensureRunning()', () => {
  let f: Fake
  beforeEach(() => {
    f = makeFake()
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_t"\n')
  })

  it('start refuses when not managed or already alive', async () => {
    f.files.set(PID, '4242'); f.alivePids.add(4242); f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    await expect(createLocalDaemon(f.deps).start()).rejects.toThrow(/already running/)
  })

  it('start returns the registration payload', async () => {
    const res = await createLocalDaemon(f.deps).start()
    expect(res).toMatchObject({ url: 'http://100.64.0.9:7860', token: 'purdex_t', hash: 'aaa', hostname: 'air-2026' })
  })

  it('restart stops then starts without downloading', async () => {
    f.files.set(PID, '4242'); f.alivePids.add(4242); f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    await createLocalDaemon(f.deps).restart()
    expect(f.execLog.filter((e) => ['stop', 'start'].includes(e.args[0])).map((e) => e.args[0])).toEqual(['stop', 'start'])
    expect(f.downloads).toHaveLength(0)
  })

  it('ensureRunning outcomes', async () => {
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('started')
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('already-running')
    f.files.delete(BIN); f.alivePids.clear(); f.files.delete(PID); f.health = null; f.lsofListen = ''; f.portProbe = 'refused'
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('not-installed')
    f.files.set(CFG, 'data_dir = "/x"\n')
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('external')
  })

  it('ensureRunning retries start 3× then reports failed', async () => {
    let n = 0
    f.onExec = (_file, args) => (args[0] === 'start' ? (n++, { code: 1, stdout: '', stderr: 'no', timedOut: false }) : undefined)
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('failed')
    expect(n).toBe(3)
  })

  it('ensureRunning never rejects — a broken config reads as failed', async () => {
    f.files.set(CFG, 'bind = ')
    await expect(createLocalDaemon(f.deps).ensureRunning()).resolves.toBe('failed')
  })

  it('never touches pdx.new', async () => {
    f.files.set(`${BIN}.new`, 'partial')
    await createLocalDaemon(f.deps).ensureRunning()
    expect(f.files.get(`${BIN}.new`)).toBe('partial')
  })
})

describe('withLock', () => {
  it('serialises public calls and callers of withLock', async () => {
    const f = makeFake()
    const order: string[] = []
    const d = createLocalDaemon(f.deps)
    const a = d.withLock(async () => { order.push('a-start'); await new Promise((r) => setTimeout(r, 10)); order.push('a-end') })
    const b = d.status().then(() => order.push('b'))
    await Promise.all([a, b])
    expect(order).toEqual(['a-start', 'a-end', 'b'])
  })
})

describe('launch env cache (spec §4.2)', () => {
  let f: Fake
  beforeEach(() => {
    f = makeFake()
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_t"\n')
  })
  // Each probe round starts with -ilc; a failing round also tries -lc.
  const probes = () => f.execLog.filter((e) => e.file === '/bin/zsh' && e.args[0] === '-ilc').length
  const lastPathEnv = () => f.execLog.filter((e) => e.args[0] === 'path').at(-1)?.env?.PATH

  it('a healthy machine polled ten times runs the login shell once', async () => {
    const d = createLocalDaemon(f.deps)
    for (let i = 0; i < 10; i++) await d.status()
    expect(probes()).toBe(1)
  })

  it('a machine where pdx does not resolve re-probes, but at most once per 5 s', async () => {
    f.shellPath = '/opt/homebrew/bin:/usr/bin'
    const d = createLocalDaemon(f.deps)
    for (let i = 0; i < 10; i++) await d.status()
    expect(probes()).toBe(1)          // the first poll's own probe is already fresh
    f.clock += 5000
    await d.status()
    expect(probes()).toBe(2)
    await d.status()
    expect(probes()).toBe(2)
  })

  it('a successful re-probe replaces the cached PATH', async () => {
    f.shellPath = '/opt/homebrew/bin:/usr/bin'
    const d = createLocalDaemon(f.deps)
    await d.status()
    expect(lastPathEnv()).toBe('/opt/homebrew/bin:/usr/bin')
    f.shellPath = `/opt/homebrew/bin:/usr/bin:${HOME}/.local/bin`
    f.clock += 5000
    await d.status()
    expect(lastPathEnv()).toBe(`/opt/homebrew/bin:/usr/bin:${HOME}/.local/bin`)
  })

  it('a failed re-probe keeps the last good PATH rather than dropping to the fallback', async () => {
    f.shellPath = '/good/bin'
    const d = createLocalDaemon(f.deps)
    await d.status()
    expect(lastPathEnv()).toBe('/good/bin')
    f.onExec = (file) => (file === '/bin/zsh' ? { code: 1, stdout: '', stderr: 'boom', timedOut: false } : undefined)
    f.clock += 5000
    await d.status()
    expect(probes()).toBe(2)
    expect(lastPathEnv()).toBe('/good/bin')
  })

  it('a machine with no binary installed is never re-probed — there is nothing to ask', async () => {
    f.files.delete(BIN)
    const d = createLocalDaemon(f.deps)
    await d.status()
    f.clock += 60_000
    await d.status()
    expect(probes()).toBe(1)
    expect(f.execLog.some((e) => e.args[0] === 'path')).toBe(false)
  })
})

describe('readCliState (spec §4.1)', () => {
  const env: NodeJS.ProcessEnv = { PATH: '/x' }
  const report = {
    self: BIN, resolved: `${HOME}/.local/bin/pdx`, isSelf: true,
    localBin: `${HOME}/.local/bin`, localBinExists: true, localBinOnPath: true,
    link: 'ok', fixes: [], ok: true,
  }
  const cli = (exec: ExecFn, present = true) => readCliState({ exec, exists: async () => present }, BIN, env)

  it('runs the managed binary with `path --json` and the launch env', async () => {
    const calls: Array<{ file: string; args: string[]; env?: NodeJS.ProcessEnv }> = []
    const exec: ExecFn = async (file, args, opts) => {
      calls.push({ file, args, env: opts.env })
      return { code: 0, stdout: JSON.stringify(report), stderr: '', timedOut: false }
    }
    await cli(exec)
    expect(calls).toHaveLength(1)
    expect(calls[0].file).toBe(BIN)
    expect(calls[0].args).toEqual(['path', '--json'])
    expect(calls[0].env).toBe(env)
  })

  it('exit 0 is a parsed report', async () => {
    const exec: ExecFn = async () => ({ code: 0, stdout: JSON.stringify(report) + '\n', stderr: '', timedOut: false })
    expect(await cli(exec)).toEqual({ kind: 'report', code: 0, report })
  })

  it('exit 1 is the normal not-reachable answer, not an error', async () => {
    const off = { ...report, resolved: null, isSelf: false, link: 'missing', fixes: ['link'], ok: false }
    const exec: ExecFn = async () => ({ code: 1, stdout: JSON.stringify(off), stderr: '', timedOut: false })
    expect(await cli(exec)).toEqual({ kind: 'report', code: 1, report: off })
  })

  it('malformed output is its own outcome, never a report', async () => {
    const exec: ExecFn = async () => ({ code: 0, stdout: 'not json', stderr: 'oops', timedOut: false })
    expect(await cli(exec)).toEqual({ kind: 'unparseable', code: 0, stdout: 'not json', stderr: 'oops' })
  })

  it('JSON that is not a pdx path report is unparseable, not a report', async () => {
    const exec: ExecFn = async () => ({ code: 0, stdout: '{"resolved":42}', stderr: '', timedOut: false })
    expect((await cli(exec)).kind).toBe('unparseable')
  })

  it('a thrown exec is its own outcome', async () => {
    const exec: ExecFn = async () => { throw new Error('EACCES') }
    expect(await cli(exec)).toEqual({ kind: 'exec-failed', error: 'EACCES' })
  })

  it('a timeout is an exec failure, not "not reachable"', async () => {
    const exec: ExecFn = async () => ({ code: null, stdout: '', stderr: '', timedOut: true })
    expect((await cli(exec)).kind).toBe('exec-failed')
  })

  it('a spawn failure (code null, no timeout) is an exec failure', async () => {
    const exec: ExecFn = async () => ({ code: null, stdout: '', stderr: 'ENOENT', timedOut: false })
    expect((await cli(exec)).kind).toBe('exec-failed')
  })

  it('an absent binary is its own outcome and nothing is executed', async () => {
    let ran = false
    const exec: ExecFn = async () => { ran = true; return { code: 0, stdout: '', stderr: '', timedOut: false } }
    expect(await cli(exec, false)).toEqual({ kind: 'not-installed' })
    expect(ran).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// The PATH gate (spec §4)
// ---------------------------------------------------------------------------

/** A PATH on which nothing named pdx is an executable file. */
const noPdxAnywhere = () => ({ ...defaultPathReport(''), localBinExists: false })

describe('the PATH gate (spec §4.3)', () => {
  let f: Fake
  beforeEach(() => {
    f = makeFake()
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_t"\n')
    f.shellPath = '/opt/homebrew/bin:/usr/bin'   // no ~/.local/bin
  })

  it('start refuses and never runs `pdx start`', async () => {
    await expect(createLocalDaemon(f.deps).start()).rejects.toThrow(/Refusing to start/)
    expect(f.execLog.some((e) => e.args[0] === 'start')).toBe(false)
  })

  it('restart refuses and never stops the running daemon', async () => {
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    await expect(createLocalDaemon(f.deps).restart()).rejects.toThrow(/Refusing to start/)
    expect(f.execLog.some((e) => ['stop', 'start'].includes(e.args[0]))).toBe(false)
  })

  it('ensureRunning reports path-unresolved — a state, not a failure to diagnose', async () => {
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('path-unresolved')
    expect(f.execLog.some((e) => e.args[0] === 'start')).toBe(false)
  })

  it('the refusal names the binary and both commands (spec §4.5)', async () => {
    const err = await createLocalDaemon(f.deps).start().catch((e: Error) => e.message)
    expect(err).toContain('The daemon binary is installed at ~/.config/pdx/bin/pdx.')
    expect(err).toContain('~/.config/pdx/bin/pdx path link           create ~/.local/bin/pdx')
    expect(err).toContain('~/.config/pdx/bin/pdx path add-to-shell   put ~/.local/bin on PATH')
    expect(err).not.toMatch(/\/Users\/t\//)   // no half-tilde mixture
    expect(err).toContain('command not found')
    expect(err).toContain('Settings → Development')
    expect(err).toMatch(/open a new terminal and run `pdx path`/)
  })

  it('lists the fix that would help first, and still shows both', async () => {
    // ~/.local/bin is on PATH already: only `link` is missing.
    f.pathReport = () => ({ ...defaultPathReport(''), localBinExists: false, localBinOnPath: true, fixes: ['link'] })
    const onlyLink = await createLocalDaemon(f.deps).start().catch((e: Error) => e.message)
    expect(onlyLink!.indexOf('path link')).toBeLessThan(onlyLink!.indexOf('path add-to-shell'))

    const g = makeFake()
    g.files.set(BIN, identity('aaa'))
    g.shellPath = '/opt/homebrew/bin:/usr/bin'
    g.pathReport = () => ({ ...defaultPathReport(''), link: 'ok', fixes: ['add-to-shell'] })
    const onlyShell = await createLocalDaemon(g.deps).start().catch((e: Error) => e.message)
    expect(onlyShell!.indexOf('path add-to-shell')).toBeLessThan(onlyShell!.indexOf('path link'))
  })

  it('the SAME instance starts once the user fixes PATH — the gate defeats the cache (spec §7.5)', async () => {
    const d = createLocalDaemon(f.deps)
    await expect(d.start()).rejects.toThrow(/Refusing to start/)
    f.shellPath = `/opt/homebrew/bin:/usr/bin:${HOME}/.local/bin`   // user ran `pdx path add-to-shell`
    const res = await d.start()
    expect(res).toMatchObject({ url: 'http://100.64.0.9:7860', hash: 'aaa' })
  })

  it('install completes the swap and only then refuses; the binary is installed-and-stopped', async () => {
    scriptedDownload(f, 'bbb')
    const steps: string[] = []
    const d = createLocalDaemon(f.deps)
    await expect(d.install('http://src', 'tok', (s) => steps.push(s))).rejects.toThrow(/Refusing to start/)
    expect(steps).toEqual(['prepare', 'download', 'verify', 'swap'])
    expect(f.files.has(`${BIN}.new`)).toBe(false)
    expect(Buffer.from(f.files.get(BIN) as Uint8Array).toString('utf8')).toBe(identity('bbb'))
    const st = await d.status()
    expect(st.managed).toBe('managed')
    expect(st.alive).toBeNull()
    expect(st.installed?.hash).toBe('bbb')
  })

  it('a different pdx winning on PATH does NOT refuse — the criterion is resolved, never ok', async () => {
    // mlab today: ~/.local/bin/pdx is a hand-made symlink to a repo build.
    f.pathReport = () => ({
      ...defaultPathReport(`${HOME}/.local/bin`), resolved: '/repo/bin/pdx',
      resolvedReal: '/repo/bin/pdx', isSelf: false, ok: false, fixes: ['link'],
    })
    const d = createLocalDaemon(f.deps)
    await expect(d.start()).resolves.toMatchObject({ hash: 'aaa' })
    const st = await d.status()
    expect(st.cli).toMatchObject({ resolved: '/repo/bin/pdx', isManagedBinary: false })
  })

  it('a binary too old to have `path` is not refused — a gate the user cannot satisfy is worse', async () => {
    f.onExec = (_file, args) => (args[0] === 'path'
      ? { code: 2, stdout: '', stderr: 'pdx: unknown command "path"', timedOut: false }
      : undefined)
    await expect(createLocalDaemon(f.deps).start()).resolves.toMatchObject({ hash: 'aaa' })
  })
})

describe('the gate when the shell PATH cannot be read (spec §4.4)', () => {
  let f: Fake
  beforeEach(() => {
    f = makeFake()
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_t"\n')
    f.onExec = (file) => (file === '/bin/zsh' ? { code: 1, stdout: 'banner only\n', stderr: 'boom', timedOut: false } : undefined)
  })

  it('probe failed AND no resolve even on the injected fallback ⇒ refuse', async () => {
    f.pathReport = noPdxAnywhere
    await expect(createLocalDaemon(f.deps).start()).rejects.toThrow(/Refusing to start/)
  })

  it('probe failed BUT pdx resolves on the fallback ⇒ start, flagged pathSource fallback', async () => {
    const d = createLocalDaemon(f.deps)
    await expect(d.start()).resolves.toMatchObject({ hash: 'aaa' })
    const st = await d.status()
    expect(st.cli?.pathSource).toBe('fallback')
  })
})

describe('status cli block (spec §4.6)', () => {
  it('reports what pdx path found, with the Electron side\'s own pathSource', async () => {
    const f = makeFake()
    f.files.set(BIN, identity('aaa'))
    const st = await createLocalDaemon(f.deps).status()
    expect(st.cli).toEqual({
      resolved: `${HOME}/.local/bin/pdx`,
      isManagedBinary: true,
      pathSource: 'shell',
      localBinOnPath: true,
      link: 'ok',
    })
  })

  it('has no cli block when nothing is installed — there is nothing to ask', async () => {
    const f = makeFake()
    const st = await createLocalDaemon(f.deps).status()
    expect(st.cli).toBeUndefined()
  })

  it('never reports a link state of "created" — status has no memory of actions', async () => {
    const f = makeFake()
    f.files.set(BIN, identity('aaa'))
    const st = await createLocalDaemon(f.deps).status()
    expect(['ok', 'missing', 'conflict', 'error']).toContain(st.cli!.link)
  })
})

describe('the Electron side never writes to a shell rc file (spec §7.9)', () => {
  const RC = /(^|\/)\.(zshrc|bashrc|bash_profile|zprofile|profile)$/

  it('install, start, restart, ensureRunning and status touch no rc path', async () => {
    const f = makeFake()
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_t"\n')
    const touched: string[] = []
    const guard = (p: string) => { if (RC.test(p)) touched.push(p) }
    const fs = f.deps.fs
    f.deps.fs = {
      ...fs,
      writeFile: (p, d, m) => { guard(p); return fs.writeFile(p, d, m) },
      rename: (a, b) => { guard(a); guard(b); return fs.rename(a, b) },
      openWrite: (p) => { guard(p); return fs.openWrite(p) },
      unlink: (p) => { guard(p); return fs.unlink(p) },
    }
    scriptedDownload(f, 'bbb')
    const d = createLocalDaemon(f.deps)
    await d.install('http://src', 'tok', () => {})
    await d.restart()
    await d.ensureRunning()
    await d.status()
    await d.start().catch(() => {})
    expect(touched).toEqual([])
  })
})

describe('pathCommand (spec §5.1)', () => {
  let f: Fake
  beforeEach(() => {
    f = makeFake()
    f.files.set(BIN, identity('aaa'))
  })
  const pathRuns = () => f.execLog.filter((e) => e.args[0] === 'path' && e.args[1] !== '--json')

  it('runs the managed binary by absolute path with the launch env', async () => {
    await createLocalDaemon(f.deps).pathCommand('link')
    expect(pathRuns()).toHaveLength(1)
    expect(pathRuns()[0].file).toBe(BIN)
    expect(pathRuns()[0].args).toEqual(['path', 'link'])
    expect(pathRuns()[0].env?.PATH).toBe(f.shellPath)
  })

  // Codex R1 (PR #1081). fallbackPath() injects ~/.local/bin, so handing a
  // fallback env to `add-to-shell` would show it the very directory it exists
  // to add: the command calls itself a no-op, the rc file is never touched,
  // and the button reports success having changed nothing — on precisely the
  // machine whose real PATH we could not read.
  it('never hands a synthesized PATH to a command that decides by looking at PATH', async () => {
    f.onExec = (file) => (file === '/bin/zsh' ? { code: 1, stdout: '', stderr: 'no shell', timedOut: false } : undefined)
    // deps.baseEnv is the process's own inherited PATH: '/usr/bin:/bin'.
    await createLocalDaemon(f.deps).pathCommand('add-to-shell')
    const used = pathRuns()[0].env?.PATH ?? ''
    expect(used).toBe('/usr/bin:/bin')
    expect(used).not.toContain(`${HOME}/.local/bin`)
  })

  it('add-to-shell has its own argv', async () => {
    await createLocalDaemon(f.deps).pathCommand('add-to-shell')
    expect(pathRuns()[0].args).toEqual(['path', 'add-to-shell'])
  })

  it('--force is passed only when asked', async () => {
    const d = createLocalDaemon(f.deps)
    await d.pathCommand('link', { force: true })
    expect(pathRuns()[0].args).toEqual(['path', 'link', '--force'])
  })

  it('a refusal is returned verbatim, not thrown away — its value is the path it names', async () => {
    f.onExec = (_file, args) => (args[1] === 'link'
      ? { code: 1, stdout: '', stderr: `${HOME}/.local/bin/pdx is a symlink to /repo/bin/pdx; use --force\n`, timedOut: false }
      : undefined)
    const r = await createLocalDaemon(f.deps).pathCommand('link')
    expect(r).toEqual({ code: 1, stdout: '', stderr: `${HOME}/.local/bin/pdx is a symlink to /repo/bin/pdx; use --force\n` })
  })

  it('takes the lock: one issued during an in-flight operation runs after it, never during', async () => {
    const d = createLocalDaemon(f.deps)
    const order: string[] = []
    let release!: () => void
    const gate = new Promise<void>((r) => { release = r })
    const held = d.withLock(async () => { order.push('busy-start'); await gate; order.push('busy-end') })
    const cmd = d.pathCommand('link').then(() => order.push('path'))
    await new Promise((r) => setTimeout(r, 5))
    expect(order).toEqual(['busy-start'])
    release()
    await Promise.all([held, cmd])
    expect(order).toEqual(['busy-start', 'busy-end', 'path'])
  })
})

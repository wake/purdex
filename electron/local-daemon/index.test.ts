import { createHash } from 'node:crypto'
import { describe, expect, it, beforeEach } from 'vitest'
import { createLocalDaemon } from './index'
import type { LocalDaemonDeps, WriteHandle } from './types'
import type { ExecResult } from './launch-env'

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
  portIsOpen: boolean
  onExec: (file: string, args: string[]) => ExecResult | undefined
  downloads: Array<{ status: number; headers: Record<string, string>; body: Uint8Array }>
  clock: number
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
    portIsOpen: false,
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
      if (file === '/bin/zsh') return { code: 0, stdout: `${S}/opt/homebrew/bin:/usr/bin${S}\n`, stderr: '', timedOut: false }
      if (file === '/usr/bin/which') return { code: 0, stdout: '/opt/homebrew/bin/tmux\n', stderr: '', timedOut: false }
      if (file === '/usr/sbin/lsof') {
        if (args.includes('-d')) {
          const pid = Number(args[args.indexOf('-p') + 1])
          return { code: 0, stdout: fake.lsofTxt[pid] ?? '', stderr: '', timedOut: false }
        }
        return { code: 0, stdout: fake.lsofListen, stderr: '', timedOut: false }
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
        fake.alivePids.add(4242); fake.files.set(PID, '4242'); fake.portIsOpen = true
        fake.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
        fake.lsofListen = `p4242${NUL}\nf8${NUL}n${bindLine}:7860${NUL}\n`
        return { code: 0, stdout: 'started', stderr: '', timedOut: false }
      }
      if (args[0] === 'stop') { fake.health = null; fake.alivePids.clear(); fake.lsofListen = ''; fake.portIsOpen = false; return { code: 0, stdout: 'stopped', stderr: '', timedOut: false } }
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
      return new Response(Buffer.from(next.body), { status: next.status, headers: next.headers })
    },
    fs: {
      exists: async (p) => fake.files.has(p),
      readFile: async (p) => text(p),
      writeFile: async (p, d, mode) => { fake.files.set(p, d); fake.modes.set(p, mode) },
      rename: async (a, b) => { const v = fake.files.get(a); if (v === undefined) throw new Error('ENOENT'); fake.files.set(b, v); fake.files.delete(a); const m = fake.modes.get(a); if (m !== undefined) { fake.modes.set(b, m); fake.modes.delete(a) } },
      unlink: async (p) => { fake.files.delete(p) },
      mkdir: async (p) => { fake.dirs.push(p) },
      chmod: async (p, mode) => { fake.modes.set(p, mode) },
      realpath: async (p) => (p === `${HOME}/link-to-pdx` ? BIN : p),
      openWrite: async (p): Promise<WriteHandle> => { const chunks: Uint8Array[] = []; return { write: async (c) => { chunks.push(c) }, close: async () => { fake.files.set(p, Buffer.concat(chunks)) } } },
      sha256: async (p) => { const v = fake.files.get(p); return createHash('sha256').update(typeof v === 'string' ? Buffer.from(v) : Buffer.from(v ?? new Uint8Array())).digest('hex') },
    },
    kill0: (pid) => fake.alivePids.has(pid),
    portOpen: async (_h, _p, timeoutMs) => { fake.clock += Math.min(timeoutMs, 100); return fake.portIsOpen },
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
    expect(st.config).toEqual({ bind: '100.64.0.9', port: 7860, hasToken: true })
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
  })

  it('external when lsof times out', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.onExec = (file) => (file === '/usr/sbin/lsof' ? { code: null, stdout: '', stderr: '', timedOut: true } : undefined)
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('ownership check timed out')
  })

  it('maps x64 → amd64 and unknown identity on parse failure', async () => {
    f.deps.arch = 'x64'
    f.files.set(BIN, 'garbage')
    const st = await createLocalDaemon(f.deps).status()
    expect(st.target.goarch).toBe('amd64')
    expect(st.installed).toEqual({ version: 'unknown', hash: 'unknown', goos: 'unknown', goarch: 'unknown' })
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
    expect(start.env?.PATH).toBe('/opt/homebrew/bin:/usr/bin')
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
    f.onExec = (_file, args) => { if (args[0] === 'stop') { f.alivePids.clear(); f.portIsOpen = true; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    f.portIsOpen = true
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
    f.onExec = (_file, args) => { if (args[0] === 'stop') { f.alivePids.clear(); f.portIsOpen = true; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    f.portIsOpen = true
    const timeouts: number[] = []
    f.deps.portOpen = async (_h, _p, timeoutMs) => { timeouts.push(timeoutMs); f.clock += timeoutMs; return true }
    scriptedDownload(f, 'bbb')
    const t0 = f.clock
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/old binary was not replaced/)
    expect(f.clock - t0).toBeLessThanOrEqual(5000)
    expect(Math.max(...timeouts)).toBeLessThanOrEqual(500)
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
    f.files.delete(BIN); f.alivePids.clear(); f.files.delete(PID); f.health = null; f.lsofListen = ''; f.portIsOpen = false
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

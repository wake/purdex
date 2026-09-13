import { describe, expect, it, vi, beforeEach } from 'vitest'
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
      return new Response(next.body, { status: next.status, headers: next.headers })
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
      sha256: async (p) => { const { createHash } = await import('node:crypto'); const v = fake.files.get(p); return createHash('sha256').update(typeof v === 'string' ? Buffer.from(v) : Buffer.from(v ?? new Uint8Array())).digest('hex') },
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

// electron/local-daemon/node-deps.ts
// The real-world LocalDaemonDeps. Kept apart from index.ts so the logic
// never imports node:fs / child_process directly and stays unit-testable.
import { execFile } from 'node:child_process'
import { createHash, randomBytes } from 'node:crypto'
import { createWriteStream } from 'node:fs'
import { access, chmod, mkdir, readFile, realpath, rename, unlink, writeFile } from 'node:fs/promises'
import { connect } from 'node:net'
import { homedir, hostname, networkInterfaces } from 'node:os'
import type { ExecFn } from './launch-env'
import type { LocalDaemonDeps, WriteHandle } from './types'

const exec: ExecFn = (file, args, opts) =>
  new Promise((resolve) => {
    const child = execFile(
      file,
      args,
      { env: opts.env, cwd: opts.cwd, timeout: opts.timeoutMs, maxBuffer: 8 * 1024 * 1024, killSignal: 'SIGKILL', encoding: 'utf8' },
      (err, stdout, stderr) => {
        const timedOut = !!(err && (err as NodeJS.ErrnoException & { killed?: boolean }).killed && child.signalCode === 'SIGKILL')
        // execFile's error.code is the numeric exit status or a spawn errno
        // string (e.g. 'ENOENT'); ErrnoException types it as string, so widen.
        const code = err ? ((err as { code?: number | string }).code ?? null) : 0
        resolve({ code: typeof code === 'number' ? code : err ? null : 0, stdout: String(stdout), stderr: String(stderr), timedOut })
      },
    )
    // stdin closed: an interactive shell probe must never wait on a tty.
    child.stdin?.end()
  })

async function exists(p: string): Promise<boolean> {
  try { await access(p); return true } catch { return false }
}

async function openWrite(p: string): Promise<WriteHandle> {
  const ws = createWriteStream(p, { mode: 0o755 })
  await new Promise<void>((res, rej) => { ws.once('open', () => res()); ws.once('error', rej) })
  return {
    write: (chunk) => new Promise((res, rej) => { ws.write(chunk, (e) => (e ? rej(e) : res())) }),
    close: () => new Promise((res, rej) => { ws.once('error', rej); ws.end(() => res()) }),
  }
}

async function sha256(p: string): Promise<string> {
  const { createReadStream } = await import('node:fs')
  return new Promise((res, rej) => {
    const h = createHash('sha256')
    createReadStream(p).on('data', (d) => h.update(d)).on('end', () => res(h.digest('hex'))).on('error', rej)
  })
}

export function nodeDeps(log: (msg: string) => void = console.log): LocalDaemonDeps {
  return {
    home: homedir(),
    hostname: () => hostname(),
    platform: process.platform,
    arch: process.arch,
    shell: process.env.SHELL,
    baseEnv: process.env,
    exec,
    fetch: (url, init) => fetch(url, init),
    fs: {
      exists,
      readFile: (p) => readFile(p, 'utf8'),
      writeFile: (p, d, mode) => writeFile(p, d, { mode }),
      rename, unlink, chmod, realpath,
      mkdir: async (p) => { await mkdir(p, { recursive: true }) },
      openWrite,
      sha256,
    },
    kill0: (pid) => { try { process.kill(pid, 0); return true } catch { return false } },
    portOpen: (host, port, timeoutMs) => new Promise((res) => {
      const sock = connect({ host, port })
      const done = (v: boolean) => { sock.destroy(); res(v) }
      sock.setTimeout(timeoutMs, () => done(false))
      sock.once('connect', () => done(true))
      sock.once('error', () => done(false))
    }),
    networkInterfaces: () => Object.entries(networkInterfaces()).flatMap(([name, list]) => (list ?? []).map((i) => ({ name, address: i.address, family: String(i.family), internal: i.internal }))),
    randomBytes: (n) => randomBytes(n),
    sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
    now: () => Date.now(),
    log,
  }
}

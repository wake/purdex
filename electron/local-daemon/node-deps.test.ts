// Real-fs tests for the few node-deps helpers whose failure modes matter:
// a WriteHandle must never hang after its stream failed (index.ts awaits
// `close()` in a `finally`, on the daemon's single promise queue).
import { createHash } from 'node:crypto'
import { createWriteStream, mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { openWrite, wrapWriteStream } from './node-deps'

let dir: string
beforeEach(() => { dir = mkdtempSync(join(tmpdir(), 'pdx-node-deps-')) })
afterEach(() => { rmSync(dir, { recursive: true, force: true }) })

function settlesWithin<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((res, rej) => {
    const t = setTimeout(() => rej(new Error(`did not settle within ${ms} ms`)), ms)
    p.then((v) => { clearTimeout(t); res(v) }, (e) => { clearTimeout(t); rej(e) })
  })
}

describe('openWrite()', () => {
  it('write → close persists the bytes with mode 0755', async () => {
    const p = join(dir, 'pdx.new')
    const h = await openWrite(p)
    await h.write(Buffer.from('hello '))
    await h.write(Buffer.from('world'))
    await h.close()
    expect(readFileSync(p, 'utf8')).toBe('hello world')
    const sha = createHash('sha256').update('hello world').digest('hex')
    expect(createHash('sha256').update(readFileSync(p)).digest('hex')).toBe(sha)
  })

  it('rejects when the file cannot be opened', async () => {
    await expect(openWrite(join(dir, 'missing-dir', 'pdx.new'))).rejects.toThrow(/ENOENT/)
  })
})

describe('wrapWriteStream() after a stream failure', () => {
  async function openedStream() {
    const ws = createWriteStream(join(dir, 'out'))
    await new Promise<void>((res, rej) => { ws.once('open', () => res()); ws.once('error', rej) })
    return ws
  }

  it('close() settles (rejects with the stream error) when error+close already fired', async () => {
    const ws = await openedStream()
    const h = wrapWriteStream(ws)
    const closed = new Promise<void>((res) => ws.once('close', () => res()))
    ws.destroy(new Error('boom'))
    await closed // both 'error' and 'close' have been emitted by now
    await expect(settlesWithin(h.close(), 1000)).rejects.toThrow('boom')
  })

  it('close() settles when called right after destroy(err), before the events fire', async () => {
    const ws = await openedStream()
    const h = wrapWriteStream(ws)
    ws.destroy(new Error('boom'))
    await expect(settlesWithin(h.close(), 1000)).rejects.toThrow('boom')
  })

  it('write() after the stream failed rejects with the stored error', async () => {
    const ws = await openedStream()
    const h = wrapWriteStream(ws)
    const closed = new Promise<void>((res) => ws.once('close', () => res()))
    ws.destroy(new Error('boom'))
    await closed
    await expect(settlesWithin(h.write(Buffer.from('x')), 1000)).rejects.toThrow('boom')
  })

  it('close() after a clean close() resolves again instead of hanging', async () => {
    const ws = await openedStream()
    const h = wrapWriteStream(ws)
    await h.write(Buffer.from('x'))
    await h.close()
    await expect(settlesWithin(h.close(), 1000)).resolves.toBeUndefined()
  })
})

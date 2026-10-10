// agentUploadToPath: the deck / chat attachment upload (save only, inject=0, answers the saved path, reports progress).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import { agentUploadToPath, AgentUploadError, AGENT_UPLOAD_MAX_BYTES } from './host-api'

const HOST_ID = 'test-host'
const TOKEN = 'purdex_test_token'

class FakeXHR {
  static last: FakeXHR
  method = ''; url = ''; headers: Record<string, string> = {}; body: FormData | null = null
  status = 0; responseText = ''
  upload = { onprogress: null as ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null }
  onload: (() => void) | null = null; onerror: (() => void) | null = null; onabort: (() => void) | null = null
  aborted = false
  constructor() { FakeXHR.last = this }
  open(m: string, u: string) { this.method = m; this.url = u }
  setRequestHeader(k: string, v: string) { this.headers[k] = v }
  send(b: FormData) { this.body = b }
  abort() { this.aborted = true; this.onabort?.() }
  reply(status: number, body: unknown) { this.status = status; this.responseText = typeof body === 'string' ? body : JSON.stringify(body); this.onload?.() }
}

beforeEach(() => {
  vi.stubGlobal('XMLHttpRequest', FakeXHR)
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '100.64.0.2', port: 7860, token: TOKEN, order: 0 } },
    hostOrder: [HOST_ID],
  })
})
afterEach(() => vi.unstubAllGlobals())

const file = () => new File(['data'], 'a.png', { type: 'image/png' })

describe('agentUploadToPath', () => {
  it('posts session code, file and inject=0 with auth, and answers the saved path', async () => {
    const p = agentUploadToPath(HOST_ID, file(), 'dev001')
    const x = FakeXHR.last
    expect(x.method).toBe('POST')
    expect(x.url).toBe('http://100.64.0.2:7860/api/agent/upload')
    expect(x.headers.Authorization).toBe(`Bearer ${TOKEN}`)
    expect(x.body!.get('session')).toBe('dev001')
    expect(x.body!.get('inject')).toBe('0')
    expect(x.body!.get('file')).toBeInstanceOf(File)
    x.reply(200, { filename: 'a.png', injected: false, path: '/up/dev001/a.png' })
    expect(await p).toEqual({ filename: 'a.png', path: '/up/dev001/a.png' })
  })

  it('reports upload progress as a percentage', async () => {
    const onProgress = vi.fn()
    const p = agentUploadToPath(HOST_ID, file(), 'dev001', { onProgress })
    FakeXHR.last.upload.onprogress!({ lengthComputable: true, loaded: 25, total: 100 })
    FakeXHR.last.reply(200, { filename: 'a.png', injected: false, path: '/x' })
    await p
    expect(onProgress).toHaveBeenCalledWith(25)
  })

  it.each([[413, 'too_large'], [429, 'too_many'], [404, 'not_found'], [500, 'http'], [400, 'http']])('maps HTTP %i to %s', async (status, kind) => {
    const p = agentUploadToPath(HOST_ID, file(), 'dev001')
    FakeXHR.last.reply(status, { error: 'x' })
    await expect(p).rejects.toMatchObject({ kind, status })
    await expect(p).rejects.toBeInstanceOf(AgentUploadError)
  })

  it('maps a network failure to network', async () => {
    const p = agentUploadToPath(HOST_ID, file(), 'dev001')
    FakeXHR.last.onerror!()
    await expect(p).rejects.toMatchObject({ kind: 'network' })
  })

  it('a 200 without a path is an error, not a success', async () => {
    const p = agentUploadToPath(HOST_ID, file(), 'dev001')
    FakeXHR.last.reply(200, { filename: 'a.png', injected: false })
    await expect(p).rejects.toMatchObject({ kind: 'http' })
  })

  it('an unknown host is refused without building a request (no fallback to the active host)', async () => {
    const before = FakeXHR.last
    await expect(agentUploadToPath('not-mine', file(), 'dev001')).rejects.toMatchObject({ kind: 'host_missing' })
    expect(FakeXHR.last).toBe(before)
  })

  it('abort cancels the request and rejects as aborted', async () => {
    const ac = new AbortController()
    const p = agentUploadToPath(HOST_ID, file(), 'dev001', { signal: ac.signal })
    ac.abort()
    expect(FakeXHR.last.aborted).toBe(true)
    await expect(p).rejects.toMatchObject({ kind: 'aborted' })
  })

  // #2493: the size is compared before anything is sent
  const sized = (bytes: number) => Object.defineProperty(file(), 'size', { value: bytes })

  it('a file over 256 MiB is refused as too_large without a request', async () => {
    const before = FakeXHR.last
    await expect(agentUploadToPath(HOST_ID, sized(AGENT_UPLOAD_MAX_BYTES + 1), 'dev001')).rejects.toMatchObject({ kind: 'too_large' })
    expect(FakeXHR.last).toBe(before)
  })

  it('a file of exactly 256 MiB is sent', () => {
    const before = FakeXHR.last
    void agentUploadToPath(HOST_ID, sized(AGENT_UPLOAD_MAX_BYTES), 'dev001')
    expect(FakeXHR.last).not.toBe(before)
  })
})

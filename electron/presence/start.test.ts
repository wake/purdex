import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { startPresence } from './start'
import type { IpcMainLike } from './ipc'
import type { PowerMonitorLike } from './sampler'

const TOKEN = 'tok-WIRING-secret'

function setup() {
  const dir = mkdtempSync(join(tmpdir(), 'pdx-presence-'))
  const prefsPath = join(dir, 'app-prefs.json')
  const handlers = new Map<string, (e: unknown, ...a: unknown[]) => unknown>()
  const ipcMain: IpcMainLike = { handle: (c, f) => { handlers.set(c, f) } }
  const pmListeners = new Map<string, () => void>()
  const powerMonitor = {
    getSystemIdleTime: () => 0,
    getSystemIdleState: () => 'active' as const,
    on: (ev: string, cb: () => void) => { pmListeners.set(ev, cb) },
    removeListener: (ev: string) => { pmListeners.delete(ev) },
  } as unknown as PowerMonitorLike
  const puts: Array<Record<string, unknown>> = []
  const fetchFn = vi.fn(async (url: string, init: { body?: string }) => {
    if (url.endsWith('/api/info')) return { ok: true, status: 200, json: async () => ({ capabilities: ['push.presence.v2'] }) }
    puts.push(JSON.parse(init.body ?? '{}') as Record<string, unknown>)
    return { ok: true, status: 204, json: async () => ({}) }
  })
  const owned: string[][] = []
  const logs: string[] = []
  const h = startPresence({
    prefsPath,
    ipcMain,
    powerMonitor,
    fetch: fetchFn,
    broadcastOwned: (ids) => owned.push(ids),
    logger: { warn: (m) => logs.push(m) },
    now: () => Date.now(),
  })
  const call = (ch: string, ...a: unknown[]) => handlers.get(ch)!({}, ...a)
  return { dir, prefsPath, h, call, pmListeners, puts, owned, logs, fetchFn }
}

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { vi.useRealTimers() })

describe('startPresence', () => {
  it('hands hosts over through IPC, sends v2 with a persisted install id, broadcasts ownership', async () => {
    const s = setup()
    try {
      expect(s.call('presence:set-hosts', { rev: 1, hosts: [{ hostId: 'h1', baseUrl: 'http://100.64.0.2:7860', token: TOKEN }] })).toBe(true)
      await vi.advanceTimersByTimeAsync(0)
      expect(s.puts).toHaveLength(1)
      const id = JSON.parse(readFileSync(s.prefsPath, 'utf8')).installId as string
      expect(s.puts[0].client_id).toBe(`mac:${id}`)
      expect(s.puts[0]).toMatchObject({ v: 2, away_after_ms: 300_000 })
      expect(s.owned).toEqual([['h1']])
      expect(s.call('presence:get-owned')).toEqual(['h1'])
      // the token is memory-only: nothing on disk carries it
      expect(readFileSync(s.prefsPath, 'utf8')).not.toContain(TOKEN)
    } finally {
      s.h.stop()
      rmSync(s.dir, { recursive: true, force: true })
    }
  })
  it('a lock event from the sampler forces a send', async () => {
    const s = setup()
    try {
      s.call('presence:set-hosts', { rev: 1, hosts: [{ hostId: 'h1', baseUrl: 'http://h:1', token: null }] })
      await vi.advanceTimersByTimeAsync(0)
      const n = s.puts.length
      s.pmListeners.get('lock-screen')!()
      await vi.advanceTimersByTimeAsync(0)
      expect(s.puts).toHaveLength(n + 1)
      expect(s.puts.at(-1)).toMatchObject({ locked: true })
    } finally {
      s.h.stop()
      rmSync(s.dir, { recursive: true, force: true })
    }
  })
  it('changing the away minutes persists, forces a send, and the next body carries it', async () => {
    const s = setup()
    try {
      s.call('presence:set-hosts', { rev: 1, hosts: [{ hostId: 'h1', baseUrl: 'http://h:1', token: null }] })
      await vi.advanceTimersByTimeAsync(0)
      const n = s.puts.length
      expect(s.call('presence:set-away-minutes', 15)).toBe(15)
      await vi.advanceTimersByTimeAsync(0)
      expect(s.puts).toHaveLength(n + 1)
      expect(s.puts.at(-1)?.away_after_ms).toBe(900_000)
      expect(JSON.parse(readFileSync(s.prefsPath, 'utf8')).presenceAwayMinutes).toBe(15)
    } finally {
      s.h.stop()
      rmSync(s.dir, { recursive: true, force: true })
    }
  })
  it('stop() ends sampling and reporting', async () => {
    const s = setup()
    try {
      s.call('presence:set-hosts', { rev: 1, hosts: [{ hostId: 'h1', baseUrl: 'http://h:1', token: null }] })
      await vi.advanceTimersByTimeAsync(0)
      s.h.stop()
      const n = s.fetchFn.mock.calls.length
      await vi.advanceTimersByTimeAsync(120_000)
      expect(s.fetchFn.mock.calls.length).toBe(n)
      expect(s.pmListeners.size).toBe(0)
    } finally {
      rmSync(s.dir, { recursive: true, force: true })
    }
  })
})

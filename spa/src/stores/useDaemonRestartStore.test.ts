import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useDaemonRestartStore } from './useDaemonRestartStore'
import { useUndoToast } from './useUndoToast'
import { useI18nStore } from './useI18nStore'
import { useNexHostStore } from './useNexHostStore'
import { useHostStore } from './useHostStore'
import * as restartLib from '../lib/daemon-restart'
import { DaemonRestartError } from '../lib/daemon-restart'

vi.mock('../lib/daemon-restart', async (orig) => ({ ...(await orig<typeof import('../lib/daemon-restart')>()), restartDaemon: vi.fn() }))

const ok = { ipc: null, shutdownWarnings: 0 }

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useDaemonRestartStore.setState({ restarting: {}, settled: {} })
  useUndoToast.setState({ toast: null, notice: null })
  vi.mocked(restartLib.restartDaemon).mockReset()
})

describe('useDaemonRestartStore', () => {
  it('marks the host restarting while the action runs, then clears it', async () => {
    let finish!: () => void
    vi.mocked(restartLib.restartDaemon).mockReturnValueOnce(new Promise((r) => { finish = () => r(ok) }))
    const p = useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(useDaemonRestartStore.getState().restarting.h1).toBe(true)
    expect(useDaemonRestartStore.getState().restarting.h2).toBeUndefined()
    finish(); await p
    expect(useDaemonRestartStore.getState().restarting.h1).toBeUndefined()
    expect(useDaemonRestartStore.getState().settled.h1).toBe(1)
  })

  it('a second restart of the same host while one runs is a no-op', async () => {
    vi.mocked(restartLib.restartDaemon).mockReturnValueOnce(new Promise(() => {}))
    void useDaemonRestartStore.getState().restart('h1', 'mlab')
    await useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(restartLib.restartDaemon).toHaveBeenCalledTimes(1)
  })

  it('success → toast, nex info re-read', async () => {
    const invalidate = vi.spyOn(useNexHostStore.getState(), 'invalidate').mockResolvedValue()
    vi.mocked(restartLib.restartDaemon).mockResolvedValueOnce(ok)
    await useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(useUndoToast.getState().toast?.message).toBe('mlab：daemon 已重新啟動')
    expect(invalidate).toHaveBeenCalledWith('h1')
  })

  it('success with shutdown warnings → the warning toast', async () => {
    vi.spyOn(useNexHostStore.getState(), 'invalidate').mockResolvedValue()
    vi.mocked(restartLib.restartDaemon).mockResolvedValueOnce({ ipc: null, shutdownWarnings: 2 })
    await useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(useUndoToast.getState().toast?.message).toBe('mlab：daemon 已重新啟動，但關閉時有 2 個警告（見 ~/.config/pdx/logs/pdx.log）')
  })

  it('IPC result re-registers the local host', async () => {
    const reg = vi.spyOn(useHostStore.getState(), 'registerLocalHost').mockReturnValue('h1')
    vi.mocked(restartLib.restartDaemon).mockResolvedValueOnce({
      ipc: { url: 'http://127.0.0.1:7860', token: 't', hash: 'x', version: 'v', hostname: 'air' },
      shutdownWarnings: 0,
    } as never)
    await useDaemonRestartStore.getState().restart('h1', 'air')
    expect(reg).toHaveBeenCalledWith({ url: 'http://127.0.0.1:7860', token: 't', hostname: 'air' })
  })

  it.each([
    [new DaemonRestartError('timeout'), 'mlab：daemon 沒有在 60 秒內回來，請查看該主機的 ~/.config/pdx/logs/pdx.log'],
    [new DaemonRestartError('request', 'Failed to fetch'), 'mlab：重啟失敗（Failed to fetch），請查看該主機的 ~/.config/pdx/logs/pdx.log'],
    [new DaemonRestartError('unsupported', 'HTTP 404'), 'mlab：這台 daemon 版本不支援遠端重啟，請在該主機執行 pdx stop && pdx start'],
  ])('failure %# → persistent notice', async (err, text) => {
    vi.mocked(restartLib.restartDaemon).mockRejectedValueOnce(err)
    await useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(useUndoToast.getState().notice?.message).toBe(text)
    expect(useDaemonRestartStore.getState().restarting.h1).toBeUndefined()
    expect(useDaemonRestartStore.getState().settled.h1).toBe(1)
  })
})

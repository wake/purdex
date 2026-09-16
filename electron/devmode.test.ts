import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const src = (p: string) => readFileSync(resolve(__dirname, p), 'utf8')
const DTS = '../spa/src/types/electron.d.ts'

describe('PDX_DEV_MODE is on by default (spec D6)', () => {
  it('main sets the default before any gate reads it', () => {
    const main = src('main.ts')
    const idx = main.indexOf("if (process.env.PDX_DEV_MODE === undefined) process.env.PDX_DEV_MODE = '1'")
    expect(idx).toBeGreaterThan(-1)
    // The default must precede the IPC gate.
    expect(idx).toBeLessThan(main.indexOf("process.env.PDX_DEV_MODE !== '0'"))
  })
  it('no gate still requires === "1"', () => {
    for (const f of ['main.ts', 'preload.ts', 'updater.ts']) {
      expect(src(f), f).not.toContain("PDX_DEV_MODE === '1'")
    }
  })
  it('preload and updater gate on !== "0"', () => {
    expect(src('preload.ts')).toContain("process.env.PDX_DEV_MODE !== '0'")
    expect(src('updater.ts')).toContain("devUpdateEnabled: process.env.PDX_DEV_MODE !== '0'")
  })
})

describe('local daemon wiring', () => {
  it('main registers the four IPC handlers inside the dev gate and calls ensureRunning on ready', () => {
    const main = src('main.ts')
    for (const ch of ['dev:local-daemon-status', 'dev:local-daemon-install', 'dev:local-daemon-start', 'dev:local-daemon-restart']) {
      expect(main).toContain(`ipcMain.handle('${ch}'`)
    }
    expect(main).toContain('localDaemon.ensureRunning()')
    // applyUpdate must run inside the lock — the exact wrapping form.
    expect(main).toContain('await localDaemon.withLock(() => applyUpdate(')
    // Handlers and ensureRunning sit inside the dev gate.
    const gate = main.indexOf("if (process.env.PDX_DEV_MODE !== '0') {")
    expect(gate).toBeGreaterThan(-1)
    for (const ch of ['dev:local-daemon-status', 'dev:local-daemon-install', 'dev:local-daemon-start', 'dev:local-daemon-restart']) {
      expect(main.indexOf(`ipcMain.handle('${ch}'`)).toBeGreaterThan(gate)
    }
    const ready = main.indexOf('localDaemon.ensureRunning()')
    expect(main.lastIndexOf("process.env.PDX_DEV_MODE !== '0'", ready)).toBeGreaterThan(-1)
  })
  it('preload exposes the bridges', () => {
    const preload = src('preload.ts')
    for (const name of ['localDaemonStatus', 'localDaemonInstall', 'localDaemonStart', 'localDaemonRestart', 'onLocalDaemonProgress']) {
      expect(preload).toContain(name)
    }
  })

  it('main registers the two `pdx path` channels inside the dev gate (spec §5.1)', () => {
    const main = src('main.ts')
    const gate = main.indexOf("if (process.env.PDX_DEV_MODE !== '0') {")
    expect(gate).toBeGreaterThan(-1)
    for (const ch of ['dev:local-daemon-path-link', 'dev:local-daemon-path-add-to-shell']) {
      expect(main).toContain(`ipcMain.handle('${ch}'`)
      expect(main.indexOf(`ipcMain.handle('${ch}'`)).toBeGreaterThan(gate)
    }
  })

  it('preload exposes both path bridges and the SPA types them', () => {
    const preload = src('preload.ts')
    const dts = src(DTS)
    for (const name of ['localDaemonPathLink', 'localDaemonPathAddToShell']) {
      expect(preload, 'preload.ts').toContain(name)
      expect(dts, 'electron.d.ts').toContain(name)
    }
  })
})

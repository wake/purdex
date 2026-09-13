import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const src = (p: string) => readFileSync(resolve(__dirname, p), 'utf8')

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

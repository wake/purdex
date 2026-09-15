import { describe, expect, it } from 'vitest'
import { join } from 'node:path'
import { resolveTrayIconPath } from './tray-icon'

const mainDir = '/App/Resources/app/out/main'
const packaged = join(mainDir, '../renderer/icons/trayTemplate.png')
const source = join(mainDir, '../../spa/public/icons/trayTemplate.png')

describe('resolveTrayIconPath', () => {
  it('prefers the built renderer copy (packaged / after electron-vite build)', () => {
    const tried: string[] = []
    const exists = (p: string) => { tried.push(p); return true }
    expect(resolveTrayIconPath(mainDir, exists)).toBe(packaged)
    expect(tried).toEqual([packaged])
  })
  it('falls back to the source tree copy when out/renderer is not built', () => {
    const exists = (p: string) => p === source
    expect(resolveTrayIconPath(mainDir, exists)).toBe(source)
  })
  it('returns null when neither candidate exists', () => {
    const tried: string[] = []
    const exists = (p: string) => { tried.push(p); return false }
    expect(resolveTrayIconPath(mainDir, exists)).toBeNull()
    expect(tried).toEqual([packaged, source])
  })
})

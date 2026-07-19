import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  shouldRegisterServiceWorker,
  shouldReloadOnControllerChange,
  registerServiceWorker,
} from './register-sw'

describe('shouldRegisterServiceWorker', () => {
  const base = { isElectron: false, protocol: 'https:', hasServiceWorker: true }
  it('web + https + SW 可用 → 註冊', () => {
    expect(shouldRegisterServiceWorker(base)).toBe(true)
  })
  it('http（localhost dev）→ 註冊（瀏覽器只在 secure context 生效，失敗會被 catch）', () => {
    expect(shouldRegisterServiceWorker({ ...base, protocol: 'http:' })).toBe(true)
  })
  it('Electron → 不註冊', () => {
    expect(shouldRegisterServiceWorker({ ...base, isElectron: true })).toBe(false)
  })
  it('app: protocol（Electron bundled）→ 不註冊', () => {
    expect(shouldRegisterServiceWorker({ ...base, protocol: 'app:' })).toBe(false)
  })
  it('navigator 無 serviceWorker → 不註冊', () => {
    expect(shouldRegisterServiceWorker({ ...base, hasServiceWorker: false })).toBe(false)
  })
})

describe('shouldReloadOnControllerChange', () => {
  it('頁面原本已被控制（更新接管）→ reload', () => {
    expect(shouldReloadOnControllerChange(true)).toBe(true)
  })
  it('首次取得控制權（無前一 controller）→ 不 reload', () => {
    expect(shouldReloadOnControllerChange(false)).toBe(false)
  })
})

describe('registerServiceWorker (glue)', () => {
  let origLocation: Location
  let origSW: PropertyDescriptor | undefined
  let register: ReturnType<typeof vi.fn>
  let reload: ReturnType<typeof vi.fn>
  let listeners: Record<string, () => void>

  beforeEach(() => {
    origLocation = window.location
    origSW = Object.getOwnPropertyDescriptor(navigator, 'serviceWorker')
    register = vi.fn().mockResolvedValue(undefined)
    reload = vi.fn()
    listeners = {}
    delete (window as unknown as { electronAPI?: unknown }).electronAPI
    Object.defineProperty(window, 'location', {
      value: { protocol: 'https:', reload }, writable: true, configurable: true,
    })
    Object.defineProperty(navigator, 'serviceWorker', {
      value: {
        controller: null,
        register,
        addEventListener: (ev: string, cb: () => void) => { listeners[ev] = cb },
      },
      writable: true, configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', { value: origLocation, writable: true, configurable: true })
    if (origSW) Object.defineProperty(navigator, 'serviceWorker', origSW)
    else delete (navigator as unknown as { serviceWorker?: unknown }).serviceWorker
  })

  it('web https 無前一 controller → 註冊，首次 controllerchange 不 reload', () => {
    registerServiceWorker()
    expect(register).toHaveBeenCalledWith('/sw.js')
    listeners['controllerchange']?.()
    expect(reload).not.toHaveBeenCalled()
  })

  it('已有 controller（更新接管）→ controllerchange reload 一次（防迴圈）', () => {
    ;(navigator.serviceWorker as unknown as { controller: unknown }).controller = {}
    registerServiceWorker()
    listeners['controllerchange']?.()
    listeners['controllerchange']?.()
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('Electron → 不註冊', () => {
    ;(window as unknown as { electronAPI?: unknown }).electronAPI = {}
    registerServiceWorker()
    expect(register).not.toHaveBeenCalled()
  })
})

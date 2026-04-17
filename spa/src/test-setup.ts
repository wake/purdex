import '@testing-library/jest-dom/vitest'
import { afterEach, beforeAll } from 'vitest'
import { cleanup } from '@testing-library/react'
import { registerBuiltinLocales } from './lib/register-locales'
import { clearLocaleRegistry } from './lib/locale-registry'

// Register built-in locales once so t() returns real strings in all tests
beforeAll(() => {
  clearLocaleRegistry()
  registerBuiltinLocales()
})

// Auto-cleanup after each test (required because vitest doesn't expose afterEach globally)
afterEach(() => cleanup())

// jsdom does not implement ResizeObserver
globalThis.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// jsdom does not implement matchMedia
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
  }),
})

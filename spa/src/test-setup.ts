import '@testing-library/jest-dom/vitest'
import 'fake-indexeddb/auto'
import { afterEach, beforeAll, beforeEach } from 'vitest'
import { cleanup } from '@testing-library/react'
import { registerBuiltinLocales } from './lib/register-locales'
import { clearLocaleRegistry } from './lib/locale-registry'
import { keyboardGuard } from './components/approval-dialog-helpers'
import { clearAllWorkerDrafts } from './lib/nex/worker-draft-memory'

// Register built-in locales once so t() returns real strings in all tests
beforeAll(() => {
  clearLocaleRegistry()
  registerBuiltinLocales()
})

// jsdom's fireEvent.click has detail 0, which the approval dialog reads as the keyboard: no guard unless a test asks for it
beforeEach(() => { keyboardGuard.ms = 0 })

// Auto-cleanup after each test (required because vitest doesn't expose afterEach globally)
afterEach(() => { cleanup(); clearAllWorkerDrafts() })

// jsdom does not implement ResizeObserver
globalThis.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// jsdom 不提供 matchMedia — skip under `@vitest-environment node` (no window;
// used by tests that need a real ReadableStream/TextEncoder for SSE parsing).
if (typeof window !== 'undefined') {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  })
}

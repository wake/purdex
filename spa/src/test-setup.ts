import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

// Auto-cleanup after each test (required because vitest doesn't expose afterEach globally)
afterEach(() => cleanup())

// jsdom does not implement ResizeObserver
globalThis.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

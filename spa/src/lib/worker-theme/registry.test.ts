import { describe, it, expect } from 'vitest'
import { getWorkerTheme, listWorkerThemes, workerThemeStyle } from './registry'

describe('worker-theme registry', () => {
  it('falls back to purdex for an unknown or missing id', () => {
    expect(getWorkerTheme('nope').id).toBe('purdex')
    expect(getWorkerTheme(undefined).id).toBe('purdex')
  })

  it('lists purdex', () => {
    expect(listWorkerThemes().map((t) => t.id)).toContain('purdex')
  })

  it('maps vars to --wt- custom properties', () => {
    const style = workerThemeStyle(getWorkerTheme('purdex'))
    expect(style['--wt-font-size']).toBe('14px')
    expect(Object.keys(style).every((k) => k.startsWith('--wt-'))).toBe(true)
  })
})

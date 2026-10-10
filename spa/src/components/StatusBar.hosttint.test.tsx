// Bottom bar v3: the host's colour, darkened into the surface, is the bar's ground (c9d9c7-52 point 2).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { useHostStore } from '../stores/useHostStore'
import { HOST_COLOR_PRESETS } from '../lib/host-color'
import { hexToRgb } from '../lib/color-space'
import { STATUS_BAR_HOST_TINT_PCT } from './status/StatusSegments'
import { HOST_ID, setupStores, makeTab } from './StatusBar.test-helpers'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))
beforeEach(() => { cleanup(); setupStores() })

const tab = () => makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })

describe('StatusBar host tint', () => {
  it('has no tint for a host without a colour', () => {
    render(<StatusBar activeTab={tab()} />)
    const bar = screen.getByTestId('status-bar')
    expect(bar.getAttribute('data-host-tinted')).toBeNull()
    expect(bar.style.backgroundColor).toBe('')
  })

  it('mixes the host colour into the theme surface', () => {
    useHostStore.setState({ hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, color: '#3b82f6' } } })
    render(<StatusBar activeTab={tab()} />)
    const bar = screen.getByTestId('status-bar')
    expect(bar.getAttribute('data-host-tinted')).toBe('true')
    // jsdom keeps the declaration it cannot parse out of the inline style attribute
    expect(bar.getAttribute('style')).toContain(`color-mix(in srgb, #3b82f6 ${STATUS_BAR_HOST_TINT_PCT}%, var(--surface-secondary))`)
  })

  // The tint is a fixed share of the host colour over the theme's surface, so the worst case is the brightest preset on each theme.
  const lum = ({ r, g, b }: { r: number; g: number; b: number }) => {
    const f = (c: number) => { const s = c / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4 }
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
  }
  const contrast = (a: { r: number; g: number; b: number }, b: { r: number; g: number; b: number }) => {
    const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x)
    return (hi + 0.05) / (lo + 0.05)
  }
  const THEMES: Record<string, { surface: string; text: string }> = {
    dark: { surface: '#12122a', text: '#9ca3af' },
    light: { surface: '#e8e8e8', text: '#4a4a5a' },
    nord: { surface: '#3b4252', text: '#d8dee9' },
    dracula: { surface: '#21222c', text: '#bfbfbf' },
  }
  it.each(Object.entries(THEMES))('keeps text-secondary at 4.5:1 or better on %s for every preset host colour', (_name, th) => {
    const surface = hexToRgb(th.surface)!
    const text = hexToRgb(th.text)!
    const k = STATUS_BAR_HOST_TINT_PCT / 100
    for (const preset of HOST_COLOR_PRESETS) {
      const h = hexToRgb(preset)!
      const ground = { r: h.r * k + surface.r * (1 - k), g: h.g * k + surface.g * (1 - k), b: h.b * k + surface.b * (1 - k) }
      expect(contrast(text, ground), `${preset} on ${_name}`).toBeGreaterThanOrEqual(4.5)
    }
  })
})

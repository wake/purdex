import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DevBuildLogPanel } from './DevBuildLogPanel'
import { useI18nStore } from '../../stores/useI18nStore'

describe('DevBuildLogPanel', () => {
  beforeEach(() => {
    // Stub t() so the component renders i18n keys verbatim; tests match on keys.
    useI18nStore.setState({ t: (k: string) => k })
  })

  it('renders phase + stdout + stderr lines in arrival order', () => {
    const events: ElectronStreamCheckEvent[] = [
      { type: 'phase', phase: 'install' },
      { type: 'stdout', line: 'pnpm install running' },
      { type: 'stderr', line: 'WARN: deprecated' },
      { type: 'phase', phase: 'build' },
      { type: 'stdout', line: 'built ok' },
    ]
    render(<DevBuildLogPanel events={events} streaming={true} />)
    const pre = screen.getByTestId('dev-build-log')
    expect(pre.textContent).toContain('── install ──')
    expect(pre.textContent).toContain('pnpm install running')
    expect(pre.textContent).toContain('WARN: deprecated')
    expect(pre.textContent).toContain('── build ──')
    expect(pre.textContent).toContain('built ok')
    const installIdx = pre.textContent!.indexOf('── install ──')
    const buildIdx = pre.textContent!.indexOf('── build ──')
    expect(installIdx).toBeLessThan(buildIdx)
  })

  it('renders waiting placeholder while streaming with no events yet', () => {
    render(<DevBuildLogPanel events={[]} streaming={true} />)
    const pre = screen.getByTestId('dev-build-log')
    expect(pre.textContent).toBe('settings.dev.log.waiting')
  })

  it('disables copy button when log is empty', () => {
    render(<DevBuildLogPanel events={[]} streaming={false} />)
    const btn = screen.getByRole('button', { name: 'settings.dev.log.copy' })
    expect(btn).toHaveProperty('disabled', true)
  })

  it('copies log text via clipboard API', () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      writable: true,
      configurable: true,
    })
    const events: ElectronStreamCheckEvent[] = [
      { type: 'stdout', line: 'hello' },
    ]
    render(<DevBuildLogPanel events={events} streaming={false} />)
    const btn = screen.getByRole('button', { name: 'settings.dev.log.copy' })
    fireEvent.click(btn)
    expect(writeText).toHaveBeenCalledWith('hello')
  })

  it('shows error events with a leading marker', () => {
    const events: ElectronStreamCheckEvent[] = [
      { type: 'error', error: 'build failed: exit 1' },
    ]
    render(<DevBuildLogPanel events={events} streaming={false} />)
    const pre = screen.getByTestId('dev-build-log')
    expect(pre.textContent).toContain('✖ build failed: exit 1')
  })
})

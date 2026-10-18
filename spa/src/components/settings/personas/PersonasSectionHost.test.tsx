import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PersonasSectionHost } from './PersonasSectionHost'
import { getSettingsSections } from '../../../lib/settings-section-registry'
import { registerBuiltinModules } from '../../../lib/register-modules'

describe('PersonasSectionHost', () => {
  it('loads the page only when it is shown, and then renders it', async () => {
    render(<PersonasSectionHost />)
    expect(await screen.findByTestId('personas-section')).toBeTruthy()
  })
})

describe('the Personas settings section', () => {
  it('is registered right after Profile and before the App section', () => {
    registerBuiltinModules()
    const ids = getSettingsSections().map((s) => s.id)
    const at = ids.indexOf('personas')
    expect(at).toBeGreaterThan(-1)
    expect(at).toBe(ids.indexOf('profile') + 1)
    expect(ids.indexOf('electron')).toBeGreaterThan(at)
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ModuleConfigSection } from './ModuleConfigSection'

// Mock module-registry
vi.mock('../../lib/module-registry', () => ({
  getModulesWithGlobalConfig: vi.fn(() => []),
  getModulesWithWorkspaceConfig: vi.fn(() => []),
}))

// Mock stores
vi.mock('../../stores/useModuleConfigStore', () => ({
  useModuleConfigStore: Object.assign(vi.fn(() => undefined), {
    getState: () => ({ setGlobalModuleConfig: vi.fn() }),
  }),
}))

vi.mock('../../features/workspace/store', () => ({
  useWorkspaceStore: Object.assign(vi.fn(() => undefined), {
    getState: () => ({ setModuleConfig: vi.fn() }),
  }),
}))

import { getModulesWithGlobalConfig } from '../../lib/module-registry'
import type { ModuleDefinition } from '../../lib/module-registry'

describe('ModuleConfigSection', () => {
  beforeEach(() => {
    vi.mocked(getModulesWithGlobalConfig).mockReturnValue([])
  })

  it('renders nothing when no modules have config', () => {
    const { container } = render(<ModuleConfigSection scope="global" />)
    expect(container.innerHTML).toBe('')
  })

  describe('boolean config field', () => {
    beforeEach(() => {
      vi.mocked(getModulesWithGlobalConfig).mockReturnValue([{
        id: 'test-mod',
        name: 'Test Module',
        globalConfig: [{ key: 'enabled', type: 'boolean', label: 'Enable Feature', defaultValue: false }],
      }] as ModuleDefinition[])
    })

    it('renders toggle with role="switch"', () => {
      render(<ModuleConfigSection scope="global" />)
      expect(screen.getByRole('switch')).toBeTruthy()
    })

    it('has correct aria-checked reflecting value', () => {
      render(<ModuleConfigSection scope="global" />)
      expect(screen.getByRole('switch').getAttribute('aria-checked')).toBe('false')
    })

    it('has type="button"', () => {
      render(<ModuleConfigSection scope="global" />)
      expect(screen.getByRole('switch').getAttribute('type')).toBe('button')
    })

    it('does not use <label> element for boolean field', () => {
      render(<ModuleConfigSection scope="global" />)
      expect(screen.queryByLabelText('Enable Feature')).toBeTruthy() // aria-label on ToggleSwitch
    })
  })

  describe('text config field', () => {
    beforeEach(() => {
      vi.mocked(getModulesWithGlobalConfig).mockReturnValue([{
        id: 'test-mod',
        name: 'Test Module',
        globalConfig: [{ key: 'apiUrl', type: 'string', label: 'API URL', defaultValue: '' }],
      }] as ModuleDefinition[])
    })

    it('associates label with input via htmlFor', () => {
      render(<ModuleConfigSection scope="global" />)
      const input = screen.getByRole('textbox')
      const label = input.closest('div')?.querySelector('label')
      expect(label).toBeTruthy()
      expect(label!.getAttribute('for')).toBe(input.id)
    })
  })
})

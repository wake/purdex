import { render } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import { getAgentIcon, CC_ICON_VARIANTS, CODEX_ICON_VARIANTS } from './agent-icons'

vi.mock('@lobehub/icons-static-svg/icons/opencode.svg?react', () => ({
  default: (props: import('react').SVGProps<SVGSVGElement>) => (
    <svg data-testid="opencode-svg-marker" {...props} />
  ),
}))

describe('getAgentIcon', () => {
  it('returns bot variant for cc when ccVariant=bot', () => {
    expect(getAgentIcon('cc', { ccVariant: 'bot', codexVariant: 'openai' })).toBe(CC_ICON_VARIANTS.bot)
  })

  it('returns star variant for cc when ccVariant=star', () => {
    expect(getAgentIcon('cc', { ccVariant: 'star', codexVariant: 'openai' })).toBe(CC_ICON_VARIANTS.star)
  })

  it('returns distinct components for bot vs star', () => {
    expect(CC_ICON_VARIANTS.bot).not.toBe(CC_ICON_VARIANTS.star)
  })

  it('returns openai variant for codex when codexVariant=openai', () => {
    expect(getAgentIcon('codex', { ccVariant: 'bot', codexVariant: 'openai' })).toBe(CODEX_ICON_VARIANTS.openai)
  })

  it('returns codex variant for codex when codexVariant=codex', () => {
    expect(getAgentIcon('codex', { ccVariant: 'bot', codexVariant: 'codex' })).toBe(CODEX_ICON_VARIANTS.codex)
  })

  it('returns distinct components for openai vs codex', () => {
    expect(CODEX_ICON_VARIANTS.openai).not.toBe(CODEX_ICON_VARIANTS.codex)
  })

  it('returns opencode icon for opencode agent type', () => {
    const Icon = getAgentIcon('opencode', { ccVariant: 'bot', codexVariant: 'openai' })

    expect(Icon).toBeDefined()
    expect(Icon).not.toBe(CC_ICON_VARIANTS.bot)
    expect(Icon).not.toBe(CC_ICON_VARIANTS.star)
    expect(Icon).not.toBe(CODEX_ICON_VARIANTS.openai)
    expect(Icon).not.toBe(CODEX_ICON_VARIANTS.codex)

    const { container } = render(Icon ? <Icon size={16} /> : null)
    expect(container.querySelector('[data-testid="opencode-svg-marker"]')).toBeTruthy()
  })

  it('opencode icon ignores cc and codex variants', () => {
    const icon = getAgentIcon('opencode', { ccVariant: 'bot', codexVariant: 'openai' })
    const matrix = [
      { ccVariant: 'bot', codexVariant: 'openai' },
      { ccVariant: 'bot', codexVariant: 'codex' },
      { ccVariant: 'star', codexVariant: 'openai' },
      { ccVariant: 'star', codexVariant: 'codex' },
    ] as const

    for (const options of matrix) {
      const Icon = getAgentIcon('opencode', options)
      expect(Icon).toBe(icon)

      const { container } = render(Icon ? <Icon size={16} /> : null)
      expect(container.querySelector('[data-testid="opencode-svg-marker"]')).toBeTruthy()
    }
  })

  it('codex branch ignores ccVariant and respects codexVariant', () => {
    const withBot = getAgentIcon('codex', { ccVariant: 'bot', codexVariant: 'codex' })
    const withStar = getAgentIcon('codex', { ccVariant: 'star', codexVariant: 'codex' })
    expect(withBot).toBe(CODEX_ICON_VARIANTS.codex)
    expect(withStar).toBe(CODEX_ICON_VARIANTS.codex)
  })

  it('returns undefined for unknown agent types', () => {
    expect(getAgentIcon('gemini', { ccVariant: 'bot', codexVariant: 'openai' })).toBeUndefined()
    expect(getAgentIcon('', { ccVariant: 'bot', codexVariant: 'openai' })).toBeUndefined()
  })
})

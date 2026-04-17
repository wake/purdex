import { describe, it, expect } from 'vitest'
import { getAgentIcon, CC_ICON_VARIANTS } from './agent-icons'

describe('getAgentIcon', () => {
  it('returns bot variant for cc when ccVariant=bot', () => {
    expect(getAgentIcon('cc', { ccVariant: 'bot' })).toBe(CC_ICON_VARIANTS.bot)
  })

  it('returns star variant for cc when ccVariant=star', () => {
    expect(getAgentIcon('cc', { ccVariant: 'star' })).toBe(CC_ICON_VARIANTS.star)
  })

  it('returns distinct components for bot vs star', () => {
    expect(CC_ICON_VARIANTS.bot).not.toBe(CC_ICON_VARIANTS.star)
  })

  it('returns the codex icon regardless of ccVariant', () => {
    const codexBot = getAgentIcon('codex', { ccVariant: 'bot' })
    const codexStar = getAgentIcon('codex', { ccVariant: 'star' })
    expect(codexBot).toBeDefined()
    expect(codexBot).toBe(codexStar)
  })

  it('returns undefined for unknown agent types', () => {
    expect(getAgentIcon('gemini', { ccVariant: 'bot' })).toBeUndefined()
    expect(getAgentIcon('', { ccVariant: 'bot' })).toBeUndefined()
  })
})

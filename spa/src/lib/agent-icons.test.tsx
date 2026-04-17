import { describe, it, expect } from 'vitest'
import { getAgentIcon, CC_ICON_VARIANTS } from './agent-icons'

describe('getAgentIcon', () => {
  it('returns bot variant for cc when ccVariant=bot', () => {
    expect(getAgentIcon('cc', 'bot')).toBe(CC_ICON_VARIANTS.bot)
  })

  it('returns star variant for cc when ccVariant=star', () => {
    expect(getAgentIcon('cc', 'star')).toBe(CC_ICON_VARIANTS.star)
  })

  it('returns distinct components for bot vs star', () => {
    expect(CC_ICON_VARIANTS.bot).not.toBe(CC_ICON_VARIANTS.star)
  })

  it('returns the codex icon regardless of ccVariant', () => {
    const codexBot = getAgentIcon('codex', 'bot')
    const codexStar = getAgentIcon('codex', 'star')
    expect(codexBot).toBeDefined()
    expect(codexBot).toBe(codexStar)
  })

  it('returns undefined for unknown agent types', () => {
    expect(getAgentIcon('gemini', 'bot')).toBeUndefined()
    expect(getAgentIcon('', 'bot')).toBeUndefined()
  })
})

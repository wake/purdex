import { render } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { workerIcon } from './worker-icon'
import { CC_ICON_VARIANTS, CODEX_ICON_VARIANTS, CC_COLOR_ICON_VARIANTS, CODEX_COLOR_ICON } from './agent-icons'
import { ICON_MAP } from '../components/tab-icon-map'

const base = { ccVariant: 'bot', codexVariant: 'openai', customIcon: '' } as const

describe('ICON_MAP', () => {
  it('has Robot, the execution pane icon (no empty slot)', () => {
    expect(ICON_MAP.Robot).toBeDefined()
  })
})

describe('workerIcon — mono', () => {
  it('claude follows ccVariant', () => {
    expect(workerIcon('claude', 'mono', base)).toBe(CC_ICON_VARIANTS.bot)
    expect(workerIcon('claude', 'mono', { ...base, ccVariant: 'star' })).toBe(CC_ICON_VARIANTS.star)
  })
  it('codex follows codexVariant', () => {
    expect(workerIcon('codex', 'mono', base)).toBe(CODEX_ICON_VARIANTS.openai)
    expect(workerIcon('codex', 'mono', { ...base, codexVariant: 'codex' })).toBe(CODEX_ICON_VARIANTS.codex)
  })
})

describe('workerIcon — color', () => {
  it('claude → claudecode-color (bot) / claude-color (star)', () => {
    expect(workerIcon('claude', 'color', base)).toBe(CC_COLOR_ICON_VARIANTS.bot)
    expect(workerIcon('claude', 'color', { ...base, ccVariant: 'star' })).toBe(CC_COLOR_ICON_VARIANTS.star)
    expect(CC_COLOR_ICON_VARIANTS.bot).not.toBe(CC_COLOR_ICON_VARIANTS.star)
    expect(CC_COLOR_ICON_VARIANTS.bot).not.toBe(CC_ICON_VARIANTS.bot)
  })
  it('codex → codex-color for both variants (OpenAI has no colour mark)', () => {
    expect(workerIcon('codex', 'color', base)).toBe(CODEX_COLOR_ICON)
    expect(workerIcon('codex', 'color', { ...base, codexVariant: 'codex' })).toBe(CODEX_COLOR_ICON)
    expect(CODEX_COLOR_ICON).not.toBe(CODEX_ICON_VARIANTS.codex)
  })
})

describe('workerIcon — custom', () => {
  it('a known Phosphor name renders that icon, and the component is stable per name', () => {
    const Icon = workerIcon('claude', 'custom', { ...base, customIcon: 'Rocket' })
    expect(Icon).not.toBe(ICON_MAP.Robot)
    expect(workerIcon('codex', 'custom', { ...base, customIcon: 'Rocket' })).toBe(Icon)
    expect(workerIcon('claude', 'custom', { ...base, customIcon: 'Star' })).not.toBe(Icon)
    const { container } = render(<Icon size={16} />)
    expect(container.firstChild).toBeTruthy()
  })
  it("'' (no icon set) falls back to Robot", () => {
    expect(workerIcon('claude', 'custom', base)).toBe(ICON_MAP.Robot)
  })
  it('an unrecognised name falls back to Robot', () => {
    expect(workerIcon('claude', 'custom', { ...base, customIcon: 'NotAnIcon' })).toBe(ICON_MAP.Robot)
    expect(workerIcon('claude', 'custom', { ...base, customIcon: 'x' })).toBe(ICON_MAP.Robot)
  })
})

describe('workerIcon — unknown provider', () => {
  it('→ Robot for mono and color', () => {
    expect(workerIcon('gemini', 'mono', base)).toBe(ICON_MAP.Robot)
    expect(workerIcon('', 'color', base)).toBe(ICON_MAP.Robot)
  })
  it('custom ignores the provider', () => {
    expect(workerIcon('gemini', 'custom', { ...base, customIcon: 'Rocket' })).toBe(workerIcon('claude', 'custom', { ...base, customIcon: 'Rocket' }))
  })
})

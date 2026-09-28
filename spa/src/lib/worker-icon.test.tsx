import { render } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { workerIcon, type WorkerIconOptions } from './worker-icon'
import { CC_ICON_VARIANTS, CODEX_ICON_VARIANTS, CC_COLOR_ICON_VARIANTS, CODEX_COLOR_ICON, getAgentIcon } from './agent-icons'
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

describe('workerIcon — known-but-unlisted provider (opencode)', () => {
  it('mono and color both resolve through providerAgentType to its own logo, not Robot', () => {
    const expected = getAgentIcon('opencode', base)
    expect(workerIcon('opencode', 'mono', base)).toBe(expected)
    // No colour mark for opencode: `color` falls back to its own mono logo, not Robot.
    expect(workerIcon('opencode', 'color', base)).toBe(expected)
    expect(workerIcon('opencode', 'mono', base)).not.toBe(ICON_MAP.Robot)
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

// A Profile Sync payload only checks that ccIconVariant/codexIconVariant are
// strings (not that they're a known variant), so a stale or foreign value can
// reach workerIcon here. It must never return undefined for a recognised
// agentType — fall back to the default variant's icon, not an empty tab icon.
describe('workerIcon — unknown icon variant (review finding A1)', () => {
  it('unknown ccIconVariant falls back to the default variant (bot), mono and color', () => {
    const opts: WorkerIconOptions = { ...base, ccVariant: 'nope-not-a-variant' as WorkerIconOptions['ccVariant'] }
    expect(workerIcon('claude', 'mono', opts)).toBe(CC_ICON_VARIANTS.bot)
    expect(workerIcon('claude', 'color', opts)).toBe(CC_COLOR_ICON_VARIANTS.bot)
  })
  it('unknown codexIconVariant falls back to the default variant (openai), mono and color', () => {
    const opts: WorkerIconOptions = { ...base, codexVariant: 'nope-not-a-variant' as WorkerIconOptions['codexVariant'] }
    expect(workerIcon('codex', 'mono', opts)).toBe(CODEX_ICON_VARIANTS.openai)
    expect(workerIcon('codex', 'color', opts)).toBe(CODEX_COLOR_ICON)
  })
  it('both unknown together still resolves to defined icons, never undefined', () => {
    const opts: WorkerIconOptions = {
      ...base,
      ccVariant: 'x' as WorkerIconOptions['ccVariant'],
      codexVariant: 'y' as WorkerIconOptions['codexVariant'],
    }
    expect(workerIcon('claude', 'mono', opts)).toBeDefined()
    expect(workerIcon('claude', 'color', opts)).toBeDefined()
    expect(workerIcon('codex', 'mono', opts)).toBeDefined()
    expect(workerIcon('codex', 'color', opts)).toBeDefined()
  })
})

// The variant guard used `v in <map>`, which is true for inherited
// Object.prototype keys (`__proto__`, `constructor`, `toString`) even though
// the map has no own property by that name — a synced value like that
// yields a non-component and breaks tab rendering (review finding P2).
// `Object.hasOwn` must be used instead, for every variant map lookup.
describe('workerIcon — prototype-chain variant values (review finding P2)', () => {
  const proto = ['__proto__', 'constructor', 'toString'] as const

  it.each(proto)('ccIconVariant %s falls back to bot, mono and color', (name) => {
    const opts: WorkerIconOptions = { ...base, ccVariant: name as WorkerIconOptions['ccVariant'] }
    expect(workerIcon('claude', 'mono', opts)).toBe(CC_ICON_VARIANTS.bot)
    expect(workerIcon('claude', 'color', opts)).toBe(CC_COLOR_ICON_VARIANTS.bot)
  })

  it.each(proto)('codexIconVariant %s falls back to openai, mono and color', (name) => {
    const opts: WorkerIconOptions = { ...base, codexVariant: name as WorkerIconOptions['codexVariant'] }
    expect(workerIcon('codex', 'mono', opts)).toBe(CODEX_ICON_VARIANTS.openai)
    expect(workerIcon('codex', 'color', opts)).toBe(CODEX_COLOR_ICON)
  })
})

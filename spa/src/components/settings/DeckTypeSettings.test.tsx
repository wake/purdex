// c9d9c7-60: the deck's and the chat's type size / line height. The store clamps and persists, an untouched setting sets nothing (the built-in look
// is the CSS fallback), the sliders show the built-in numbers until moved, the pane roots carry the variables and follow a change at once,
// and every place that used a fixed size now reads the scale.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
import { DeckTypeSettings } from './DeckTypeSettings'
import { DeckPane } from '../deck/DeckPane'
import { ChatPane } from '../deck/ChatPane'
import { UserBlock } from '../deck/UserBlock'
import { deckTypeStyle } from '../../lib/conversations/deck-type'
import { useDeckTypeStore, clampDeckFont, clampDeckLine, DECK_FONT_DEFAULT, DECK_LINE_DEFAULT } from '../../stores/useDeckTypeStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { STORAGE_KEYS } from '../../lib/storage'

// @ts-expect-error __dirname is untyped here.
const css: string = readFileSync(resolve(__dirname, '../../index.css'), 'utf8')

beforeEach(() => {
  cleanup()
  useI18nStore.getState().setLocale('en')
  useDeckTypeStore.setState({ fontSize: null, lineHeight: null })
  localStorage.removeItem(STORAGE_KEYS.DECK_TYPE)
})

describe('the store', () => {
  it('starts on the built-in look (null / null)', () => {
    expect(useDeckTypeStore.getState().fontSize).toBeNull()
    expect(useDeckTypeStore.getState().lineHeight).toBeNull()
  })
  it('clamps the size to 11–18 px whole, and the line height to 1.3–2.0 in steps of 0.05', () => {
    expect([5, 11, 13.4, 13.6, 18, 99].map(clampDeckFont)).toEqual([11, 11, 13, 14, 18, 18])
    expect([0.5, 1.3, 1.65, 1.67, 1.99, 2.0, 9].map(clampDeckLine)).toEqual([1.3, 1.3, 1.65, 1.65, 2, 2, 2])
    useDeckTypeStore.getState().setFontSize(40)
    useDeckTypeStore.getState().setLineHeight(0.1)
    expect(useDeckTypeStore.getState()).toMatchObject({ fontSize: 18, lineHeight: 1.3 })
  })
  it('ignores a value that is not a number', () => {
    useDeckTypeStore.getState().setFontSize(Number.NaN)
    expect(useDeckTypeStore.getState().fontSize).toBeNull()
  })
  it('persists and reads back; reset goes back to null', async () => {
    useDeckTypeStore.getState().setFontSize(15)
    useDeckTypeStore.getState().setLineHeight(1.8)
    const saved = localStorage.getItem(STORAGE_KEYS.DECK_TYPE)!
    expect(JSON.parse(saved).state).toEqual({ fontSize: 15, lineHeight: 1.8 })
    useDeckTypeStore.setState({ fontSize: null, lineHeight: null }) // (this writes nulls back too)
    localStorage.setItem(STORAGE_KEYS.DECK_TYPE, saved) // a reload: what was saved is what is read
    await useDeckTypeStore.persist.rehydrate()
    expect(useDeckTypeStore.getState()).toMatchObject({ fontSize: 15, lineHeight: 1.8 })
    useDeckTypeStore.getState().resetFontSize()
    useDeckTypeStore.getState().resetLineHeight()
    expect(useDeckTypeStore.getState()).toMatchObject({ fontSize: null, lineHeight: null })
  })
  it('a corrupted payload falls back to the built-in look / clamps (never a broken size)', async () => {
    localStorage.setItem(STORAGE_KEYS.DECK_TYPE, JSON.stringify({ state: { fontSize: 'big', lineHeight: 99 }, version: 1 }))
    await useDeckTypeStore.persist.rehydrate()
    expect(useDeckTypeStore.getState()).toMatchObject({ fontSize: null, lineHeight: 2 })
  })
})

describe('the style on the pane root', () => {
  it('sets nothing for an untouched setting, only what was chosen otherwise', () => {
    expect(deckTypeStyle(null, null)).toBeUndefined()
    expect(deckTypeStyle(15, null)).toEqual({ '--deck-font-size': '15px' })
    expect(deckTypeStyle(null, 1.8)).toEqual({ '--deck-line-height': '1.8' })
  })
})

describe('the settings block', () => {
  it('sits on the built-in numbers until moved; Reset is disabled then', () => {
    render(<DeckTypeSettings />)
    expect(screen.getByTestId('deck-type-font-value')).toHaveTextContent(`${DECK_FONT_DEFAULT} px`)
    expect(screen.getByTestId('deck-type-line-value')).toHaveTextContent(DECK_LINE_DEFAULT.toFixed(2))
    expect(screen.getByTestId('deck-type-font-reset')).toBeDisabled()
    expect(screen.getByTestId('deck-type-line-reset')).toBeDisabled()
  })
  it('moving a slider writes the store at once; Reset puts it back', () => {
    render(<DeckTypeSettings />)
    fireEvent.change(screen.getByRole('slider', { name: 'Text size' }), { target: { value: '16' } })
    fireEvent.change(screen.getByRole('slider', { name: 'Line height' }), { target: { value: '1.8' } })
    expect(useDeckTypeStore.getState()).toMatchObject({ fontSize: 16, lineHeight: 1.8 })
    expect(screen.getByTestId('deck-type-font-value')).toHaveTextContent('16 px')
    expect(screen.getByTestId('deck-type-line-value')).toHaveTextContent('1.80')
    fireEvent.click(screen.getByTestId('deck-type-font-reset'))
    fireEvent.click(screen.getByTestId('deck-type-line-reset'))
    expect(useDeckTypeStore.getState()).toMatchObject({ fontSize: null, lineHeight: null })
  })
  it('the sliders carry the allowed ranges', () => {
    render(<DeckTypeSettings />)
    const font = screen.getByRole('slider', { name: 'Text size' })
    const line = screen.getByRole('slider', { name: 'Line height' })
    expect([font.getAttribute('min'), font.getAttribute('max'), font.getAttribute('step')]).toEqual(['11', '18', '1'])
    expect([line.getAttribute('min'), line.getAttribute('max'), line.getAttribute('step')]).toEqual(['1.3', '2', '0.05'])
  })
})

describe('the panes follow the setting live', () => {
  const noop = () => {}
  it('the deck root: no variables by default, then the chosen ones, then none again after Reset', () => {
    render(<DeckPane paneId="p" conversation={{ state: 'off' }} isActive isFocusTarget onSwitchToTerminal={noop} />)
    const root = screen.getByTestId('session-view-deck')
    expect(root.style.getPropertyValue('--deck-font-size')).toBe('')
    act(() => { useDeckTypeStore.getState().setFontSize(16); useDeckTypeStore.getState().setLineHeight(1.8) })
    expect(root.style.getPropertyValue('--deck-font-size')).toBe('16px')
    expect(root.style.getPropertyValue('--deck-line-height')).toBe('1.8')
    act(() => { useDeckTypeStore.getState().resetFontSize(); useDeckTypeStore.getState().resetLineHeight() })
    expect(root.style.getPropertyValue('--deck-font-size')).toBe('')
  })
  it('the chat root too', () => {
    render(<ChatPane paneId="p" title="t" conversation={{ state: 'off' }} isActive isFocusTarget onSwitchToTerminal={noop} />)
    act(() => { useDeckTypeStore.getState().setFontSize(17) })
    expect(screen.getByTestId('session-view-chat').style.getPropertyValue('--deck-font-size')).toBe('17px')
  })
})

describe('what reads the scale', () => {
  it('the built-in look is every fallback: 13px / 1.65 for the body, +1px for user text, -1px for the small rows', () => {
    expect(css).toMatch(/\.deck-t-base \{ font-size: var\(--deck-font-size, 13px\); line-height: var\(--deck-line-height, 1\.65\); \}/)
    expect(css).toMatch(/\.deck-t-up \{ font-size: calc\(var\(--deck-font-size, 13px\) \+ 1px\); line-height: var\(--deck-line-height, 1\.4286\); \}/)
    expect(css).toMatch(/\.deck-t-down \{ font-size: calc\(var\(--deck-font-size, 13px\) - 1px\); line-height: var\(--deck-line-height, 1\.3333\); \}/)
  })
  it('the prose keeps the theme\'s size (minus 1px) and 1.65 as its fallbacks, and code follows the prose (1em)', () => {
    expect(css).toMatch(/line-height: var\(--deck-line-height, 1\.65\);\s*font-size: var\(--deck-font-size, calc\(var\(--wt-font-size\) - 1px\)\);/)
    expect(css).toMatch(/\.worker-prose :where\(code, pre\) \{ font-family: var\(--wt-code-font\); font-size: 1em; \}/)
  })
  it('the user block, the agent head and the rows take the scale', () => {
    render(<UserBlock item={{ type: 'user', id: 'u', at: 1, index: 0, text: 'hi', source: 'user' }} />)
    expect(screen.getByTestId('deck-user-caption')).toHaveClass('deck-t-down')
    expect(screen.getByTestId('deck-user').querySelector('.deck-t-up')).not.toBeNull()
  })
})

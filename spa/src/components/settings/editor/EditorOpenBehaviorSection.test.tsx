import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { EditorOpenBehaviorSection } from './EditorOpenBehaviorSection'
import {
  useEditorSettingsStore,
  DEFAULT_EDITOR_SETTINGS,
} from '../../../stores/useEditorSettingsStore'

beforeEach(() => {
  // merge-mode reset — DO NOT pass `true` (replace mode wipes actions).
  // Per feedback_zustand_harness_setstate: explicit-list every mutable
  // field that this section reads so cross-test leakage between specs
  // can't bleed in (other tests may have flipped the toggles).
  useEditorSettingsStore.setState({
    ...DEFAULT_EDITOR_SETTINGS,
    popupOnMissingFile: true,
    autoSearchLayer1: true,
  })
})

// `ToggleSwitch` renders <button role="switch" aria-checked> (see
// ToggleSwitch.tsx) — find by accessible name to keep this test resilient
// to label text re-wording.
const POPUP_RE = /missing file|不存在.*檔案|彈窗/i
const AUTO_RE = /auto.*layer.?1|自動.*快取/i

describe('EditorOpenBehaviorSection', () => {
  it('renders both toggles wired to the store defaults (both true)', () => {
    render(<EditorOpenBehaviorSection />)
    const popup = screen.getByRole('switch', { name: POPUP_RE })
    const auto = screen.getByRole('switch', { name: AUTO_RE })
    expect(popup.getAttribute('aria-checked')).toBe('true')
    expect(auto.getAttribute('aria-checked')).toBe('true')
  })

  it('toggling popupOnMissingFile updates the store', () => {
    render(<EditorOpenBehaviorSection />)
    fireEvent.click(screen.getByRole('switch', { name: POPUP_RE }))
    expect(useEditorSettingsStore.getState().popupOnMissingFile).toBe(false)
  })

  it('toggling autoSearchLayer1 updates the store', () => {
    render(<EditorOpenBehaviorSection />)
    fireEvent.click(screen.getByRole('switch', { name: AUTO_RE }))
    expect(useEditorSettingsStore.getState().autoSearchLayer1).toBe(false)
  })
})

import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { listContributions } from '../../../lib/settings-contribution-registry'
import { editorModuleDefinition } from '../../../lib/register-modules/editor-module'
import {
  clearAllBuiltinModuleRegistries,
  resetAndRegisterBuiltinModules,
  resetModuleEnabledStore,
} from '../../../lib/__tests__/test-bootstrap-harness'
import { EditorPurdexSettingsSection } from '../EditorPurdexSettingsSection'
import { useEditorSettingsStore } from '../../../stores/useEditorSettingsStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'

// Spec §4.2 — PR-2 collapses three sidebar entries (open-behavior /
// link-detect / editor) into a single Editor page that hosts all three
// segments inline. The two segment components themselves
// (EditorOpenBehaviorSection, EditorLinkDetectionSection) are reused
// verbatim — we just stop registering them as standalone contributions.

beforeEach(() => {
  resetAndRegisterBuiltinModules()
})

afterEach(() => {
  clearAllBuiltinModuleRegistries()
  resetModuleEnabledStore()
})

describe('Editor consolidation (spec §4.2)', () => {
  it('purdex contributions no longer include open-behavior / link-detect', () => {
    const ids = listContributions('purdex').map((c) => c.localId)
    expect(ids).not.toContain('open-behavior')
    expect(ids).not.toContain('link-detect')
    // The single canonical Editor entry remains.
    expect(ids).toContain('editor')
  })

  it('editorModuleDefinition.settings keeps only the editor purdex entry plus workspace + host scopes', () => {
    const purdex = editorModuleDefinition.settings!.filter((s) => s.scope === 'purdex')
    expect(purdex).toHaveLength(1)
    expect(purdex[0].localId).toBe('editor')

    const workspace = editorModuleDefinition.settings!.filter((s) => s.scope === 'workspace')
    expect(workspace).toHaveLength(1)
    expect(workspace[0].localId).toBe('workspace-home-path')

    const host = editorModuleDefinition.settings!.filter((s) => s.scope === 'host')
    expect(host).toHaveLength(1)
    expect(host[0].localId).toBe('host-home-path')
  })

  it('EditorPurdexSettingsSection renders Monaco header + Open Behavior + Link Detection segments', () => {
    render(<EditorPurdexSettingsSection ctx={{ scope: 'purdex' }} />)

    // The page-level h2 (Editor title).
    const pageHeadings = screen.getAllByRole('heading', { level: 2 })
    expect(pageHeadings.length).toBe(1)
    expect(pageHeadings[0].textContent).toMatch(/Editor/)

    // Two h3 segment headings (Open Behavior + Link Detection).
    const subHeadings = screen.getAllByRole('heading', { level: 3 })
    const headingTexts = subHeadings.map((h) => h.textContent ?? '')
    expect(subHeadings.length).toBe(2)
    expect(headingTexts.some((t) => /Open Behavior/i.test(t))).toBe(true)
    expect(headingTexts.some((t) => /File path link detection|link detection/i.test(t))).toBe(true)
  })

  it('Open Behavior toggle (popupOnMissingFile) writes to useEditorSettingsStore', () => {
    useEditorSettingsStore.setState({ popupOnMissingFile: false })
    render(<EditorPurdexSettingsSection ctx={{ scope: 'purdex' }} />)

    // Find the Open Behavior <h3> and its enclosing <div> segment.
    const openBehaviorHeading = screen
      .getAllByRole('heading', { level: 3 })
      .find((h) => /Open Behavior/i.test(h.textContent ?? ''))!
    const segment = openBehaviorHeading.closest('div')!

    // First toggle within the segment is the popup-on-missing-file switch.
    const toggle = within(segment).getAllByRole('switch')[0]
    fireEvent.click(toggle)
    expect(useEditorSettingsStore.getState().popupOnMissingFile).toBe(true)
  })

  it('Link Detection toggle (linkDetectAbsolute) writes to useUISettingsStore', () => {
    // Default for linkDetectAbsolute is `true`; flip to false first so the
    // click below produces an observable transition.
    useUISettingsStore.setState({ linkDetectAbsolute: false })
    render(<EditorPurdexSettingsSection ctx={{ scope: 'purdex' }} />)

    const linkHeading = screen
      .getAllByRole('heading', { level: 3 })
      .find((h) => /link detection|File path link detection/i.test(h.textContent ?? ''))!
    const segment = linkHeading.closest('div')!

    const absoluteToggle = within(segment).getAllByRole('switch')[0]
    fireEvent.click(absoluteToggle)
    expect(useUISettingsStore.getState().linkDetectAbsolute).toBe(true)
  })

  it('outer wrapper has no p-6 (relies on GlobalSettingsPage padding)', () => {
    const { container } = render(<EditorPurdexSettingsSection ctx={{ scope: 'purdex' }} />)
    const outer = container.firstElementChild as HTMLElement
    expect(outer.className).not.toMatch(/\bp-6\b/)
  })
})

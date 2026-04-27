import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { registerBuiltinModules } from '../register-modules'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { clearAllForHmr, getDefaultOpener } from '../file-opener-registry'
import { clearModuleRegistry, resolvePaneRenderer } from '../module-registry'
import { clearContributions } from '../settings-contribution-registry'
import { PaneLayoutRenderer } from '../../components/PaneLayoutRenderer'
import type { PaneLayout } from '../../types/tab'

const txtFile = { name: 'a.txt', path: '/a.txt', extension: 'txt', size: 1, isDirectory: false }

const editorLeaf: PaneLayout = {
  type: 'leaf',
  pane: { id: 'p1', content: { kind: 'editor', source: { type: 'inapp' }, filePath: '/a.txt' } as never },
}

beforeEach(() => {
  cleanup()
  clearContributions()
  clearModuleRegistry()
  clearAllForHmr()
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  registerBuiltinModules()
})

afterEach(() => {
  cleanup()
  clearContributions()
  clearModuleRegistry()
  clearAllForHmr()
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
})

describe('Editor disable flow (end-to-end)', () => {
  it('text file opens via monaco-editor when editor is enabled (default)', () => {
    expect(getDefaultOpener(txtFile)?.id).toBe('monaco-editor')
  })

  it('disabling editor and re-bootstrapping leaves no opener for text files', () => {
    useModuleEnabledStore.getState().setEnabled('editor', false)
    clearContributions()
    clearModuleRegistry()
    clearAllForHmr()
    registerBuiltinModules()
    expect(getDefaultOpener(txtFile)).toBeNull()
  })

  it('disabling editor reports kind=disabled for the editor pane', () => {
    useModuleEnabledStore.getState().setEnabled('editor', false)
    const r = resolvePaneRenderer('editor', useModuleEnabledStore.getState().isEnabled)
    expect(r.kind).toBe('disabled')
    if (r.kind === 'disabled') {
      expect(r.moduleId).toBe('editor')
      expect(r.paneKind).toBe('editor')
    }
  })

  it('PaneLayoutRenderer renders the placeholder enable button when editor is disabled', () => {
    useModuleEnabledStore.getState().setEnabled('editor', false)
    render(<PaneLayoutRenderer layout={editorLeaf} tabId="t1" isActive={true} />)
    expect(screen.getByRole('button', { name: /editor/i })).toBeInTheDocument()
  })

  it('re-enabling editor through the placeholder restores the monaco-editor opener after bootstrap', () => {
    useModuleEnabledStore.getState().setEnabled('editor', false)
    clearContributions()
    clearModuleRegistry()
    clearAllForHmr()
    registerBuiltinModules()
    expect(getDefaultOpener(txtFile)).toBeNull()

    useModuleEnabledStore.getState().setEnabled('editor', true)
    clearContributions()
    clearModuleRegistry()
    clearAllForHmr()
    registerBuiltinModules()
    expect(getDefaultOpener(txtFile)?.id).toBe('monaco-editor')
  })
})

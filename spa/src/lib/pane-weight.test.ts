import { describe, it, expect } from 'vitest'
import { isLightTab } from './pane-weight'
import type { PaneLayout, PaneContent } from '../types/tab'

function leaf(content: PaneContent, id = 'p'): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

const editor: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/a.md' }
const terminal: PaneContent = {
  kind: 'tmux-session', hostId: 'h', sessionCode: 's', mode: 'terminal', cachedName: '', tmuxInstance: '',
}
const browser: PaneContent = { kind: 'browser', url: 'https://x' }
const image: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/a.png' }

describe('isLightTab', () => {
  it('a single editor / preview / settings pane is light', () => {
    expect(isLightTab(leaf(editor))).toBe(true)
    expect(isLightTab(leaf(image))).toBe(true)
    expect(isLightTab(leaf({ kind: 'settings', scope: 'global' }))).toBe(true)
    expect(isLightTab(leaf({ kind: 'new-tab' }))).toBe(true)
  })

  it('a terminal or browser pane is heavy (not light)', () => {
    expect(isLightTab(leaf(terminal))).toBe(false)
    expect(isLightTab(leaf(browser))).toBe(false)
  })

  it('background-working / unknown kinds are heavy (allowlist: not always-alive)', () => {
    // memory-monitor polls while mounted → must be bounded, not always alive.
    expect(isLightTab(leaf({ kind: 'memory-monitor' }))).toBe(false)
    // editor-buffers (storage pane) + hosts do their own fetching → heavy.
    expect(isLightTab(leaf({ kind: 'editor-buffers' }))).toBe(false)
    expect(isLightTab(leaf({ kind: 'hosts' }))).toBe(false)
  })

  it('a split is heavy if ANY leaf is heavy', () => {
    const editorPlusTerminal: PaneLayout = {
      type: 'split', id: 'sp', direction: 'h', sizes: [50, 50],
      children: [leaf(editor, 'a'), leaf(terminal, 'b')],
    }
    expect(isLightTab(editorPlusTerminal)).toBe(false)
  })

  it('a split of only light leaves is light', () => {
    const twoEditors: PaneLayout = {
      type: 'split', id: 'sp', direction: 'v', sizes: [50, 50],
      children: [leaf(editor, 'a'), leaf(image, 'b')],
    }
    expect(isLightTab(twoEditors)).toBe(true)
  })
})

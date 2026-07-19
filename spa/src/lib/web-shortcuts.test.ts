import { describe, it, expect } from 'vitest'
import { matchWebShortcut, isEditableTarget, WEB_SHORTCUTS } from './web-shortcuts'
import { getDefaultKeybindings } from '../../../electron/keybindings-manifest'

function ev(over: Partial<KeyboardEvent>): Pick<KeyboardEvent,'key'|'metaKey'|'ctrlKey'|'altKey'|'shiftKey'> {
  return { key: '', metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...over }
}

describe('matchWebShortcut', () => {
  it('Cmd+, → open-settings（非 global）', () => {
    const m = matchWebShortcut(ev({ key: ',', metaKey: true }))
    expect(m?.action).toBe('open-settings')
    expect(m?.global).toBeFalsy()
  })
  it('Ctrl+, → open-settings（跨平台 primary）', () => {
    expect(matchWebShortcut(ev({ key: ',', ctrlKey: true }))?.action).toBe('open-settings')
  })
  it('Cmd+Alt+ArrowRight → next-tab（global）', () => {
    const m = matchWebShortcut(ev({ key: 'ArrowRight', metaKey: true, altKey: true }))
    expect(m?.action).toBe('next-tab')
    expect(m?.global).toBe(true)
  })
  it('Cmd+Alt+1 → switch-workspace-1（global）', () => {
    expect(matchWebShortcut(ev({ key: '1', metaKey: true, altKey: true }))?.action).toBe('switch-workspace-1')
  })
  it('Cmd+Shift+H → open-hosts（字母大小寫不敏感）', () => {
    expect(matchWebShortcut(ev({ key: 'H', metaKey: true, shiftKey: true }))?.action).toBe('open-hosts')
  })
  it('無 primary → null', () => {
    expect(matchWebShortcut(ev({ key: ',' }))).toBeNull()
  })
  it('browser-reserved Cmd+T 不在 manifest → null', () => {
    expect(matchWebShortcut(ev({ key: 't', metaKey: true }))).toBeNull()
  })
})

describe('isEditableTarget', () => {
  it('textarea（含 xterm helper）→ true', () => {
    const ta = document.createElement('textarea')
    expect(isEditableTarget(ta)).toBe(true)
  })
  it('div → false', () => {
    expect(isEditableTarget(document.createElement('div'))).toBe(false)
  })
  it('contentEditable div → true', () => {
    const d = document.createElement('div'); d.contentEditable = 'true'
    // jsdom: isContentEditable 需元素在 document 且屬性生效
    Object.defineProperty(d, 'isContentEditable', { value: true })
    expect(isEditableTarget(d)).toBe(true)
  })
})

describe('parity with Electron canonical actions', () => {
  it('每個 web action 都存在於 electron keybindings 的 action 集合', () => {
    const electronActions = new Set(getDefaultKeybindings().map((k) => k.action))
    for (const s of WEB_SHORTCUTS) {
      expect(electronActions.has(s.action), `web action ${s.action} missing in electron keybindings`).toBe(true)
    }
  })
})

import { describe, it, expect } from 'vitest'
import { findRestoreConflicts } from './restore-guard'
import { bufferKey } from '../editor-buffer-key'
import type { Tab, PaneContent } from '../../types/tab'
import type { FileSource } from '../../types/fs'

let seq = 0
function tabWith(content: PaneContent, opts?: { locked?: boolean }): Tab {
  seq += 1
  return {
    id: `tab-${seq}`,
    pinned: false,
    locked: opts?.locked ?? false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `pane-${seq}`, content } },
  }
}

function editorPane(source: FileSource, filePath: string): PaneContent {
  return { kind: 'editor', source, filePath }
}

const inapp: FileSource = { type: 'inapp' }
const daemon: FileSource = { type: 'daemon', hostId: 'h1' }

describe('findRestoreConflicts', () => {
  it('blocks on a dirty inapp editor buffer', () => {
    const tab = tabWith(editorPane(inapp, '/buffer/a.md'))
    const conflicts = findRestoreConflicts({
      tabs: { [tab.id]: tab },
      buffers: { [bufferKey(inapp, '/buffer/a.md')]: { isDirty: true } },
    })
    expect(conflicts).toEqual([{ type: 'dirty', tabId: tab.id, filePath: '/buffer/a.md' }])
  })

  it('blocks on a locked tab containing an inapp editor pane (even when clean)', () => {
    const tab = tabWith(editorPane(inapp, '/buffer/a.md'), { locked: true })
    const conflicts = findRestoreConflicts({
      tabs: { [tab.id]: tab },
      buffers: { [bufferKey(inapp, '/buffer/a.md')]: { isDirty: false } },
    })
    expect(conflicts).toEqual([{ type: 'locked', tabId: tab.id, filePath: '/buffer/a.md' }])
  })

  it('allows a clean, unlocked inapp editor (no conflicts)', () => {
    const tab = tabWith(editorPane(inapp, '/buffer/a.md'))
    const conflicts = findRestoreConflicts({
      tabs: { [tab.id]: tab },
      buffers: { [bufferKey(inapp, '/buffer/a.md')]: { isDirty: false } },
    })
    expect(conflicts).toEqual([])
  })

  it('does NOT block on a dirty NON-inapp (daemon) buffer', () => {
    const tab = tabWith(editorPane(daemon, '/x/y.md'))
    const conflicts = findRestoreConflicts({
      tabs: { [tab.id]: tab },
      buffers: { [bufferKey(daemon, '/x/y.md')]: { isDirty: true } },
    })
    expect(conflicts).toEqual([])
  })

  it('reports both dirty and locked for a locked tab with a dirty inapp pane', () => {
    const tab = tabWith(editorPane(inapp, '/buffer/a.md'), { locked: true })
    const conflicts = findRestoreConflicts({
      tabs: { [tab.id]: tab },
      buffers: { [bufferKey(inapp, '/buffer/a.md')]: { isDirty: true } },
    })
    expect(conflicts).toContainEqual({ type: 'dirty', tabId: tab.id, filePath: '/buffer/a.md' })
    expect(conflicts).toContainEqual({ type: 'locked', tabId: tab.id, filePath: '/buffer/a.md' })
  })
})

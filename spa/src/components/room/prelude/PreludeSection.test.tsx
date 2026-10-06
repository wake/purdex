import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, within, waitFor } from '@testing-library/react'
import { useEffect, type ReactNode } from 'react'
import { FoldContext, useFoldMemory, type FoldStore } from '../fold-context'
import PreludeSection from './PreludeSection'
import { derivePrelude, type PreludeView } from '../../../lib/nex/prelude'
import { sanitizePreludePage, type PreludeItem } from '../../../lib/nex/prelude-wire'
import type { StreamMessage } from '../../../lib/nex/message-types'
import type { EventsPage, ExecutionSummary, NexEvent } from '../../../lib/nex/types'
import type { Stint } from '../../../lib/nex/entity-stints'
import { costSummary } from '../../../lib/nex/cost-summary'
import { enrichFromEvents } from '../../../lib/nex/stint-enrichment'
import { formatTokens, formatUsd } from '../../../lib/nex/format-cost'
import { formatDuration } from '../../../lib/nex/format-duration'
import { createStintEnrichmentCache, type StintEnrichmentCache } from '../../../lib/nex/stint-enrichment-cache'
import { StintEnrichmentContext } from '../../../hooks/useStintEnrichment'
import ChatTranscript from '../../chat/ChatTranscript'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { AttachmentSourceContext } from '../attachment-source'
import * as api from '../../../lib/nex/nex-api'
import golden from '../../../lib/nex/__fixtures__/prelude-golden-nexen.json'
import real from '../../../lib/nex/__fixtures__/prelude-06GGS8J1YKZCPF4BRXZTX764F4.json'

// Only the attachment fetch is replaced (the thumbnails' tests read which execution it asks); nothing else here calls it.
vi.mock('../../../lib/nex/nex-api', async (importOriginal) => ({ ...(await importOriginal<typeof import('../../../lib/nex/nex-api')>()), fetchAttachment: vi.fn() }))

const m = (pos: string, type: 'user' | 'assistant', content: unknown[]): PreludeItem =>
  ({ offset: null, pos, at: 1, kind: type, msg: { type, parent_tool_use_id: null, message: { role: type, content, stop_reason: null } } as unknown as StreamMessage })

let observed: Array<(entries: Array<{ isIntersecting: boolean }>) => void> = []
let disconnects = 0
beforeEach(() => {
  observed = []
  disconnects = 0
  vi.stubGlobal('IntersectionObserver', class {
    constructor(cb: (e: Array<{ isIntersecting: boolean }>) => void) { observed.push(cb) }
    observe() {}
    disconnect() { disconnects++ }
  })
})
afterEach(() => vi.unstubAllGlobals())

const base = { hostId: 'h', keyPrefix: 'exc', mode: 'room' as const, onLoadOlder: vi.fn(), onRetry: vi.fn(), error: null, pages: 1 }

describe('PreludeSection', () => {
  it('draws markers, user lines, prose and notes in order', () => {
    const view = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      m('2', 'user', [{ type: 'text', text: 'fix the build' }]),
      m('3', 'assistant', [{ type: 'text', text: 'on it' }]),
      { offset: null, pos: '4', at: 0, kind: 'prelude.note', source: 'command_output', text: 'Model set to opus', truncated: false, totalBytes: null, stream: null },
      { offset: null, pos: '5', at: 0, kind: 'prelude.compaction', trigger: 'auto' },
      { offset: null, pos: '6', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    const text = screen.getByTestId('worker-prelude').textContent ?? ''
    expect(text.indexOf('In the terminal')).toBeLessThan(text.indexOf('fix the build'))
    expect(text.indexOf('fix the build')).toBeLessThan(text.indexOf('on it'))
    expect(text).toContain('Model set to opus')
    expect(text).toContain('Conversation compacted here (auto)')
    expect(text).toContain('Headless (worker)')
    expect(document.querySelector('[data-search-unit="p2:0:text"]')).not.toBeNull()
  })

  it('asks for older pages when the sentinel shows, only while ok and not done', () => {
    const onLoadOlder = vi.fn()
    const view = derivePrelude([m('2', 'user', [{ type: 'text', text: 'x' }])])
    const { rerender } = render(<PreludeSection {...base} onLoadOlder={onLoadOlder} view={view} status="ok" done={false} />)
    observed.at(-1)!([{ isIntersecting: true }])
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
    rerender(<PreludeSection {...base} onLoadOlder={onLoadOlder} view={view} status="ok" done />)
    expect(screen.queryByTestId('prelude-sentinel')).toBeNull()
    expect(disconnects).toBe(1)
  })

  it('an error unmounts the sentinel and disconnects it (Review Focus 2)', () => {
    const view = derivePrelude([m('2', 'user', [{ type: 'text', text: 'x' }])])
    const { rerender } = render(<PreludeSection {...base} view={view} status="ok" done={false} />)
    rerender(<PreludeSection {...base} view={view} status="error" error="stuck" done={false} />)
    expect(screen.queryByTestId('prelude-sentinel')).toBeNull()
    expect(disconnects).toBe(1)
  })

  it('re-arms after every page — even one with no items — so a short prelude keeps loading', () => {
    const onLoadOlder = vi.fn()
    const view = derivePrelude([m('2', 'user', [{ type: 'text', text: 'x' }])])
    const { rerender } = render(<PreludeSection {...base} onLoadOlder={onLoadOlder} view={view} status="ok" done={false} pages={1} />)
    expect(observed).toHaveLength(1)
    // A page that brought no items still counts: same view, pages 2.
    rerender(<PreludeSection {...base} onLoadOlder={onLoadOlder} view={view} status="ok" done={false} pages={2} />)
    expect(observed).toHaveLength(2)
    observed[1]([{ isIntersecting: true }])        // the fresh observer's first report: still in view
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
  })

  it('shows loading, error with retry, and gone', () => {
    const onRetry = vi.fn()
    const view = derivePrelude([])
    const { rerender } = render(<PreludeSection {...base} view={view} status="loading" done={false} />)
    expect(screen.getByTestId('prelude-loading')).toBeTruthy()
    rerender(<PreludeSection {...base} onRetry={onRetry} view={view} status="error" error="net" done={false} />)
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalled()
    rerender(<PreludeSection {...base} view={view} status="gone" done />)
    expect(screen.getByTestId('prelude-gone')).toBeTruthy()
  })

  it('renders nothing for idle / none with no items', () => {
    const { container, rerender } = render(<PreludeSection {...base} view={derivePrelude([])} status="idle" done={false} />)
    expect(container.firstChild).toBeNull()
    rerender(<PreludeSection {...base} view={derivePrelude([])} status="none" done />)
    expect(container.firstChild).toBeNull()
  })

  it('draws omitted images / documents as placeholders in user and assistant content', () => {
    const view = derivePrelude([
      m('2', 'user', [{ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 122880 } }]),
      m('3', 'assistant', [{ type: 'document', source: { type: 'omitted', media_type: 'application/pdf', bytes: 5 * 1024 * 1024 } }]),
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    expect(screen.getByText('[image · png · 120 KB]')).toBeTruthy()
    expect(screen.getByText('[document · pdf · 5.0 MB]')).toBeTruthy()
  })

  it('a Read result whose transcript block carries an omitted image shows no raw JSON (#1629)', () => {
    const view = derivePrelude([
      m('2', 'assistant', [{ type: 'tool_use', id: 'r1', name: 'Read', input: { file_path: '/a.png' } }]),
      m('3', 'user', [{ type: 'tool_result', tool_use_id: 'r1', content: [{ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 80 } }] }]),
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    expect(document.body.textContent).not.toContain('omitted')
    expect(document.body.textContent).not.toContain('"type"')
    expect(screen.getByText('[image · png · 80 B]')).toBeTruthy()
    expect(screen.queryByTestId('op-non-text')).toBeNull()
  })

  it('a placeholder without a byte count draws no size', () => {
    const view = derivePrelude([m('2', 'user', [{ type: 'image', source: { type: 'omitted', media_type: 'image/png' } }])])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    expect(screen.getByText('[image · png]')).toBeTruthy()
  })

  it('every cut block and every cut note says so', () => {
    const view = derivePrelude([
      m('2', 'assistant', [
        { type: 'text', text: 'long…', truncated: true, total_bytes: 200000 },
        { type: 'thinking', thinking: 'mulling', truncated: true, total_bytes: 100000 },
        { type: 'tool_use', id: 't', name: 'Write', input: { content: 'x' }, truncated: true, total_bytes: 90000 },
      ]),
      m('3', 'user', [{ type: 'tool_result', tool_use_id: 't', content: 'out', truncated: true, total_bytes: 80000 }]),
      { offset: null, pos: '4', at: 0, kind: 'prelude.note', source: 'command_output', text: 'big', truncated: true, totalBytes: 70000, stream: null },
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    const hints = screen.getAllByTestId('prelude-truncated').map((h) => h.textContent)
    expect(hints).toHaveLength(5)
    expect(hints[0]).toContain('195 KB')
    expect(hints[4]).toContain('68 KB')
  })

  it('a bash stderr note is drawn in the error tone', () => {
    const view = derivePrelude([{ offset: null, pos: '4', at: 0, kind: 'prelude.note', source: 'bash_output', text: 'boom', truncated: false, totalBytes: null, stream: 'stderr' }])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    expect(screen.getByTestId('prelude-note-bash_output').innerHTML).toContain('text-status-error')
  })

  it('hostile blocks that passed the sanitiser still render (Review Focus 5)', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [{ pos: '9', kind: 'assistant', at: 1, payload: { type: 'assistant', message: { role: 'assistant', content: [
        { type: 'tool_use', id: '__proto__', name: 'Bash', input: 'x' },
        { type: 'text', text: 42 },
        { type: 'mystery', blob: [1, 2] },
      ] } } }, { pos: '10', kind: 'user', at: 1, payload: { type: 'user', message: { role: 'user', content: [
        { type: 'tool_result', tool_use_id: '__proto__', content: { not: 'an array' } },
        { type: 'image', source: { type: 'omitted' } },
      ] } } }],
    })!
    expect(() => render(<PreludeSection {...base} view={derivePrelude(page.items)} status="ok" done />)).not.toThrow()
  })
})

const note = (pos: string, source: string, text: string, extra: Record<string, unknown> = {}): PreludeItem =>
  ({ pos, at: 0, kind: 'prelude.note', source, text, truncated: false, totalBytes: null, stream: null, ...extra }) as unknown as PreludeItem

describe('PreludeSection notes and labels', () => {
  it('a bash_input line keeps "! " outside its anchor, which holds only the text', () => {
    render(<PreludeSection {...base} view={derivePrelude([note('7', 'bash_input', 'ls')])} status="ok" done />)
    expect(screen.getByTestId('prelude-bash-input').textContent).toContain('! ls')
    expect(document.querySelector('[data-search-unit="p7:note:text"]')!.textContent).toBe('ls')
  })

  it('a task notification is a one-liner under its label', () => {
    render(<PreludeSection {...base} view={derivePrelude([note('7', 'task_notification', 'build done')])} status="ok" done />)
    const el = screen.getByTestId('prelude-task')
    expect(el.textContent).toContain('Background task: build done')
    expect(el.querySelector('[data-search-unit="p7:note:text"]')!.textContent).toBe('build done')
  })

  it('a peer message is labelled, markdown, and not folded', () => {
    const long = Array.from({ length: 60 }, (_, i) => `line ${i}`).join('\n\n')
    render(<PreludeSection {...base} view={derivePrelude([note('7', 'peer_message', `**bold**\n\n${long}`)])} status="ok" done />)
    const el = screen.getByTestId('prelude-note-peer_message')
    expect(el.textContent).toContain('Peer message')
    expect(el.querySelector('strong')!.textContent).toBe('bold')
    expect(screen.queryByTestId('fold-more')).toBeNull()
    expect(el.textContent).toContain('line 59')
    expect(el.querySelector('[data-search-unit="p7:note:text"]')).not.toBeNull()
  })

  it('an unknown note source is drawn muted, known ones are not', () => {
    render(<PreludeSection {...base} view={derivePrelude([note('7', 'mystery', 'huh'), note('8', 'command_output', 'ok')])} status="ok" done />)
    expect(screen.getByTestId('prelude-note-mystery').innerHTML).toContain('text-text-muted')
    expect(screen.getByTestId('prelude-note-mystery').innerHTML).not.toContain('text-text-secondary')
    expect(screen.getByTestId('prelude-note-command_output').innerHTML).toContain('text-text-secondary')
  })

  it('compaction manual and plain labels, and a raw entrypoint fallback', () => {
    render(<PreludeSection {...base} status="ok" done view={derivePrelude([
      { pos: '1', at: 0, kind: 'prelude.compaction', trigger: 'manual' },
      { pos: '2', at: 0, kind: 'prelude.compaction', trigger: null },
      { pos: '3', at: 0, kind: 'prelude.segment', entrypoint: 'vscode' },
    ] as unknown as PreludeItem[])} />)
    const text = screen.getByTestId('worker-prelude').textContent ?? ''
    expect(text).toContain('Conversation compacted here (manual)')
    expect(text).toContain('Conversation compacted here')
    expect(screen.getAllByTestId('prelude-compaction').map((e) => e.getAttribute('aria-label'))).toContain('Conversation compacted here')
    expect(screen.getByRole('separator', { name: 'vscode' })).toBeTruthy()
  })

  it('a note folds under the key p<pos>:note: expanding it shows the whole output', () => {
    const held: { store?: FoldStore } = {}
    function Wrapper({ children }: { children: ReactNode }) {
      const store = useFoldMemory()
      useEffect(() => { held.store = store }, [store])
      return <FoldContext.Provider value={store}>{children}</FoldContext.Provider>
    }
    const text = Array.from({ length: 80 }, (_, i) => `row ${i}`).join('\n')
    render(<Wrapper><PreludeSection {...base} view={derivePrelude([note('4', 'command_output', text)])} status="ok" done /></Wrapper>)
    expect(screen.getByTestId('fold-more')).toBeTruthy()
    expect(screen.getByTestId('fold-body').textContent).not.toContain('row 79')
    act(() => held.store!.expand(['p4:note']))
    expect(screen.getByTestId('fold-body').textContent).toContain('row 79')
  })

  it('a cut block or note without a usable total omits "of …"; the tool_use hint measures the serialized input', () => {
    const view = derivePrelude([
      m('2', 'assistant', [
        { type: 'text', text: 'cut', truncated: true },
        { type: 'tool_use', id: 't', name: 'Write', input: { content: 'x' }, truncated: true, total_bytes: 90000 },
      ]),
      note('4', 'command_output', 'big', { truncated: true, totalBytes: null }),
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    const hints = screen.getAllByTestId('prelude-truncated').map((h) => h.textContent ?? '')
    expect(hints).toHaveLength(3)
    expect(hints[0]).toBe('Too long — showing the first 3 B')
    expect(hints[1]).toBe('Too long — showing the first 15 B of 88 KB')   // {"content":"x"}
    expect(hints[2]).toBe('Too long — showing the first 3 B')
  })

  it('chat: your lines are bubbles, an agent turn’s tools collapse into one line', () => {
    const view = derivePrelude([
      m('2', 'user', [{ type: 'text', text: 'run it' }]),
      m('3', 'assistant', [{ type: 'tool_use', id: 't1', name: 'Bash', input: { command: 'ls' } }]),
      m('4', 'user', [{ type: 'tool_result', tool_use_id: 't1', content: 'a\nb' }]),
      m('5', 'assistant', [{ type: 'text', text: 'done' }]),
    ])
    render(<PreludeSection {...base} mode="chat" view={view} status="ok" done />)
    expect(document.querySelector('[data-search-unit="p2:0:text"]')).not.toBeNull()
    expect(screen.getAllByTestId('chat-tools-line')).toHaveLength(1)
    expect(screen.getByText('done')).toBeTruthy()
  })

  it('chat: an omitted image sits in a user bubble; a cut text block gets its hint', () => {
    const view = derivePrelude([
      m('2', 'user', [{ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 122880 } }]),
      m('3', 'assistant', [{ type: 'text', text: 'abc', truncated: true, total_bytes: 90000 }]),
    ])
    render(<PreludeSection {...base} mode="chat" view={view} status="ok" done />)
    const bubble = screen.getByTestId('chat-bubble-user')
    expect(bubble.textContent).toContain('[image · png · 120 KB]')
    expect(screen.getByTestId('prelude-truncated').textContent).toBe('Too long — showing the first 3 B of 88 KB')
  })

  it('chat: markers and notes keep entry order around the spans', () => {
    const view = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      m('2', 'user', [{ type: 'text', text: 'first' }]),
      note('3', 'command_output', 'Model set'),
      m('4', 'assistant', [{ type: 'text', text: 'last' }]),
    ])
    render(<PreludeSection {...base} mode="chat" view={view} status="ok" done />)
    const text = screen.getByTestId('worker-prelude').textContent ?? ''
    const at = ['In the terminal', 'first', 'Model set', 'last'].map((x) => text.indexOf(x))
    expect(at.every((x) => x >= 0)).toBe(true)
    expect(at).toEqual([...at].sort((x, y) => x - y))
    // Chat-only: the agent's prose sits in a bubble.
    expect(screen.getByTestId('chat-bubble-agent').textContent).toContain('last')
  })
})

describe('PreludeSection chat form', () => {
  const chat = { ...base, mode: 'chat' as const }
  const call = (id: string, extra: Record<string, unknown> = {}) => ({ type: 'tool_use', id, name: 'Bash', input: { command: 'ls' }, ...extra })
  const result = (id: string, content: string, extra: Record<string, unknown> = {}) => ({ type: 'tool_result', tool_use_id: id, content, ...extra })

  it('one span with two plain ops is one tools line of 2; two spans are two lines', () => {
    const one = derivePrelude([
      m('2', 'assistant', [call('a')]), m('3', 'user', [result('a', 'x')]),
      m('4', 'assistant', [call('b')]), m('5', 'user', [result('b', 'y')]),
    ])
    const { unmount } = render(<PreludeSection {...chat} view={one} status="ok" done />)
    expect(screen.getAllByTestId('chat-tools-line')).toHaveLength(1)
    expect(screen.getByTestId('chat-tools-line').textContent).toContain('Used 2 tools')
    unmount()
    const two = derivePrelude([
      m('2', 'user', [{ type: 'text', text: 'one' }]), m('3', 'assistant', [call('a')]), m('4', 'user', [result('a', 'x')]),
      m('5', 'user', [{ type: 'text', text: 'two' }]), m('6', 'assistant', [call('b')]), m('7', 'user', [result('b', 'y')]),
    ])
    render(<PreludeSection {...chat} view={two} status="ok" done />)
    expect(screen.getAllByTestId('chat-tools-line')).toHaveLength(2)
  })

  // #1539: an image-only prompt opens its own span, so the work it asked for is drawn after it, never folded above it.
  it('an image-only prompt: the tools line that follows it is drawn after its bubble', () => {
    const view = derivePrelude([
      m('2', 'user', [{ type: 'text', text: 'one' }]), m('3', 'assistant', [call('a')]), m('4', 'user', [result('a', 'x')]),
      m('5', 'user', [{ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 2048 } }]),
      m('6', 'assistant', [call('b')]), m('7', 'user', [result('b', 'y')]),
    ])
    render(<PreludeSection {...chat} view={view} status="ok" done />)
    const [before, after] = screen.getAllByTestId('chat-tools-line')
    const image = screen.getByText('[image · png · 2 KB]')
    expect(screen.getAllByTestId('chat-tools-line')).toHaveLength(2)
    expect(before.compareDocumentPosition(image) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(image.compareDocumentPosition(after) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('hides thinking', () => {
    const view = derivePrelude([m('2', 'assistant', [{ type: 'thinking', thinking: 'deep thought' }, { type: 'text', text: 'answer' }])])
    render(<PreludeSection {...chat} view={view} status="ok" done />)
    expect(screen.queryByText(/deep thought/)).toBeNull()
    expect(screen.getByText('answer')).toBeTruthy()
  })

  it('an edit and a failure get their own lines', () => {
    const base0 = derivePrelude([
      m('2', 'assistant', [call('e'), call('f')]),
      m('3', 'user', [result('e', 'ok'), result('f', 'boom', { is_error: true })]),
    ])
    const view: PreludeView = {
      ...base0,
      tools: { e: { name: 'Edit', startedAt: 1, endedAt: 2, status: 'done', diff: { path: '/w/n.md', added: 1, removed: 0, truncated: false, hunks: [{ oldStart: 1, oldLines: 1, newStart: 1, newLines: 2, lines: [' a', '+b'] }] } } },
    }
    render(<PreludeSection {...chat} view={view} status="ok" done />)
    expect(screen.getByTestId('chat-edited-line').textContent).toContain('n.md')
    expect(screen.getByTestId('chat-failed-line')).toBeTruthy()
  })

  it('a cut edit call and its cut result show their hints beside the edited line, not behind the fold', () => {
    const diff = { path: '/w/n.md', added: 1, removed: 0, truncated: false, hunks: [{ oldStart: 1, oldLines: 1, newStart: 1, newLines: 2, lines: [' a', '+b'] }] }
    const tools = { e: { name: 'Edit', startedAt: 1, endedAt: 2, status: 'done' as const, diff } }
    const cut = derivePrelude([
      m('2', 'assistant', [call('e', { truncated: true, total_bytes: 90000 })]),
      m('3', 'user', [result('e', 'abcd', { truncated: true, total_bytes: 50000 })]),
    ])
    const { unmount } = render(<PreludeSection {...chat} view={{ ...cut, tools }} status="ok" done />)
    expect(screen.getByTestId('chat-edited-line')).toBeTruthy()
    expect(screen.getAllByTestId('prelude-truncated')).toHaveLength(2)
    unmount()
    const whole = derivePrelude([m('2', 'assistant', [call('e')]), m('3', 'user', [result('e', 'abcd')])])
    render(<PreludeSection {...chat} view={{ ...whole, tools }} status="ok" done />)
    expect(screen.getByTestId('chat-edited-line')).toBeTruthy()
    expect(screen.queryAllByTestId('prelude-truncated')).toHaveLength(0)
  })

  it('an omitted image in an agent bubble; a cut user line gets its hint', () => {
    const view = derivePrelude([
      m('2', 'assistant', [{ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 2048 } }]),
      m('3', 'user', [{ type: 'text', text: 'abc', truncated: true, total_bytes: 90000 }]),
    ])
    render(<PreludeSection {...chat} view={view} status="ok" done />)
    expect(screen.getByTestId('chat-bubble-agent').textContent).toContain('[image · png · 2 KB]')
    expect(screen.getByTestId('chat-bubble-user').textContent).toContain('abc')
    expect(screen.getByTestId('prelude-truncated').textContent).toBe('Too long — showing the first 3 B of 88 KB')
  })

  it('a cut call and a cut result each show their hint inside the expanded tools line', () => {
    const view = derivePrelude([
      m('2', 'assistant', [call('a', { truncated: true, total_bytes: 90000 })]),
      m('3', 'user', [result('a', 'abcd', { truncated: true, total_bytes: 50000 })]),
    ])
    render(<PreludeSection {...chat} view={view} status="ok" done />)
    expect(screen.queryAllByTestId('prelude-truncated')).toHaveLength(0)
    fireEvent.click(screen.getByTestId('chat-tools-line'))
    const hints = within(screen.getByTestId('chat-tools-ops')).getAllByTestId('prelude-truncated').map((h) => h.textContent)
    expect(hints).toEqual(['Too long — showing the first 16 B of 88 KB', 'Too long — showing the first 4 B of 49 KB'])
  })

  it('an expanded tools line survives an older page joining its span (keyed by the last message)', () => {
    const tail = [m('4', 'assistant', [call('a')]), m('5', 'user', [result('a', 'x')])]
    const { rerender } = render(<PreludeSection {...chat} view={derivePrelude(tail)} status="ok" done={false} />)
    fireEvent.click(screen.getByTestId('chat-tools-line'))
    expect(screen.getByTestId('chat-tools-line').getAttribute('aria-expanded')).toBe('true')
    rerender(<PreludeSection {...chat} view={derivePrelude([m('3', 'assistant', [{ type: 'text', text: 'older' }]), ...tail])} status="ok" done={false} pages={2} />)
    expect(screen.getByText('older')).toBeTruthy()
    expect(screen.getByTestId('chat-tools-line').getAttribute('aria-expanded')).toBe('true')
  })

  it('prelude and live fold keys are independent', () => {
    const view = derivePrelude([m('2', 'assistant', [call('a')]), m('3', 'user', [result('a', 'x')])])
    const live = [
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'q' }], stop_reason: null } },
      { type: 'assistant', message: { id: 'm', role: 'assistant', content: [call('b')], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [result('b', 'y')], stop_reason: null } },
    ] as unknown as StreamMessage[]
    render(
      <ChatTranscript keyPrefix="exc" showThinking={false} showEmptyHint={false} messages={live}
        prelude={<PreludeSection {...chat} view={view} status="ok" done />} />,
    )
    const lines = screen.getAllByTestId('chat-tools-line')
    expect(lines).toHaveLength(2)
    fireEvent.click(lines[0])
    const after = screen.getAllByTestId('chat-tools-line')
    expect(after[0].getAttribute('aria-expanded')).toBe('true')
    expect(after[1].getAttribute('aria-expanded')).toBe('false')
  })
})

// Nexen's early golden page (spec §4.6), drawn in both modes.
describe.each<['room' | 'chat']>([['room'], ['chat']])('PreludeSection over Nexen\'s golden page in %s mode', (mode) => {
  const view = derivePrelude(sanitizePreludePage(golden)!.items)
  const draw = () => render(<PreludeSection {...base} mode={mode} view={view} status="ok" done />)

  it('renders without throwing, with the D2 boundaries (three segments, one compaction)', () => {
    expect(draw).not.toThrow()
    expect(screen.getAllByTestId('prelude-segment').map((e) => e.getAttribute('aria-label'))).toEqual(['In the terminal', 'Headless (worker)', 'In the terminal'])
    expect(screen.getAllByTestId('prelude-compaction').map((e) => e.getAttribute('aria-label'))).toEqual(['Conversation compacted here (auto)'])
  })

  it('draws the omitted image / PDF as placeholders with their sizes', () => {
    draw()
    expect(screen.getAllByTestId('prelude-media').map((e) => e.textContent))
      .toEqual([
        '[image · png · 69 B]', '[document · pdf · 15 B]', '[image · png · 69 B]',
        // The Read result's image: room draws its operation open, chat folds it into the tools line.
        ...(mode === 'room' ? ['[image · png · 69 B]'] : []),
      ])
  })

  it('draws every note source, the stderr one in the error tone', () => {
    draw()
    const ids = [...document.querySelectorAll('[data-testid^="prelude-note"],[data-testid="prelude-task"],[data-testid="prelude-bash-input"]')].map((e) => e.getAttribute('data-testid'))
    expect(ids).toEqual(['prelude-note-command_output', 'prelude-bash-input', 'prelude-note-bash_output', 'prelude-note-bash_output',
      'prelude-note-peer_message', 'prelude-task', 'prelude-note-peer_message', 'prelude-task', 'prelude-note-command_output'])
    const [out, err] = screen.getAllByTestId('prelude-note-bash_output')
    expect(out.innerHTML).not.toContain('text-status-error')
    expect(err.innerHTML).toContain('text-status-error')
    expect(screen.getByTestId('prelude-bash-input').textContent).toContain('! ls -la')
    expect(screen.getByTestId('worker-prelude').textContent).toContain('Catch you later!')
  })

  if (mode === 'room') {
    it('says so after every cut block: the 70000 text, the 90056 Write call and the 80000 result', () => {
      draw()
      expect(screen.getAllByTestId('prelude-truncated').map((h) => h.textContent)).toEqual([
        'Too long — showing the first 64 KB of 68 KB',
        'Too long — showing the first 32 KB of 88 KB',
        'Too long — showing the first 64 KB of 78 KB',
      ])
    })

    it('draws the denied Edit struck through with a warning dot, and the others as done', () => {
      draw()
      const ops = screen.getAllByTestId('operation-block').map((b) => [
        b.querySelector('[data-testid="op-name"]')!.textContent,
        b.querySelector('[data-testid="op-name"]')!.className.includes('line-through'),
        b.querySelector('[data-testid="op-dot"]')!.className.match(/bg-status-\w+/)![0],
      ])
      expect(ops).toEqual([['Bash', false, 'bg-status-success'], ['Read', false, 'bg-status-success'], ['Edit', true, 'bg-status-warning'], ['Write', false, 'bg-status-success']])
      // The Read's result carries an image: its placeholder replaces the generic non-text line (#1629).
      expect(screen.queryByTestId('op-non-text')).toBeNull()
    })
  } else {
    it('hints only where chat draws the block: the cut text now, the cut Write call and result inside its tools line', () => {
      draw()
      // Thinking is not drawn in chat and the Write sits behind a folded tools line.
      expect(screen.getAllByTestId('prelude-truncated').map((h) => h.textContent)).toEqual(['Too long — showing the first 64 KB of 68 KB'])
      for (const line of screen.getAllByTestId('chat-tools-line')) fireEvent.click(line)
      expect(screen.getAllByTestId('prelude-truncated').map((h) => h.textContent)).toEqual([
        'Too long — showing the first 64 KB of 68 KB',
        'Too long — showing the first 32 KB of 88 KB',
        'Too long — showing the first 64 KB of 78 KB',
      ])
    })

    it('shows the denied Edit as a failed line, never hidden, and counts only the other three tools', () => {
      draw()
      expect(screen.getAllByTestId('chat-failed-line').map((e) => e.textContent)).toEqual(["Edit · The user doesn't want to proceed with this tool use."])
      expect(screen.getAllByTestId('chat-tools-line').map((e) => e.textContent)).toEqual(['Used 1 tool', 'Used 1 tool', 'Used 1 tool'])
    })
  }
})

describe.each<['room' | 'chat']>([['room'], ['chat']])('PreludeSection closing handoff marker in %s mode', (mode) => {
  const items = [
    m('2', 'user', [{ type: 'text', text: 'fix the build' }]),
    m('3', 'assistant', [{ type: 'text', text: 'on it' }]),
  ]

  it('ends the section with a Headless (worker) rule, as its last child', () => {
    render(<PreludeSection {...base} mode={mode} view={derivePrelude(items)} status="ok" done />)
    const section = screen.getByTestId('worker-prelude')
    const last = section.lastElementChild as HTMLElement
    expect(last.getAttribute('data-testid')).toBe('prelude-handoff')
    expect(last.getAttribute('role')).toBe('separator')
    expect(last.getAttribute('aria-label')).toBe('Headless (worker)')
    expect(section.querySelectorAll('[data-testid="prelude-handoff"]')).toHaveLength(1)
  })

  it('is a marker, not a search unit', () => {
    render(<PreludeSection {...base} mode={mode} view={derivePrelude(items)} status="ok" done />)
    const h = screen.getByTestId('prelude-handoff')
    expect(h.hasAttribute('data-search-unit')).toBe(false)
    expect(h.querySelector('[data-search-unit]')).toBeNull()
  })

  it('follows a trailing sdk segment too (the worker is its own switch)', () => {
    render(<PreludeSection {...base} mode={mode} view={derivePrelude([...items, { offset: null, pos: '4', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' }])} status="ok" done />)
    const section = screen.getByTestId('worker-prelude')
    expect(section.lastElementChild?.getAttribute('data-testid')).toBe('prelude-handoff')
    expect(screen.getAllByTestId('prelude-segment')).toHaveLength(1)
  })

  it('is absent for an empty view: idle, none, gone, loading', () => {
    for (const status of ['idle', 'none', 'gone', 'loading'] as const) {
      const { unmount } = render(<PreludeSection {...base} mode={mode} view={derivePrelude([])} status={status} done />)
      expect(screen.queryByTestId('prelude-handoff')).toBeNull()
      unmount()
    }
  })
})

// #1534 (spec §5.4): every drawn prelude element names the position it
// starts at, so the scroll memory can anchor inside the prelude. The
// attribute sits on the row / span / note / marker root itself — a wrapper
// would add a box (and the section's space-y margin) above turn 1.
describe('PreludeSection scroll anchors (#1534)', () => {
  const items: PreludeItem[] = [
    { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
    m('2', 'user', [{ type: 'text', text: 'fix the build' }]),
    m('3', 'assistant', [{ type: 'text', text: 'on it' }]),
    note('4', 'command_output', 'Model set'),
    note('5', 'bash_input', 'ls'),
    note('6', 'task_notification', 'build done'),
    note('7', 'peer_message', 'hi'),
    { offset: null, pos: '8', at: 0, kind: 'prelude.compaction', trigger: 'auto' },
    m('9', 'user', [{ type: 'text', text: 'again' }]),
    m('10', 'assistant', [{ type: 'tool_use', id: 'tk', name: 'Task', input: { description: 'look', subagent_type: 'Explore' } }]),
    // A subagent's frame: drawn inside its Task (room), never a row of its own.
    { offset: null, pos: '11', at: 0, kind: 'user', msg: { type: 'user', parent_tool_use_id: 'tk', message: { role: 'user', content: [{ type: 'text', text: 'sub prompt' }], stop_reason: null } } as unknown as StreamMessage },
    m('12', 'user', [{ type: 'tool_result', tool_use_id: 'tk', content: 'back' }]),
    { offset: null, pos: '13', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
  ]
  /** The very same nodes, in order (identity, not isEqualNode). */
  const sameNodes = (a: Element[], b: Element[]) => a.length === b.length && a.every((e, k) => e === b[k])
  const draw = (mode: 'room' | 'chat') => {
    render(<PreludeSection {...base} mode={mode} view={derivePrelude(items)} status="ok" done />)
    const section = screen.getByTestId('worker-prelude')
    const marked = [...section.querySelectorAll<HTMLElement>('[data-prelude-pos]')]
    return { section, marked, poses: marked.map((e) => e.getAttribute('data-prelude-pos')) }
  }

  it('room: every row, note and marker carries its pos, on its own root, the handoff none', () => {
    const { section, marked, poses } = draw('room')
    expect(poses).toEqual(['1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '12', '13'])
    // Notes and markers: the attribute is on the element that carries the testid.
    const byPos = (p: string) => marked.find((e) => e.getAttribute('data-prelude-pos') === p)!
    expect(['1', '4', '5', '6', '7', '8', '13'].map((p) => byPos(p).getAttribute('data-testid'))).toEqual([
      'prelude-segment', 'prelude-note-command_output', 'prelude-bash-input', 'prelude-task',
      'prelude-note-peer_message', 'prelude-compaction', 'prelude-segment',
    ])
    expect(byPos('2').textContent).toContain('fix the build')
    expect(screen.getByTestId('prelude-handoff').hasAttribute('data-prelude-pos')).toBe(false)
    // No wrapper: the section's children are exactly the marked roots, then the handoff.
    expect(sameNodes([...section.children], [...marked, screen.getByTestId('prelude-handoff')])).toBe(true)
    expect(section.querySelector('[data-prelude-poses]')).toBeNull()
  })

  it('chat: each span carries its first pos and every message it holds; entries carry theirs', () => {
    const { section, marked, poses } = draw('chat')
    expect(poses).toEqual(['1', '2', '4', '5', '6', '7', '8', '9', '13'])
    const spans = [...section.querySelectorAll<HTMLElement>('[data-prelude-poses]')]
    expect(spans.map((s) => [s.getAttribute('data-prelude-pos'), s.getAttribute('data-prelude-poses')])).toEqual([
      ['2', '2 3'], ['9', '9 10 11 12'],
    ])
    expect(spans[0].textContent).toContain('fix the build')
    expect(spans[0].textContent).toContain('on it')
    expect(screen.getByTestId('prelude-handoff').hasAttribute('data-prelude-pos')).toBe(false)
    expect(sameNodes([...section.children], [...marked, screen.getByTestId('prelude-handoff')])).toBe(true)
  })

  it('a span word-matches a pos it holds, and only that', () => {
    draw('chat')
    expect(document.querySelector('[data-prelude-poses~="11"]')?.getAttribute('data-prelude-pos')).toBe('9')
    expect(document.querySelector('[data-prelude-poses~="1"]')).toBeNull()
  })
})

// U3 (spec §5.3 "Pasted text"): each pasted segment is a titled fold block, never a user line.
describe.each<['room' | 'chat']>([['room'], ['chat']])('PreludeSection pasted text (U3) in %s mode', (mode) => {
  const OPEN = '<pasted_content id="bb1b">'
  const CLOSE = '</pasted_content id="bb1b">'
  const body = (n: number) => Array.from({ length: n }, (_, k) => `pasted line ${k}`).join('\n')
  const draw = (items: PreludeItem[]) => render(<PreludeSection {...base} mode={mode} view={derivePrelude(items)} status="ok" done />)
  const titles = () => screen.getAllByTestId('prelude-pasted-title').map((e) => e.textContent)
  // Your typed lines (chat: your bubbles, minus the ones a paste sits in).
  const userLines = () => screen.queryAllByTestId(mode === 'room' ? 'room-user-line' : 'chat-bubble-user')
    .filter((e) => !e.querySelector('[data-testid="prelude-pasted"]'))
  const items = [
    m('1', 'user', [{ type: 'text', text: `${OPEN}\nsolo\n${CLOSE}` }]),
    m('2', 'assistant', [{ type: 'text', text: 'ok' }]),
    m('3', 'user', [{ type: 'text', text: `${OPEN}\n${body(3)}\n${CLOSE}` }]),
    m('4', 'user', [{ type: 'text', text: `${OPEN}\n${body(5)}`, truncated: true, total_bytes: 90000 }]),
  ]

  it('titles each paste by its line count: one line, several, and N+ when cut', () => {
    draw(items)
    expect(titles()).toEqual(['Pasted text · 1 line', 'Pasted text · 3 lines', 'Pasted text · 5+ lines'])
  })

  it('zh-TW: 「貼上的文字 · N 行」, 「N+ 行」 when cut', () => {
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    try {
      draw(items)
      expect(titles()).toEqual(['貼上的文字 · 1 行', '貼上的文字 · 3 行', '貼上的文字 · 5+ 行'])
    } finally {
      act(() => { useI18nStore.getState().setLocale('en') })
    }
  })

  it('typed text around a paste draws as your lines; the paste folds, never inside one, and no wrapper shows', () => {
    draw([m('1', 'user', [{ type: 'text', text: `fix this:\n${OPEN}\n${body(60)}\n${CLOSE}\nthanks` }])])
    const section = screen.getByTestId('worker-prelude')
    expect(section.textContent).not.toContain('pasted_content')
    // The room's line carries a `›` prefix; chat's bubble does not.
    expect(userLines().map((e) => e.textContent?.replace('›', '').trim())).toEqual(['fix this:', 'thanks'])
    const pasted = screen.getByTestId('prelude-pasted')
    expect(pasted.closest('[data-testid="room-user-line"]')).toBeNull()
    if (mode === 'chat') {
      // Chat tells speakers apart by side: your paste sits on your side, in a bubble of its own.
      const bubble = pasted.closest('[data-testid="chat-bubble-user"]')
      expect(bubble).not.toBeNull()
      expect(bubble!.textContent).not.toContain('fix this:')
      expect(bubble!.textContent).not.toContain('thanks')
    } else {
      expect(pasted.closest('[data-testid="chat-bubble-user"]')).toBeNull()
    }
    expect(pasted.textContent).not.toContain('pasted line 59')
    expect(document.querySelector('[data-search-unit="p1:1:text"]')).toBeNull()
    fireEvent.click(within(pasted).getByTestId('fold-more'))
    expect(document.querySelector('[data-search-unit="p1:1:text"]')!.textContent).toBe(body(60))
    expect(section.textContent).not.toContain('pasted_content')
  })

  it('a body that starts with / is pasted text, not a slash command', () => {
    draw([m('1', 'user', [{ type: 'text', text: `${OPEN}\n/compact now\n${CLOSE}` }])])
    expect(screen.queryByTestId('room-command')).toBeNull()
    expect(userLines()).toHaveLength(0)
    expect(screen.getByTestId('prelude-pasted').textContent).toContain('/compact now')
  })

  it('a cut paste shows exactly one truncation hint, after it, and the fold adds none — collapsed or expanded', () => {
    draw([m('1', 'user', [{ type: 'text', text: `look ${OPEN}\n${body(60)}`, truncated: true, total_bytes: 200000 }])])
    const once = () => {
      const hints = screen.getAllByTestId('prelude-truncated')
      expect(hints).toHaveLength(1)
      expect(screen.queryAllByTestId('fold-daemon-truncated')).toHaveLength(0)
      expect(screen.getByTestId('prelude-pasted').compareDocumentPosition(hints[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
      expect(hints[0].textContent).toContain('of 195 KB')
    }
    once()
    fireEvent.click(screen.getByTestId('fold-more'))
    once()
  })

  it('a closed paste then a short typed suffix in a cut block: the one hint reports the whole block (64 KB), not the suffix (10 B)', () => {
    const head = `${OPEN}\n`
    const tail = `\n${CLOSE}\n\nand thanks` // the typed suffix is 10 bytes
    draw([m('1', 'user', [{ type: 'text', text: head + 'x'.repeat(65536 - head.length - tail.length) + tail, truncated: true, total_bytes: 70000 }])])
    expect(titles()).toEqual(['Pasted text · 1 line'])
    expect(userLines().map((e) => e.textContent?.replace('›', '').trim())).toEqual(['and thanks'])
    expect(screen.getAllByTestId('prelude-truncated').map((e) => e.textContent)).toEqual(['Too long — showing the first 64 KB of 68 KB'])
  })

  it('wire path, the live capture\'s shape: N lines when complete; cut at 64 KiB, N+ and one hint for the whole block', () => {
    const lines = Array.from({ length: 1501 }, (_, k) => `filler line ${String(k).padStart(5, '0')} for the prelude truncation check`)
    const full = `\n\n${OPEN}\n${lines.join('\n')}\n${CLOSE}\n`
    const wire = (block: Record<string, unknown>) => sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [{ pos: '1', kind: 'user', at: 1, payload: { type: 'user', message: { role: 'user', content: [block] } } }],
    })!.items
    const { unmount } = draw(wire({ type: 'text', text: full }))
    expect(titles()).toEqual(['Pasted text · 1501 lines'])
    expect(screen.queryAllByTestId('prelude-truncated')).toHaveLength(0)
    unmount()
    draw(wire({ type: 'text', text: full.slice(0, 65536), truncated: true, total_bytes: 76617 }))
    expect(titles()).toEqual(['Pasted text · 1285+ lines'])
    expect(userLines()).toHaveLength(0)
    expect(screen.getAllByTestId('prelude-truncated').map((e) => e.textContent)).toEqual(['Too long — showing the first 64 KB of 75 KB'])
    expect(screen.getByTestId('worker-prelude').textContent).not.toContain('pasted_content')
  })
})

// Conversation entity spec §10.3: the rows are drawn in runs of one
// attribution, a PreludeSegment each — a Fragment, so the DOM never shows it.
describe('PreludeSection runs of attribution (§10.3)', () => {
  const view = derivePrelude(sanitizePreludePage(real)!.items)
  // Worker A's segment (the capture's own) and its two lines, and two later lines given to B.
  const attribution = new Map([
    ...['394248.0', '394248.1', '411382.1'].map((p) => [p, 'exc_A'] as const),
    ...['447031.1', '448274.1'].map((p) => [p, 'exc_B'] as const),
  ])
  /** Per row of the section: its testid, pos, poses and every testid inside it, in order. */
  const rows = () => [...screen.getByTestId('worker-prelude').children].map((c) => [
    c.getAttribute('data-testid') ?? '·',
    c.getAttribute('data-prelude-pos') ?? '-',
    ...(c.hasAttribute('data-prelude-poses') ? [`[${c.getAttribute('data-prelude-poses')}]`] : []),
    ...[...c.querySelectorAll('[data-testid]')].map((e) => e.getAttribute('data-testid')),
  ].join(' '))
  const draw = (mode: 'room' | 'chat', a?: ReadonlyMap<string, string>) => {
    const { unmount } = render(<PreludeSection {...base} mode={mode} view={view} status="ok" done={false} {...(a ? { attribution: a } : {})} />)
    const out = { html: screen.getByTestId('worker-prelude').outerHTML, rows: rows() }
    unmount()
    return out
  }

  it('room: an unattributed prelude draws as it did before runs existed', () => {
    expect(draw('room').rows).toMatchInlineSnapshot(`
      [
        "prelude-sentinel -",
        "prelude-segment 22485.0",
        "· 22485.1 room-user-line room-user-prefix",
        "· 198497.1 operation-block op-dot op-name op-arg op-duration op-input-toggle op-rail fold-body",
        "· 200316.1",
        "· 322459.1 room-prose",
        "prelude-bash-input 325664.1",
        "prelude-note-bash_output 326143.1 fold-body",
        "· 329531.1 room-prose",
        "· 333903.1 room-user-line room-user-prefix",
        "· 341355.1 operation-block op-dot op-name op-arg op-input-toggle op-rail fold-body",
        "· 343241.1",
        "· 348261.1 room-prose",
        "· 351605.1 room-user-line room-user-prefix",
        "· 355768.1 operation-block op-dot op-name op-arg op-input-toggle op-rail fold-body",
        "· 358406.1",
        "· 363145.1 room-prose",
        "prelude-task 366929.1",
        "· 368019.1 operation-block op-dot op-name op-arg op-input-toggle op-rail fold-body",
        "· 370091.1",
        "· 373374.1 room-prose",
        "· 376642.1 room-user-line room-user-prefix",
        "· 377974.1 operation-block op-dot op-name op-arg op-duration op-input-toggle op-rail fold-body",
        "· 380381.1",
        "· 381826.1 room-user-line room-user-prefix",
        "· 383631.1 operation-block op-dot op-name op-arg op-input-toggle op-rail fold-body",
        "· 385439.1",
        "· 387451.1 room-prose",
        "· 392609.1 room-command",
        "prelude-note-command_output 393190.1 fold-body",
        "prelude-segment 394248.0",
        "· 394248.1 room-user-line room-user-prefix",
        "· 411382.1 room-prose",
        "prelude-segment 415815.0",
        "· 415815.1 room-user-line room-user-prefix",
        "· 437272.1 operation-block op-dot op-name op-arg op-duration op-input-toggle op-rail fold-body",
        "· 439033.1",
        "· 443629.1 room-prose",
        "· 447031.1 room-user-line room-user-prefix",
        "· 448274.1 room-prose",
        "· 453523.1 room-command",
        "prelude-note-command_output 454104.1 fold-body",
        "prelude-handoff -",
      ]
    `)
  })

  it('chat: an unattributed prelude draws as it did before runs existed', () => {
    expect(draw('chat').rows).toMatchInlineSnapshot(`
      [
        "prelude-sentinel -",
        "prelude-segment 22485.0",
        "· 22485.1 [22485.1 198497.1 200316.1 322459.1] chat-bubble-user chat-tools-line chat-bubble-agent room-prose",
        "prelude-bash-input 325664.1",
        "prelude-note-bash_output 326143.1 fold-body",
        "· 329531.1 [329531.1] chat-bubble-agent room-prose",
        "· 333903.1 [333903.1 341355.1 343241.1 348261.1] chat-bubble-user chat-tools-line chat-bubble-agent room-prose",
        "· 351605.1 [351605.1 355768.1 358406.1 363145.1] chat-bubble-user chat-tools-line chat-bubble-agent room-prose",
        "prelude-task 366929.1",
        "· 368019.1 [368019.1 370091.1 373374.1] chat-tools-line chat-bubble-agent room-prose",
        "· 376642.1 [376642.1 377974.1 380381.1] chat-bubble-user chat-tools-line",
        "· 381826.1 [381826.1 383631.1 385439.1 387451.1] chat-bubble-user chat-tools-line chat-bubble-agent room-prose",
        "· 392609.1 [392609.1] chat-bubble-user",
        "prelude-note-command_output 393190.1 fold-body",
        "prelude-segment 394248.0",
        "· 394248.1 [394248.1 411382.1] chat-bubble-user chat-bubble-agent room-prose",
        "prelude-segment 415815.0",
        "· 415815.1 [415815.1 437272.1 439033.1 443629.1] chat-bubble-user chat-tools-line chat-bubble-agent room-prose",
        "· 447031.1 [447031.1 448274.1] chat-bubble-user chat-bubble-agent room-prose",
        "· 453523.1 [453523.1] chat-bubble-user",
        "prelude-note-command_output 454104.1 fold-body",
        "prelude-handoff -",
      ]
    `)
  })

  it.each<['room' | 'chat']>([['room'], ['chat']])('%s: an attributed prelude is the very same DOM: elements, testids, attributes, order', (mode) => {
    const plain = draw(mode)
    const attributed = draw(mode, attribution)
    expect(attributed.rows).toEqual(plain.rows)
    expect(attributed.html).toBe(plain.html)
    // An empty attribution (the stint list unavailable) is the same as none.
    expect(draw(mode, new Map()).html).toBe(plain.html)
  })
})

// Conversation entity spec §10.4: an attributed segment fetches its stint's
// event log once per pane, the first time it is drawn, and draws its lines
// with the stint's real tool status and subagent tasks.
describe.each<['room' | 'chat']>([['room'], ['chat']])('PreludeSection enrichment of earlier worker segments (§10.4) in %s mode', (mode) => {
  const n2 = (pos: string, kind: 'tool_use' | 'tool_result', payload: Record<string, unknown>): PreludeItem => ({ offset: null, pos, at: 1, kind, payload })
  const view = derivePrelude([
    { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
    m('2', 'user', [{ type: 'text', text: 'start' }]),
    { offset: null, pos: '3', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
    m('4', 'user', [{ type: 'text', text: 'run it' }]),
    m('5', 'assistant', [
      { type: 'tool_use', id: 'toolu_1', name: 'Bash', input: { command: 'make' } },
      { type: 'tool_use', id: 'toolu_T', name: 'Task', input: { subagent_type: 'Explore', description: 'look' } },
    ]),
    n2('5.1', 'tool_use', { tool_use_id: 'toolu_1', name: 'Bash' }),
    m('6', 'user', [{ type: 'tool_result', tool_use_id: 'toolu_1', content: 'built' }, { type: 'tool_result', tool_use_id: 'toolu_T', content: 'found' }]),
    // The transcript says the call succeeded in 5 s.
    n2('6.1', 'tool_result', { tool_use_id: 'toolu_1', status: 'ok', duration_ms: 5000 }),
    m('7', 'assistant', [{ type: 'text', text: 'done' }]),
  ])
  // The worker segment and its lines were written by exc_A.
  const A = new Map(['3', '4', '5', '6', '7'].map((p) => [p, 'exc_A'] as const))
  const ev = (seq: number, kind: string, payload: Record<string, unknown>): NexEvent => ({ seq, execution_id: 'exc_A', kind, payload, created_at: 1000 + seq })
  // exc_A's own log: the call failed after 1988 ms, and its Task ran a subagent.
  const stintEvents: NexEvent[] = [
    ev(1, 'assistant', { type: 'assistant', parent_tool_use_id: null, message: { role: 'assistant', stop_reason: null, content: [
      { type: 'tool_use', id: 'toolu_1', name: 'Bash', input: { command: 'make' } },
      { type: 'tool_use', id: 'toolu_T', name: 'Task', input: { subagent_type: 'Explore', description: 'look' } },
    ] } }),
    ev(2, 'task_start', { task_id: 'tk1', kind: 'subagent', tool_use_id: 'toolu_T', description: 'look', started_at: 1000 }),
    ev(3, 'task_end', { task_id: 'tk1', kind: 'subagent', tool_use_id: 'toolu_T', status: 'completed', ended_at: 13000, usage: { total_tokens: 26000, tool_uses: 8, duration_ms: 12000 } }),
    ev(4, 'user', { type: 'user', parent_tool_use_id: null, message: { role: 'user', content: [
      { type: 'tool_result', tool_use_id: 'toolu_1', content: 'built', is_error: true },
      { type: 'tool_result', tool_use_id: 'toolu_T', content: 'found' },
    ] } }),
    ev(5, 'tool_result', { tool_use_id: 'toolu_1', parent_tool_use_id: null, status: 'error', duration_ms: 1988 }),
  ]
  const many = (n: number) => Array.from({ length: n }, (_, i) => ev(i + 1, 'execution.observer_attached', { observers: 1 }))
  const pageOf = (items: NexEvent[]): EventsPage => ({ items, next_cursor: 0 })
  const draw = (cache: StintEnrichmentCache | null, attribution: ReadonlyMap<string, string> = A) => render(
    <StintEnrichmentContext.Provider value={cache}>
      <PreludeSection {...base} mode={mode} view={view} status="ok" done attribution={attribution} />
    </StintEnrichmentContext.Provider>,
  )
  /** Bash as drawn: room, its dot and duration; chat, the failed lines and the tools lines. */
  const bash = () => {
    if (mode === 'chat') return [screen.queryAllByTestId('chat-failed-line').map((e) => e.textContent), screen.queryAllByTestId('chat-tools-line').map((e) => e.textContent)]
    const block = screen.getAllByTestId('operation-block')[0]
    return [block.querySelector('[data-testid="op-dot"]')!.className.match(/bg-status-\w+/)![0], block.querySelector('[data-testid="op-duration"]')?.textContent]
  }
  const transcriptBash = mode === 'room' ? ['bg-status-success', '5.0s'] : [[], ['Used 2 tools']]

  it('lazy: only a stint whose segment is drawn is fetched; a plain segment fetches nothing', async () => {
    const fetch = vi.fn(async () => pageOf(stintEvents))
    // exc_B wrote a line that is not loaded: no segment of it is in the tree.
    draw(createStintEnrichmentCache(fetch), new Map([...A, ['99', 'exc_B']]))
    await act(async () => {})
    expect(fetch.mock.calls).toEqual([['h', 'exc_A', { after: 0, limit: 500, signal: expect.any(AbortSignal) }]])
  })

  it('two segments of one stint share one fetch', async () => {
    const fetch = vi.fn(async () => pageOf(many(5001)))
    // exc_A: the cli marker, then the sdk marker and what follows — two runs with a plain one between.
    draw(createStintEnrichmentCache(fetch), new Map(['1', '3', '4'].map((p) => [p, 'exc_A'] as const)))
    // Each enriched segment ends with its own budget line: two segments, one fetch.
    expect(await screen.findAllByTestId('prelude-enrichment-truncated')).toHaveLength(2)
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('draws the stint\'s tool status and duration over the transcript\'s, and its subagent task on the Task call', async () => {
    let settle: (p: EventsPage) => void = () => {}
    draw(createStintEnrichmentCache(vi.fn(() => new Promise<EventsPage>((resolve) => { settle = resolve }))))
    // Still loading: exactly the transcript's.
    expect(bash()).toEqual(transcriptBash)
    expect(screen.queryByTestId('subagent-task-suffix')).toBeNull()
    await act(async () => settle(pageOf(stintEvents)))
    if (mode === 'room') {
      expect(bash()).toEqual(['bg-status-error', '2.0s'])
    } else {
      // Chat recomputes the span's operations: the failure gets its own line.
      expect(bash()).toEqual([['Bash · built'], ['Used 1 tool']])
      fireEvent.click(screen.getByTestId('chat-tools-line'))
    }
    expect(screen.getByTestId('subagent-task-suffix').textContent).toContain('26k tokens')
    expect(screen.queryByTestId('prelude-enrichment-truncated')).toBeNull()
  })

  it('a stint over the budget says so in one muted line, the last of its segment, its count in the UI language', async () => {
    // A runner whose own default groups 5000 as "5.000": the count must follow the UI language, never the runner's.
    const RealNumberFormat = Intl.NumberFormat
    const numberFormat = vi.spyOn(Intl, 'NumberFormat').mockImplementation(function (locales?: string | string[], options?: Intl.NumberFormatOptions) {
      return new RealNumberFormat(locales ?? 'de-DE', options)
    } as unknown as typeof Intl.NumberFormat)
    let custom: string | null = null
    try {
      draw(createStintEnrichmentCache(vi.fn(async () => pageOf(many(5001)))))
      const line = await screen.findByTestId('prelude-enrichment-truncated')
      expect(line.textContent).toBe('This worker segment has too many events; only the first 5,000 were used.')
      expect(line.className).toBe('text-xs text-text-muted')
      // Right after the segment's last row, before the handoff that closes the section.
      const kids = [...screen.getByTestId('worker-prelude').children]
      expect(kids.at(-2)).toBe(line)
      expect(kids.at(-3)!.getAttribute('data-prelude-pos')).toBe(mode === 'room' ? '7' : '4')
      act(() => { useI18nStore.getState().setLocale('zh-TW') })
      expect(line.textContent).toBe('這段 worker 的事件太多，只補充了前 5,000 筆')
      // A user-imported locale names no language: its missing keys are English, and so is its count.
      custom = useI18nStore.getState().importLocale({ name: 'Mine', translations: {} })
      act(() => { useI18nStore.getState().setLocale(custom!) })
      expect(line.textContent).toBe('This worker segment has too many events; only the first 5,000 were used.')
    } finally {
      numberFormat.mockRestore()
      act(() => { useI18nStore.getState().setLocale('en') })
      if (custom) useI18nStore.getState().deleteCustomLocale(custom)
    }
  })

  it('a failed fetch draws the transcript\'s status: the same DOM as no enrichment, no toast, no error row', async () => {
    const plain = draw(null)
    const html = screen.getByTestId('worker-prelude').outerHTML
    plain.unmount()
    const error = vi.spyOn(console, 'error')
    const warn = vi.spyOn(console, 'warn')
    try {
      const fetch = vi.fn(async () => { throw new Error('down') })
      const cache = createStintEnrichmentCache(fetch)
      draw(cache)
      await waitFor(() => expect(cache.get('exc_A')).toBeNull())
      await act(async () => {})
      expect(bash()).toEqual(transcriptBash)
      expect(screen.getByTestId('worker-prelude').outerHTML).toBe(html)
      expect(screen.queryByTestId('prelude-error')).toBeNull()
      expect(useUndoToast.getState().toast).toBeNull()
      expect(useUndoToast.getState().notice).toBeNull()
      expect(error).not.toHaveBeenCalled()
      expect(warn).not.toHaveBeenCalled()
    } finally {
      error.mockRestore()
      warn.mockRestore()
    }
  })
})

describe.each<['room' | 'chat']>([['room'], ['chat']])('PreludeSection per-turn cost footers on earlier worker segments (§10.4 result frames) in %s mode', (mode) => {
  const say = (pos: string, id: string, text: string): PreludeItem => ({
    offset: null, pos, at: 1, kind: 'assistant',
    msg: { type: 'assistant', parent_tool_use_id: null, message: { id, role: 'assistant', content: [{ type: 'text', text }], stop_reason: null } } as unknown as StreamMessage,
  })
  // Two prompt spans. The transcript lists the turn that ran SECOND first: its lines carry msg_B, the other's msg_A.
  const view = derivePrelude([
    { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
    m('2', 'user', [{ type: 'text', text: 'prompt one' }]),
    say('3', 'msg_B', 'answer one'),
    m('4', 'user', [{ type: 'text', text: 'prompt two' }]),
    say('5', 'msg_A', 'answer two'),
    m('6', 'user', [{ type: 'text', text: 'prompt three' }]),
    say('7', 'msg_gone', 'answer three'),
  ])
  const A = new Map(['1', '2', '3', '4', '5', '6', '7'].map((p) => [p, 'exc_A'] as const))
  const ev = (seq: number, kind: string, payload: Record<string, unknown>): NexEvent => ({ seq, execution_id: 'exc_A', kind, payload, created_at: 1000 + seq })
  const said = (seq: number, id: string) => ev(seq, 'assistant', { type: 'assistant', parent_tool_use_id: null, message: { id, role: 'assistant', content: [{ type: 'text', text: id }], stop_reason: null } })
  const result = (seq: number, usd: number, out: number, ms: number) =>
    ev(seq, 'result', { type: 'result', subtype: 'success', total_cost_usd: usd, duration_ms: ms, usage: { input_tokens: 5, output_tokens: out } })
  // exc_A ran msg_A first (turn 1), then msg_B (turn 2).
  const events: NexEvent[] = [said(1, 'msg_A'), result(2, 0.5, 1200, 38000), said(3, 'msg_B'), result(4, 0.75, 300, 5000)]
  const turns = costSummary(enrichFromEvents(events).messages).turns
  const footerText = (t: (typeof turns)[number]) => `${formatUsd(t.costUsd)} · ${formatTokens(t.tokens!.output)} · ${formatDuration(t.durationMs ?? 0)}`
  const summary = (extra: Record<string, unknown> = {}) => ({ id: 'exc_A', state: 'completed', ...extra }) as unknown as ExecutionSummary
  const stintOf = (s: ExecutionSummary): Stint => ({ id: 'exc_A', boundary: 0, createdAt: 1, summary: s })
  const draw = (cache: StintEnrichmentCache | null, s: ExecutionSummary = summary(), v: PreludeView = view) => render(
    <StintEnrichmentContext.Provider value={cache}>
      <PreludeSection {...base} mode={mode} view={v} status="ok" done attribution={A} stints={[stintOf(s)]} />
    </StintEnrichmentContext.Provider>,
  )
  const cacheOf = (items: NexEvent[]) => createStintEnrichmentCache(vi.fn(async (): Promise<EventsPage> => ({ items, next_cursor: 0 })))
  /** The last pos of the span a footer sits under. */
  const owner = (f: HTMLElement) => mode === 'room'
    ? f.previousElementSibling!.getAttribute('data-prelude-pos')
    : f.closest('[data-prelude-poses]')!.getAttribute('data-prelude-poses')!.split(' ').at(-1)
  const footers = () => screen.queryAllByTestId('prelude-cost-footer')

  it('maps each turn to the span its assistant message ids are in, by id and not by position', async () => {
    draw(cacheOf(events))
    await waitFor(() => expect(footers()).toHaveLength(2))
    // Pos 3 holds msg_B (turn 2); pos 5 holds msg_A (turn 1). Position pairing would swap them.
    expect(footers().map((f) => [owner(f), f.textContent])).toEqual([
      ['3', footerText(turns[1])],
      ['5', footerText(turns[0])],
    ])
    expect(footers()[1].textContent).toBe('$0.5000 · 1.2k · 38.0s')
  })

  it('a span whose assistant message id is absent from the events has no footer', async () => {
    draw(cacheOf(events))
    await waitFor(() => expect(footers()).toHaveLength(2))
    expect(footers().map(owner)).not.toContain('7')
  })

  it('is styled like TurnFooter and is not a search unit', async () => {
    draw(cacheOf(events))
    await waitFor(() => expect(footers()).toHaveLength(2))
    const f = footers()[0]
    expect(f.className).toBe('mt-1 text-[length:var(--wt-font-size)] text-[var(--wt-footer-color)] select-none')
    expect(f.hasAttribute('data-search-unit')).toBe(false)
  })

  it('turn 1 of a resumed stint carries the prior-history tooltip, its numbers unchanged', async () => {
    draw(cacheOf(events), summary({ resume_session_id: 's_prev' }))
    await waitFor(() => expect(footers()).toHaveLength(2))
    const [two, one] = footers()
    expect(one.getAttribute('title')).toBe('Includes spend from before the hand-over')
    expect(one.textContent).toBe(footerText(turns[0]))
    expect(two.hasAttribute('title')).toBe(false)
  })

  it('a stint that did not resume has no tooltip', async () => {
    draw(cacheOf(events))
    await waitFor(() => expect(footers()).toHaveLength(2))
    expect(footers().some((f) => f.hasAttribute('title'))).toBe(false)
  })

  it('a turn is drawn once, under the last span that holds one of its messages', async () => {
    const odd = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
      m('2', 'user', [{ type: 'text', text: 'prompt one' }]),
      say('3', 'msg_A', 'part one'),
      m('4', 'user', [{ type: 'text', text: 'prompt two' }]),
      say('5', 'msg_A', 'part two'),
    ])
    draw(cacheOf(events), summary(), odd)
    await waitFor(() => expect(footers()).toHaveLength(1))
    expect(owner(footers()[0])).toBe('5')
  })

  it('a span holding lines of two turns (no opening line between) shows both footers, in turn order', async () => {
    // No opening line between two turns' lines (the later turn's prompt is not in the transcript): msg_B's line follows msg_A's in one span.
    const shared = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
      m('2', 'user', [{ type: 'text', text: 'prompt one' }]),
      say('3', 'msg_B', 'turn two line'),
      say('4', 'msg_A', 'turn one line'),
    ])
    draw(cacheOf(events), summary({ resume_session_id: 's_prev' }), shared)
    await waitFor(() => expect(footers()).toHaveLength(2))
    // Turn order (index ascending), not transcript order; the tooltip stays on turn 1 alone.
    expect(footers().map((f) => f.textContent)).toEqual([footerText(turns[0]), footerText(turns[1])])
    expect(footers().map((f) => f.hasAttribute('title'))).toEqual([true, false])
    // Consecutive siblings right after the span's last row.
    if (mode === 'room') {
      expect(footers()[0].previousElementSibling!.getAttribute('data-prelude-pos')).toBe('4')
      expect(footers()[0].nextElementSibling).toBe(footers()[1])
    } else {
      expect(footers()[0].closest('[data-prelude-poses]')).toBe(footers()[1].closest('[data-prelude-poses]'))
    }
  })

  it('draws nothing while loading, for a plain segment, and when the fetch failed', async () => {
    const plainDom = () => screen.getByTestId('worker-prelude').outerHTML
    let settle: (p: EventsPage) => void = () => {}
    const loading = draw(createStintEnrichmentCache(vi.fn(() => new Promise<EventsPage>((resolve) => { settle = resolve }))))
    expect(footers()).toHaveLength(0)
    await act(async () => settle({ items: events, next_cursor: 0 }))
    expect(footers()).toHaveLength(2)
    loading.unmount()
    const plain = draw(null)
    const html = plainDom()
    expect(footers()).toHaveLength(0)
    plain.unmount()
    const failed = createStintEnrichmentCache(vi.fn(async () => { throw new Error('down') }))
    draw(failed)
    await waitFor(() => expect(failed.get('exc_A')).toBeNull())
    expect(footers()).toHaveLength(0)
    expect(plainDom()).toBe(html)
  })
})

// Task 33 (R-2c-2 / R-2c-3): an earlier worker segment's omitted images become
// thumbnails fetched from that stint, only when its prompts pair with the
// stint's attachment-carrying events unambiguously; otherwise nothing changes.
describe.each<['room' | 'chat']>([['room'], ['chat']])('PreludeSection attachment thumbnails on earlier worker segments in %s mode', (mode) => {
  const ROUTE = { method: 'GET', path: '/api/nex/v1/executions/{id}/attachments/{sha256}' }
  const att = (c: string) => ({ media_type: 'image/png', bytes: 10, sha256: c.repeat(64) })
  const img = (kb: number) => ({ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: kb * 1024 } })
  // Two prompts carry images, as Nexen writes them (text first, then each image); a third has none.
  const makeView = () => derivePrelude([
    { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
    m('2', 'user', [{ type: 'text', text: 'compare these' }, img(1), img(2)]),
    m('3', 'assistant', [{ type: 'text', text: 'they differ' }]),
    // An attachment-only send (#1614): an opening line, so chat starts a span here.
    m('4', 'user', [img(3)]),
    m('5', 'assistant', [{ type: 'text', text: 'a third' }]),
    m('6', 'user', [{ type: 'text', text: 'thanks' }]),
  ])
  const view = makeView()
  const A = new Map(['1', '2', '3', '4', '5', '6'].map((p) => [p, 'exc_A'] as const))
  const ev = (seq: number, kind: string, payload: Record<string, unknown>): NexEvent => ({ seq, execution_id: 'exc_A', kind, payload, created_at: 1000 + seq })
  const sent = (seq: number, attachments: unknown[]) => (seq === 1
    ? ev(seq, 'execution.delegated', { brief: 'b', attachments })
    : ev(seq, 'execution.message_accepted', { text: 't', turn_id: `t${seq}`, attachments }))
  // The stint's sends: its brief with two images, an image-only message, a message with none.
  const exact = [sent(1, [att('a'), att('b')]), sent(2, [att('c')]), sent(3, [])]
  const cacheOf = (items: NexEvent[]) => createStintEnrichmentCache(vi.fn(async (): Promise<EventsPage> => ({ items, next_cursor: 0 })))
  const draw = (cache: StintEnrichmentCache | null, v: PreludeView = view) => render(
    // The pane's own source: an earlier stint's images must never be fetched from it.
    <AttachmentSourceContext.Provider value={{ hostId: 'h', executionId: 'exc_now' }}>
      <StintEnrichmentContext.Provider value={cache}>
        <PreludeSection {...base} mode={mode} view={v} status="ok" done attribution={A} />
      </StintEnrichmentContext.Provider>
    </AttachmentSourceContext.Provider>,
  )
  const dom = () => screen.getByTestId('worker-prelude').outerHTML
  const thumbs = (root: ParentNode = document) => root.querySelectorAll('[data-testid="attachment-thumb"]')
  const placeholders = () => screen.queryAllByText(/^\[image · png · \d+ KB\]$/).map((e) => e.textContent)
  const row = (pos: string) => document.querySelector(`[data-prelude-pos="${pos}"]`)!
  /** Today's DOM: the same view with no enrichment at all. */
  const plainDom = (v: PreludeView = view) => {
    const r = draw(null, v)
    const html = dom()
    r.unmount()
    return html
  }
  const settled = async (cache: StintEnrichmentCache) => {
    await waitFor(() => expect(cache.get('exc_A')).toBeDefined())
    await act(async () => {})
  }
  const fetched = () => vi.mocked(api.fetchAttachment).mock.calls.map((c) => [c[0], c[1], c[2]])

  const origCreate = URL.createObjectURL, origRevoke = URL.revokeObjectURL
  beforeEach(() => {
    vi.mocked(api.fetchAttachment).mockReset()
    vi.mocked(api.fetchAttachment).mockResolvedValue(new Blob(['x'], { type: 'image/png' }))
    let n = 0
    Object.assign(URL, { createObjectURL: vi.fn(() => `blob:t${++n}`), revokeObjectURL: vi.fn() })
    const send = { delivery: ['delivered'], max_text_bytes: 1, max_request_bytes: 1, attachments: { image: { media_types: [], max_bytes: 1, max_count: 1, max_total_bytes: 1, providers: [], fetch: ROUTE } } }
    useNexHostStore.setState({ byHost: { h: { phase: 'ready', capabilities: { send }, info: null, error: null, fetchedAt: 0, generation: 1, fingerprint: 'x' } } as never })
  })
  afterEach(() => {
    Object.assign(URL, { createObjectURL: origCreate, revokeObjectURL: origRevoke })
    useNexHostStore.setState({ byHost: {} })
  })

  it('an exact pairing: each prompt draws its list as thumbnails fetched from the stint, its text kept, its placeholders gone', async () => {
    const plain = plainDom()
    let settle: (p: EventsPage) => void = () => {}
    draw(createStintEnrichmentCache(vi.fn(() => new Promise<EventsPage>((resolve) => { settle = resolve }))))
    // Loading: today's DOM, placeholders and all.
    expect(dom()).toBe(plain)
    expect(placeholders()).toEqual(['[image · png · 1 KB]', '[image · png · 2 KB]', '[image · png · 3 KB]'])
    await act(async () => settle({ items: exact, next_cursor: 0 }))
    await waitFor(() => expect(thumbs()).toHaveLength(3))
    expect(placeholders()).toEqual([])
    // The k-th prompt gets the k-th list: two under the text, one on the image-only line's own.
    expect(thumbs(row('2'))).toHaveLength(2)
    expect(thumbs(row('4'))).toHaveLength(1)
    expect(within(row('2') as HTMLElement).getByText('compare these')).toBeTruthy()
    // The text keeps its search unit: each image's block becomes a slot in place, so no block index moves.
    expect(document.querySelector('[data-search-unit="p2:0:text"]')!.textContent).toBe('compare these')
    // From the stint, never the pane's own execution.
    await waitFor(() => expect(fetched()).toHaveLength(3))
    expect(fetched().sort()).toEqual([['h', 'exc_A', 'a'.repeat(64)], ['h', 'exc_A', 'b'.repeat(64)], ['h', 'exc_A', 'c'.repeat(64)]])
    expect(vi.mocked(api.fetchAttachment).mock.calls.map((c) => c[3])).toEqual([ROUTE, ROUTE, ROUTE])
  })

  it('an image before its text (R-2c-5): every search unit id is the unmatched render\'s, and the thumbnail still shows', async () => {
    const imageFirst = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
      m('2', 'user', [img(1), { type: 'text', text: 'what is this' }]),
      m('3', 'assistant', [{ type: 'text', text: 'a cat' }]),
    ])
    const units = () => [...document.querySelectorAll('[data-search-unit]')].map((e) => [e.getAttribute('data-search-unit'), e.textContent])
    const unmatched = draw(null, imageFirst)
    const before = units()
    unmatched.unmount()
    expect(before).toContainEqual(['p2:1:text', 'what is this'])
    draw(cacheOf([sent(1, [att('a')])]), imageFirst)
    await waitFor(() => expect(thumbs(row('2'))).toHaveLength(1))
    expect(placeholders()).toEqual([])
    expect(units()).toEqual(before)
  })

  it('the thumbnail slot itself draws nothing: a row with slots is the row without them', () => {
    const slot = { type: 'purdex_thumbnail_slot' }
    const prompt = (content: unknown[]) => derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
      m('2', 'user', [{ type: 'text', text: 'compare these' }, ...content]),
      m('3', 'assistant', [{ type: 'text', text: 'they differ' }]),
    ])
    const withSlots = plainDom(prompt([slot, slot]))
    expect(withSlots).toBe(plainDom(prompt([])))
    expect(withSlots).toContain('compare these')
  })

  it('never mutates the transcript\'s own messages', async () => {
    const fresh = makeView()
    const before = JSON.stringify(fresh.messages)
    const objects = [...fresh.messages]
    draw(cacheOf(exact), fresh)
    await waitFor(() => expect(thumbs()).toHaveLength(3))
    expect(JSON.stringify(fresh.messages)).toBe(before)
    expect(fresh.messages.every((msg, i) => msg === objects[i])).toBe(true)
  })

  it.each<[string, NexEvent[]]>([
    // Mutation guard (count check): the first two lists pair, the extra one would be ignored without it.
    ['one list too many', [...exact, sent(4, [att('d')])]],
    ['one list too few', [sent(1, [att('a'), att('b')])]],
    // Mutation guard (per-prompt check): as many lists as prompts, but the first holds one image, not two.
    ['a list of the wrong length', [sent(1, [att('a')]), sent(2, [att('b'), att('c')])]],
  ])('%s: placeholders stay for the whole segment, today\'s DOM, nothing fetched', async (_, events) => {
    const plain = plainDom()
    const cache = cacheOf(events)
    draw(cache)
    await settled(cache)
    expect(thumbs()).toHaveLength(0)
    expect(placeholders()).toHaveLength(3)
    expect(dom()).toBe(plain)
    expect(api.fetchAttachment).not.toHaveBeenCalled()
  })

  it('a failed fetch: today\'s DOM', async () => {
    const plain = plainDom()
    const cache = createStintEnrichmentCache(vi.fn(async () => { throw new Error('down') }))
    draw(cache)
    await waitFor(() => expect(cache.get('exc_A')).toBeNull())
    await act(async () => {})
    expect(dom()).toBe(plain)
  })

  it('a segment with no images is unchanged, whatever the stint sent', async () => {
    const textOnly = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
      m('2', 'user', [{ type: 'text', text: 'compare these' }]),
      m('3', 'assistant', [{ type: 'text', text: 'they differ' }]),
    ])
    const plain = plainDom(textOnly)
    for (const events of [exact, [sent(1, [])]]) {
      const cache = cacheOf(events)
      const r = draw(cache, textOnly)
      await settled(cache)
      expect(dom()).toBe(plain)
      r.unmount()
    }
    expect(api.fetchAttachment).not.toHaveBeenCalled()
  })

  // Codex R1-1: a send's attachments are images only (Nexen's `send.attachments.image`); an omitted document never pairs.
  describe('documents never pair', () => {
    const pdf = (kb: number) => ({ type: 'document', source: { type: 'omitted', media_type: 'application/pdf', bytes: kb * 1024 } })
    const docPlaceholders = () => screen.queryAllByText(/^\[document · pdf · \d+ KB\]$/).map((e) => e.textContent)
    const units = () => [...document.querySelectorAll('[data-search-unit]')].map((e) => [e.getAttribute('data-search-unit'), e.textContent])
    // A prompt with an image AND a document (the document first), then an image-only one.
    const mixed = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
      m('2', 'user', [pdf(5), { type: 'text', text: 'compare these' }, img(1)]),
      m('3', 'assistant', [{ type: 'text', text: 'they differ' }]),
      m('4', 'user', [img(3)]),
      m('5', 'assistant', [{ type: 'text', text: 'a third' }]),
    ])

    it('a document-only prompt is unchanged: never counted, never replaced', async () => {
      const docOnly = derivePrelude([
        { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
        m('2', 'user', [{ type: 'text', text: 'read this' }, pdf(5)]),
        m('3', 'assistant', [{ type: 'text', text: 'read it' }]),
      ])
      const plain = plainDom(docOnly)
      for (const events of [[sent(1, [att('a')])], [sent(1, [])]]) {
        const cache = cacheOf(events)
        const r = draw(cache, docOnly)
        await settled(cache)
        expect(dom()).toBe(plain)
        expect(docPlaceholders()).toEqual(['[document · pdf · 5 KB]'])
        r.unmount()
      }
      expect(api.fetchAttachment).not.toHaveBeenCalled()
    })

    it('an image + document prompt whose image count matches: a thumbnail for the image, the document keeps its placeholder', async () => {
      const r = draw(null, mixed)
      const before = units()
      r.unmount()
      draw(cacheOf([sent(1, [att('a')]), sent(2, [att('c')])]), mixed)
      await waitFor(() => expect(thumbs()).toHaveLength(2))
      expect(thumbs(row('2'))).toHaveLength(1)
      expect(thumbs(row('4'))).toHaveLength(1)
      expect(placeholders()).toEqual([])
      expect(docPlaceholders()).toEqual(['[document · pdf · 5 KB]'])
      expect(row('2').contains(screen.getByText('[document · pdf · 5 KB]'))).toBe(true)
      expect(units()).toEqual(before)
      await waitFor(() => expect(fetched()).toHaveLength(2))
      expect(fetched().sort()).toEqual([['h', 'exc_A', 'a'.repeat(64)], ['h', 'exc_A', 'c'.repeat(64)]])
    })

    it.each<[string, NexEvent[]]>([
      ['one list too few', [sent(1, [att('a')])]],
      ['one list too many', [sent(1, [att('a')]), sent(2, [att('c')]), sent(3, [att('d')])]],
      // Two lists for the image AND the document of prompt 2: a document is no attachment, so this is a mismatch.
      ['a list counting the document', [sent(1, [att('a'), att('b')]), sent(2, [att('c')])]],
    ])('%s: today\'s DOM, nothing fetched', async (_, events) => {
      const plain = plainDom(mixed)
      const cache = cacheOf(events)
      draw(cache, mixed)
      await settled(cache)
      expect(dom()).toBe(plain)
      expect(placeholders()).toHaveLength(2)
      expect(docPlaceholders()).toHaveLength(1)
      expect(api.fetchAttachment).not.toHaveBeenCalled()
    })
  })
})

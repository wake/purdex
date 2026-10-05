import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, within } from '@testing-library/react'
import { useEffect, type ReactNode } from 'react'
import { FoldContext, useFoldMemory, type FoldStore } from '../fold-context'
import PreludeSection from './PreludeSection'
import { derivePrelude, type PreludeView } from '../../../lib/nex/prelude'
import { sanitizePreludePage, type PreludeItem } from '../../../lib/nex/prelude-wire'
import type { StreamMessage } from '../../../lib/nex/message-types'
import ChatTranscript from '../../chat/ChatTranscript'

const m = (pos: string, type: 'user' | 'assistant', content: unknown[]): PreludeItem =>
  ({ pos, at: 1, kind: type, msg: { type, parent_tool_use_id: null, message: { role: type, content, stop_reason: null } } as unknown as StreamMessage })

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

const base = { keyPrefix: 'exc', mode: 'room' as const, onLoadOlder: vi.fn(), onRetry: vi.fn(), error: null, pages: 1 }

describe('PreludeSection', () => {
  it('draws markers, user lines, prose and notes in order', () => {
    const view = derivePrelude([
      { pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      m('2', 'user', [{ type: 'text', text: 'fix the build' }]),
      m('3', 'assistant', [{ type: 'text', text: 'on it' }]),
      { pos: '4', at: 0, kind: 'prelude.note', source: 'command_output', text: 'Model set to opus', truncated: false, totalBytes: null, stream: null },
      { pos: '5', at: 0, kind: 'prelude.compaction', trigger: 'auto' },
      { pos: '6', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
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

  it('every cut block and every cut note says so', () => {
    const view = derivePrelude([
      m('2', 'assistant', [
        { type: 'text', text: 'long…', truncated: true, total_bytes: 200000 },
        { type: 'thinking', thinking: 'mulling', truncated: true, total_bytes: 100000 },
        { type: 'tool_use', id: 't', name: 'Write', input: { content: 'x' }, truncated: true, total_bytes: 90000 },
      ]),
      m('3', 'user', [{ type: 'tool_result', tool_use_id: 't', content: 'out', truncated: true, total_bytes: 80000 }]),
      { pos: '4', at: 0, kind: 'prelude.note', source: 'command_output', text: 'big', truncated: true, totalBytes: 70000, stream: null },
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    const hints = screen.getAllByTestId('prelude-truncated').map((h) => h.textContent)
    expect(hints).toHaveLength(5)
    expect(hints[0]).toContain('195 KB')
    expect(hints[4]).toContain('68 KB')
  })

  it('a bash stderr note is drawn in the error tone', () => {
    const view = derivePrelude([{ pos: '4', at: 0, kind: 'prelude.note', source: 'bash_output', text: 'boom', truncated: false, totalBytes: null, stream: 'stderr' }])
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
      { pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
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

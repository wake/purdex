// A real /prelude body from Nexen v0.16.0 (terminal -> worker -> terminal ->
// worker round trip; two assistant prose texts were redacted, every other
// byte is as served) through the sanitiser and the view, so a contract drift
// fails here and not on screen.
import { describe, it, expect } from 'vitest'
import page from './__fixtures__/prelude-06GGS8J1YKZCPF4BRXZTX764F4.json'
import { sanitizePreludePage } from './prelude-wire'
import { derivePrelude, preludeBlocks } from './prelude'

type RawItem = { pos: string; kind: string; payload: Record<string, unknown> }
const raw = (page as unknown as { items: RawItem[] }).items
const p = sanitizePreludePage(page)!
const v = derivePrelude(p.items)

const userTexts = v.entries.flatMap((e) => {
  if (e.kind !== 'message') return []
  const m = v.messages[e.m] as unknown as { type: string; message: { content: { type: string; text?: string }[] } }
  if (m.type !== 'user') return []
  const t = m.message.content.find((b) => b.type === 'text')
  return t ? [{ pos: e.pos, text: t.text ?? '' }] : []
})

describe('prelude replay (real capture)', () => {
  it('sanitises the whole page: envelope and every item, in order', () => {
    expect(p.state).toBe('ok')
    expect(p.prevCursor).toBeNull()
    expect(p.totalBytes).toBe(454607)
    expect(p.items).toHaveLength(55)
    expect(p.items.map((i) => i.pos)).toEqual(raw.map((i) => i.pos))
    const count = (k: string) => p.items.filter((i) => i.kind === k).length
    expect([count('assistant'), count('user'), count('tool_use'), count('tool_result'), count('prelude.segment'), count('prelude.note')])
      .toEqual([16, 17, 7, 7, 3, 5])
  })

  it('draws the segments and notes in order', () => {
    const segs = v.entries.filter((e) => e.kind === 'segment')
    expect(segs.map((e) => [e.pos, e.kind === 'segment' && e.entrypoint])).toEqual([
      ['22485.0', 'cli'], ['394248.0', 'sdk-cli'], ['415815.0', 'cli'],
    ])
    const notes = v.entries.flatMap((e) => (e.kind === 'note' ? [e] : []))
    expect(notes.map((n) => n.source)).toEqual(['bash_input', 'bash_output', 'task_notification', 'command_output', 'command_output'])
    expect(notes[0].text).toBe('pwd')
    expect(notes[1].stream).toBe('stdout')
    expect(notes.slice(3).map((n) => n.text)).toEqual(['Catch you later!', 'See ya!'])
  })

  it('pairs every tool_use with its result, all done', () => {
    const ids = raw.filter((i) => i.kind === 'tool_use').map((i) => i.payload.tool_use_id as string)
    expect(ids).toHaveLength(7)
    for (const id of ids) expect(v.tools[id]?.status).toBe('done')
    expect(Object.keys(v.tools)).toHaveLength(7)
  })

  it('covers every entry exactly once in the chat blocks', () => {
    const blocks = preludeBlocks(v)
    const covered = blocks.flatMap((b) => (b.kind === 'span' ? v.entries.filter((e) => e.kind === 'message' && e.m >= b.start && e.m < b.end) : [b.entry]))
    expect(covered).toEqual(v.entries)
    expect(v.entries).toHaveLength(p.items.length - 14) // tool_use/tool_result are not entries
  })

  it('shows the human prompts as user text messages, in order', () => {
    const want: [string, string][] = [
      ['22485.1', 'Run ls with your Bash tool'],
      ['333903.1', 'Run sleep 15 && ls with your Bash tool'],
      ['351605.1', 'Then also reply with the word banana.'],
      ['376642.1', 'Run ping -c 12 127.0.0.1 | tail -1 in the foreground'],
      ['381826.1', 'Also tell me the word cherry'],
      ['392609.1', '/exit'],
      ['394248.1', '(handed off from tmux session prelude-acc)'],
      ['415815.1', 'Run echo round-trip with your Bash tool'],
      ['447031.1', 'Reply with the word kiwi.'],
      ['453523.1', '/exit'],
    ]
    expect(userTexts.map((u) => u.pos)).toEqual(want.map(([pos]) => pos))
    want.forEach(([, prefix], i) => expect(userTexts[i].text.startsWith(prefix)).toBe(true))
  })
})

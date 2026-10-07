import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, cleanup } from '@testing-library/react'
import { RelayPromptEditor, type RelayPromptEditorProps } from './RelayPromptEditor'
import { useI18nStore } from '../../stores/useI18nStore'
import { clearAllRelayPromptDrafts, readRelayPromptDraft, relayPromptDraftKey, writeRelayPromptDraft } from '../../lib/relay-prompt-draft-memory'

const H = 'h1'
const KEY = relayPromptDraftKey(H, 'write')
const DEF = '這個 session 的 context 已達接力門檻。\n請撰寫接力檔：{{path}}'
const VARS = ['path', 'old_ref', 'old_session', 'context', 'whoami']
const FIXED = { head: '[pdx-relay op={{op}} n={{nonce}}] ', tail: '- 寫完後只回一行「HANDOFF-WRITTEN」\n# HANDOFF\n- 舊 ref：{{old_ref}}' }

function setup(over: Partial<RelayPromptEditorProps> = {}) {
  const props: RelayPromptEditorProps = {
    hostId: H, kind: 'write', fixed: FIXED, defaultBody: DEF, stored: '', variables: VARS, locked: false,
    onSave: vi.fn(async () => {}), onRestore: vi.fn(async () => {}), ...over,
  }
  const view = render(<RelayPromptEditor {...props} />)
  return { props, view }
}
const box = () => screen.getByTestId('relay-prompt-write-box') as HTMLTextAreaElement
const save = () => screen.getByTestId('relay-prompt-write-save') as HTMLButtonElement
const restore = () => screen.getByTestId('relay-prompt-write-restore') as HTMLButtonElement
const type = (text: string) => fireEvent.change(box(), { target: { value: text } })

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  clearAllRelayPromptDrafts()
})
afterEach(() => { cleanup(); clearAllRelayPromptDrafts() })

describe('RelayPromptEditor', () => {
  it('shows the fixed head and tail read-only around the one editable box, and what the mod fills in', () => {
    setup()
    expect(screen.getByTestId('relay-prompt-write-head').tagName).toBe('PRE')
    expect(screen.getByTestId('relay-prompt-write-head').textContent).toBe(FIXED.head)
    expect(screen.getByTestId('relay-prompt-write-tail').tagName).toBe('PRE')
    expect(screen.getByTestId('relay-prompt-write-tail').textContent).toBe(FIXED.tail)
    expect(screen.getAllByRole('textbox')).toEqual([box()])
    expect(screen.getByTestId('relay-prompt-write-mod-vars').textContent).toBe('{{op}} {{nonce}} 由 mod 填入。')
  })

  it('a seed with no tail shows no tail block', () => {
    setup({ kind: 'seed', fixed: { head: '↪ 接手自 {{old_ref}}\n[pdx-relay seed op={{op}} n={{nonce}}] ', tail: '' } })
    expect(screen.getByTestId('relay-prompt-seed-head')).toBeTruthy()
    expect(screen.queryByTestId('relay-prompt-seed-tail')).toBeNull()
  })

  it('the box starts with the stored text, else the default; the badge says which', () => {
    const first = setup({ stored: 'my own body' })
    expect(box().value).toBe('my own body')
    expect(screen.getByTestId('relay-prompt-write-badge').textContent).toBe('自訂')
    first.view.unmount()
    setup({ stored: '' })
    expect(box().value).toBe(DEF)
    expect(screen.getByTestId('relay-prompt-write-badge').textContent).toBe('預設')
  })

  it('lists the variables the daemon reports, each with its description', () => {
    setup({ variables: [...VARS, 'git'] })
    const list = screen.getByTestId('relay-prompt-write-variables')
    expect(list.textContent).toContain('{{path}}')
    expect(list.textContent).toContain('接力檔路徑')
    expect(list.textContent).toContain('{{whoami}}')
    expect(list.textContent).toContain('{{git}}') // a newer daemon's variable: listed, without a description
    expect(list.textContent).not.toContain('hosts.relay.prompts.var.git')
  })

  it('shows the byte counter against 16 384', () => {
    setup()
    expect(screen.getByTestId('relay-prompt-write-counter').textContent).toBe(`${new TextEncoder().encode(DEF).length} / 16384`)
    type('中')
    expect(screen.getByTestId('relay-prompt-write-counter').textContent).toBe('3 / 16384')
  })

  it.each([
    ['the tag', 'see [pdx-relay op=1]', '不能包含 [pdx-relay'],
    ['a control character', 'a\u0007b', '控制字元'],
    ['16 385 bytes', 'a'.repeat(16385), '16384'],
    ['an unpaired surrogate', 'a\ud83db', 'surrogate'],
  ])('%s shows the error and disables 儲存', (_label, text, message) => {
    setup()
    type('a fine body')
    expect(save().disabled).toBe(false)
    type(text)
    expect(screen.getByTestId('relay-prompt-write-problem').textContent).toContain(message)
    expect(save().disabled).toBe(true)
  })

  it('16 384 bytes may be saved', () => {
    setup()
    type('a'.repeat(16384))
    expect(screen.queryByTestId('relay-prompt-write-problem')).toBeNull()
    expect(save().disabled).toBe(false)
  })

  it('儲存 is disabled while the box holds what is stored', () => {
    setup({ stored: 'mine' })
    expect(save().disabled).toBe(true)
    type('mine, edited')
    expect(save().disabled).toBe(false)
    type('mine')
    expect(save().disabled).toBe(true)
    expect(readRelayPromptDraft(KEY)).toBeUndefined()
  })

  it('a save sends the text with CRLF as LF, then forgets the draft', async () => {
    // jsdom (like a browser) already gives a textarea's value with LF, so the CRLF draft is seeded directly: a paste
    // kept in the draft memory is what reaches the save.
    writeRelayPromptDraft(KEY, 'line 1\r\nline 2')
    const { props } = setup()
    await act(async () => { fireEvent.click(save()) })
    expect(props.onSave).toHaveBeenCalledWith('line 1\nline 2')
    expect(readRelayPromptDraft(KEY)).toBeUndefined()
  })

  it('an emoji (a surrogate pair) is saved as typed', async () => {
    const { props } = setup()
    type('交給你了 😀')
    expect(screen.queryByTestId('relay-prompt-write-problem')).toBeNull()
    await act(async () => { fireEvent.click(save()) })
    expect(props.onSave).toHaveBeenCalledWith('交給你了 😀')
  })

  it('saving the default text sends ""', async () => {
    const { props } = setup({ stored: 'mine' })
    type(DEF)
    await act(async () => { fireEvent.click(save()) })
    expect(props.onSave).toHaveBeenCalledWith('')
  })

  it('還原預設 sends "" through onRestore and forgets the draft; it is disabled at the default', async () => {
    const { props, view } = setup({ stored: 'mine' })
    type('mine, half edited')
    expect(restore().disabled).toBe(false)
    await act(async () => { fireEvent.click(restore()) })
    expect(props.onRestore).toHaveBeenCalledTimes(1)
    expect(readRelayPromptDraft(KEY)).toBeUndefined()
    view.unmount()
    setup({ stored: '' })
    expect(restore().disabled).toBe(true)
  })

  it('with the default stored, 還原預設 throws away an unsaved draft without a save', async () => {
    const { props } = setup({ stored: '' })
    type('not saved')
    expect(restore().disabled).toBe(false)
    await act(async () => { fireEvent.click(restore()) })
    expect(props.onRestore).not.toHaveBeenCalled()
    expect(box().value).toBe(DEF)
    expect(restore().disabled).toBe(true)
  })

  it('a failed save keeps the draft and shows why', async () => {
    const { props } = setup({ onSave: vi.fn(async () => { throw new Error('prompt_write: a relay prompt may not contain [pdx-relay') }) })
    type('mine')
    await act(async () => { fireEvent.click(save()) })
    expect(props.onSave).toHaveBeenCalledWith('mine')
    expect(screen.getByTestId('relay-prompt-write-error').textContent).toContain('prompt_write: a relay prompt may not contain [pdx-relay')
    expect(box().value).toBe('mine')
    expect(readRelayPromptDraft(KEY)).toBe('mine')
  })

  it('locked: the box is read-only and both buttons are disabled', () => {
    setup({ locked: true, stored: 'mine' })
    expect(box().readOnly).toBe(true)
    expect(save().disabled).toBe(true)
    expect(restore().disabled).toBe(true)
  })
})

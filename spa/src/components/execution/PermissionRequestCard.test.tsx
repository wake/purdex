// spa/src/components/execution/PermissionRequestCard.test.tsx — permission channel plan Task 9 (spec §5.3, §5.5).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import PermissionRequestCard, { PermissionExpiredNotice, NOTE_MAX_BYTES, PREVIEW_CHARS } from './PermissionRequestCard'
import type { PermissionRequestState } from '../../lib/nex/permissions'
import { useI18nStore } from '../../stores/useI18nStore'
import { clearAllPermissionCards, permissionCardKey, readPermissionCard } from '../../lib/nex/permission-card-memory'

const bytes = (s: string) => new TextEncoder().encode(s).length
const KEY = permissionCardKey('h', 'exc_1', 'req_a')
const NOW = new Date('2026-10-07T10:00:00Z').getTime()
const request = (extra: Partial<PermissionRequestState> = {}): PermissionRequestState => ({
  requestId: 'req_a', toolName: 'Bash', requestedAt: NOW - 65_000, status: 'pending',
  input: { command: 'rm -rf build', description: 'Clean the build' }, description: 'Clean the build', ...extra,
})

describe('PermissionRequestCard', () => {
  const onAllow = vi.fn(), onDeny = vi.fn()
  const renderCard = (props: Partial<Parameters<typeof PermissionRequestCard>[0]> = {}) =>
    render(<PermissionRequestCard request={request()} disabled={false} onAllow={onAllow} onDeny={onDeny} memoryKey={KEY} {...props} />)

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    onAllow.mockReset()
    onDeny.mockReset()
    useI18nStore.getState().setLocale('en')
  })
  afterEach(() => {
    vi.useRealTimers()
    clearAllPermissionCards()
  })

  it('a remount under the same memoryKey brings back the note, the open note field and the expanded preview; another key starts fresh', () => {
    const long = { command: `${'x'.repeat(500)} THE-TAIL` }
    const first = renderCard({ request: request({ input: long }) })
    fireEvent.click(screen.getByTestId('permission-expand'))
    fireEvent.click(screen.getByTestId('permission-deny'))
    fireEvent.change(screen.getByTestId('permission-deny-note'), { target: { value: '中'.repeat(1000) } })
    expect(readPermissionCard(KEY)).toEqual({ note: '中'.repeat(682), noteOpen: true, expanded: true })
    first.unmount()

    const second = renderCard({ request: request({ input: long }) })
    expect((screen.getByTestId('permission-deny-note') as HTMLInputElement).value).toBe('中'.repeat(682))
    expect(screen.getByTestId('permission-input')).toHaveTextContent('THE-TAIL')
    // Escape hides the field and collapsing the preview is remembered too; the typed note stays for a reopen.
    fireEvent.keyDown(screen.getByTestId('permission-deny-note'), { key: 'Escape' })
    fireEvent.click(screen.getByTestId('permission-expand'))
    expect(readPermissionCard(KEY)).toEqual({ note: '中'.repeat(682), noteOpen: false, expanded: false })
    second.unmount()

    renderCard({ request: request({ requestId: 'req_b', input: long }), memoryKey: permissionCardKey('h', 'exc_1', 'req_b') })
    expect(screen.queryByTestId('permission-deny-note')).toBeNull()
    expect(screen.getByTestId('permission-input')).not.toHaveTextContent('THE-TAIL')
  })

  it('shows the tool (display name first), description, the command, the reason, the blocked path and the asking subagent', () => {
    renderCard({
      request: request({ displayName: 'Shell', decisionReason: 'This command requires approval', blockedPath: '/etc/hosts', agentId: 'a8fb' }),
      agentLabel: 'Probe the repo',
    })
    const card = screen.getByTestId('permission-card')
    expect(screen.getByTestId('permission-tool')).toHaveTextContent(/^Shell$/)
    expect(card).toHaveTextContent('Clean the build')
    expect(screen.getByTestId('permission-input')).toHaveTextContent('rm -rf build')
    // The command field, not the pretty-printed JSON.
    expect(screen.getByTestId('permission-input')).not.toHaveTextContent('"command"')
    expect(card).toHaveTextContent('This command requires approval')
    expect(card).toHaveTextContent('/etc/hosts')
    expect(card).toHaveTextContent('subagent: Probe the repo')
  })

  it('without display name / agent / reason it shows the tool name and no such lines', () => {
    renderCard({ request: request({ description: undefined }) })
    const card = screen.getByTestId('permission-card')
    expect(screen.getByTestId('permission-tool')).toHaveTextContent(/^Bash$/)
    expect(card).not.toHaveTextContent('subagent:')
    expect(screen.queryByTestId('permission-reason')).toBeNull()
    expect(screen.queryByTestId('permission-blocked-path')).toBeNull()
  })

  it('the time waited ticks every second from requestedAt', () => {
    renderCard({ request: request({ requestedAt: NOW - 5_000 }) })
    expect(screen.getByTestId('permission-waited')).toHaveTextContent('5s')
    act(() => { vi.advanceTimersByTime(1_000) })
    expect(screen.getByTestId('permission-waited')).toHaveTextContent('6s')
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(screen.getByTestId('permission-waited')).toHaveTextContent('1m')
  })

  it('an input without a command shows pretty-printed JSON, bounded to the preview length with an expand control', () => {
    const long = 'x'.repeat(1000) + 'THE-TAIL'
    renderCard({ request: request({ toolName: 'Write', input: { file_path: '/w/a.txt', content: long } }) })
    const box = screen.getByTestId('permission-input')
    expect(box).toHaveTextContent('"file_path": "/w/a.txt"')
    expect(box.textContent!.replace(/…$/, '').length).toBeLessThanOrEqual(PREVIEW_CHARS)
    expect(box).not.toHaveTextContent('THE-TAIL')
    fireEvent.click(screen.getByTestId('permission-expand'))
    expect(screen.getByTestId('permission-input')).toHaveTextContent('THE-TAIL')
    fireEvent.click(screen.getByTestId('permission-expand'))
    expect(screen.getByTestId('permission-input')).not.toHaveTextContent('THE-TAIL')
  })

  it('a short input has no expand control', () => {
    renderCard()
    expect(screen.getByTestId('permission-input')).toHaveTextContent('rm -rf build')
    expect(screen.queryByTestId('permission-expand')).toBeNull()
  })

  it('同意 calls onAllow', () => {
    renderCard()
    fireEvent.click(screen.getByTestId('permission-allow'))
    expect(onAllow).toHaveBeenCalledTimes(1)
    expect(onDeny).not.toHaveBeenCalled()
  })

  it('拒絕 first reveals the note; pressing it again sends the deny with the note', () => {
    renderCard()
    expect(screen.queryByTestId('permission-deny-note')).toBeNull()
    fireEvent.click(screen.getByTestId('permission-deny'))
    expect(onDeny).not.toHaveBeenCalled()
    const note = screen.getByTestId('permission-deny-note')
    fireEvent.change(note, { target: { value: '不要動 prod' } })
    fireEvent.click(screen.getByTestId('permission-deny'))
    expect(onDeny).toHaveBeenCalledWith('不要動 prod')
  })

  it('a deny without a note sends an empty note; Enter in the note sends too; Escape hides it', () => {
    renderCard()
    fireEvent.click(screen.getByTestId('permission-deny'))
    fireEvent.click(screen.getByTestId('permission-deny'))
    expect(onDeny).toHaveBeenLastCalledWith('')
    fireEvent.change(screen.getByTestId('permission-deny-note'), { target: { value: 'use a dry run' } })
    fireEvent.keyDown(screen.getByTestId('permission-deny-note'), { key: 'Enter' })
    expect(onDeny).toHaveBeenLastCalledWith('use a dry run')
    fireEvent.keyDown(screen.getByTestId('permission-deny-note'), { key: 'Escape' })
    expect(screen.queryByTestId('permission-deny-note')).toBeNull()
  })

  it('the note is capped by UTF-8 bytes (Nexen caps message at 2048 bytes), not by characters', () => {
    renderCard()
    fireEvent.click(screen.getByTestId('permission-deny'))
    const note = () => screen.getByTestId('permission-deny-note') as HTMLInputElement
    expect(note().maxLength).toBe(-1)
    fireEvent.change(note(), { target: { value: 'a'.repeat(3000) } })
    expect(note().value).toBe('a'.repeat(NOTE_MAX_BYTES))
    // 中 is 3 bytes: 1000 of them are 3000 bytes → 682 characters (2046 bytes), never a split character.
    fireEvent.change(note(), { target: { value: '中'.repeat(1000) } })
    expect(note().value).toBe('中'.repeat(682))
    expect(bytes(note().value)).toBeLessThanOrEqual(NOTE_MAX_BYTES)
    // A 4-byte emoji at the edge is dropped whole, not cut into a lone surrogate.
    fireEvent.change(note(), { target: { value: 'a'.repeat(2046) + '😀' } })
    expect(note().value).toBe('a'.repeat(2046))
    fireEvent.click(screen.getByTestId('permission-deny'))
    expect(bytes(onDeny.mock.calls[0][0])).toBeLessThanOrEqual(NOTE_MAX_BYTES)
  })

  it('invalid_permission_answer on the note: the error shows and the note stays open, editable, with its text', () => {
    const { rerender } = renderCard()
    fireEvent.click(screen.getByTestId('permission-deny'))
    fireEvent.change(screen.getByTestId('permission-deny-note'), { target: { value: 'my note' } })
    rerender(<PermissionRequestCard request={request()} disabled onAllow={onAllow} onDeny={onDeny} memoryKey={KEY} />)
    expect(screen.getByTestId('permission-deny')).toBeDisabled()
    rerender(<PermissionRequestCard request={request()} disabled={false} onAllow={onAllow} onDeny={onDeny} memoryKey={KEY}
      error={{ code: 'invalid_permission_answer', message: 'message exceeds 2048 bytes', field: 'message' }} />)
    expect(screen.getByTestId('permission-error')).toHaveTextContent('message exceeds 2048 bytes')
    const note = screen.getByTestId('permission-deny-note') as HTMLInputElement
    expect(note.value).toBe('my note')
    expect(note).not.toBeDisabled()
    fireEvent.change(note, { target: { value: 'shorter' } })
    expect(note.value).toBe('shorter')
  })

  it('other errors show their line; none → no error element', () => {
    const { rerender } = renderCard()
    expect(screen.queryByTestId('permission-error')).toBeNull()
    rerender(<PermissionRequestCard request={request()} disabled={false} onAllow={onAllow} onDeny={onDeny} memoryKey={KEY}
      error={{ code: 'permission_not_found', message: 'no such request' }} />)
    expect(screen.getByTestId('permission-error')).toBeInTheDocument()
    rerender(<PermissionRequestCard request={request()} disabled={false} onAllow={onAllow} onDeny={onDeny} memoryKey={KEY}
      error={{ code: 'network', message: 'Failed to fetch' }} />)
    expect(screen.getByTestId('permission-error')).toHaveTextContent('Failed to fetch')
  })

  it('disabled: both buttons are disabled and clicks do nothing', () => {
    renderCard({ disabled: true })
    expect(screen.getByTestId('permission-allow')).toBeDisabled()
    expect(screen.getByTestId('permission-deny')).toBeDisabled()
    fireEvent.click(screen.getByTestId('permission-allow'))
    fireEvent.click(screen.getByTestId('permission-deny'))
    expect(onAllow).not.toHaveBeenCalled()
    expect(onDeny).not.toHaveBeenCalled()
  })

  it('reads in zh-TW: 同意 / 拒絕', () => {
    useI18nStore.getState().setLocale('zh-TW')
    renderCard()
    expect(screen.getByTestId('permission-allow')).toHaveTextContent('同意')
    expect(screen.getByTestId('permission-deny')).toHaveTextContent('拒絕')
  })
})

describe('PermissionExpiredNotice', () => {
  afterEach(() => { useI18nStore.getState().setLocale('en') })

  it.each([[300, 5], [900, 15], [3600, 60], [420, 7], [450, 8]])('timeout %is → 「已逾時自動拒絕（%i 分鐘）」', (timeoutS, n) => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<PermissionExpiredNotice timeoutS={timeoutS} />)
    expect(screen.getByTestId('permission-expired')).toHaveTextContent(`已逾時自動拒絕（${n} 分鐘）`)
  })

  it('without a timeout it still says it was denied on timeout; English reads too', () => {
    useI18nStore.getState().setLocale('zh-TW')
    const { unmount } = render(<PermissionExpiredNotice />)
    expect(screen.getByTestId('permission-expired')).toHaveTextContent('已逾時自動拒絕')
    unmount()
    useI18nStore.getState().setLocale('en')
    render(<PermissionExpiredNotice timeoutS={300} />)
    expect(screen.getByTestId('permission-expired')).toHaveTextContent(/5 min/)
  })
})

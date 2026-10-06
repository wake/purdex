import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, within } from '@testing-library/react'
import { ConversationRow } from './ConversationRow'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ConversationRow as Row } from '../../lib/nex/conversations-api'

const HOME = '/Users/wake'
const NOW = 1_700_000_000_000
const HOUR = 3_600_000
const row = (o: Partial<Row> = {}): Row => ({
  session_id: 'aaaaaaaa-1111-2222-3333-444455556666',
  title: 'Fix the login flow',
  title_source: 'ai',
  cwd: '/Users/wake/Workspace/purdex',
  cwd_exists: true,
  last_activity_at: NOW - 3 * HOUR,
  last_in: 'terminal',
  ...o,
})

describe('ConversationRow', () => {
  beforeEach(() => { act(() => { useI18nStore.getState().setLocale('zh-TW') }) })
  afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

  it('shows the title, the ~ cwd (full cwd in title=), the age with 前 and the 上次在 chip', () => {
    render(<ConversationRow row={row()} state="ended" home={HOME} now={NOW} onRebuild={vi.fn()} />)
    expect(screen.getByTestId('conversation-row')).toHaveTextContent('Fix the login flow')
    const cwd = screen.getByTestId('conversation-row-cwd')
    expect(cwd).toHaveTextContent('~/Workspace/purdex')
    expect(cwd).toHaveAttribute('title', '/Users/wake/Workspace/purdex')
    expect(screen.getByTestId('conversation-row-age')).toHaveTextContent('3 小時前')
    expect(screen.getByTestId('conversation-row-last-in')).toHaveTextContent('上次在終端機')
  })

  it('a worker-last row says 上次在 Worker', () => {
    render(<ConversationRow row={row({ last_in: 'worker' })} state="ended" home={HOME} now={NOW} onRebuild={vi.fn()} />)
    expect(screen.getByTestId('conversation-row-last-in')).toHaveTextContent('上次在 Worker')
  })

  it('ages bucket by minutes and days', () => {
    const { rerender } = render(<ConversationRow row={row({ last_activity_at: NOW - 5 * 60_000 })} state="ended" home={HOME} now={NOW} />)
    expect(screen.getByTestId('conversation-row-age')).toHaveTextContent('5 分鐘前')
    rerender(<ConversationRow row={row({ last_activity_at: NOW - 49 * HOUR })} state="ended" home={HOME} now={NOW} />)
    expect(screen.getByTestId('conversation-row-age')).toHaveTextContent('2 天前')
  })

  it('a last activity ahead of this clock reads 剛剛, never a negative age', () => {
    render(<ConversationRow row={row({ last_activity_at: NOW + 10 * 60_000 })} state="ended" home={HOME} now={NOW} />)
    expect(screen.getByTestId('conversation-row-age')).toHaveTextContent('剛剛')
    expect(screen.getByTestId('conversation-row-age').textContent).not.toMatch(/-/)
  })

  it('重建… calls onRebuild', () => {
    const onRebuild = vi.fn()
    render(<ConversationRow row={row()} state="ended" home={HOME} now={NOW} onRebuild={onRebuild} />)
    const btn = screen.getByTestId('conversation-row-rebuild')
    expect(btn).toHaveTextContent('重建…')
    expect(btn).not.toBeDisabled()
    fireEvent.click(btn)
    expect(onRebuild).toHaveBeenCalledTimes(1)
  })

  it('disabled → the button is shown disabled and does nothing', () => {
    const onRebuild = vi.fn()
    render(<ConversationRow row={row()} state="ended" home={HOME} now={NOW} disabled onRebuild={onRebuild} />)
    const btn = screen.getByTestId('conversation-row-rebuild')
    expect(btn).toBeDisabled()
    fireEvent.click(btn)
    expect(onRebuild).not.toHaveBeenCalled()
  })

  it('no cwd → no cwd, no button, no cwd-missing text', () => {
    render(<ConversationRow row={row({ cwd: undefined, cwd_exists: false })} state="ended" home={HOME} now={NOW} onRebuild={vi.fn()} />)
    expect(screen.queryByTestId('conversation-row-cwd')).toBeNull()
    expect(screen.queryByTestId('conversation-row-rebuild')).toBeNull()
    expect(screen.queryByTestId('conversation-row-cwd-missing')).toBeNull()
  })

  it('a cwd that no longer exists → 工作目錄已不存在 and no button', () => {
    render(<ConversationRow row={row({ cwd_exists: false })} state="ended" home={HOME} now={NOW} onRebuild={vi.fn()} />)
    expect(screen.getByTestId('conversation-row-cwd-missing')).toHaveTextContent('工作目錄已不存在')
    expect(screen.queryByTestId('conversation-row-rebuild')).toBeNull()
    expect(screen.getByTestId('conversation-row-cwd')).toHaveTextContent('~/Workspace/purdex')
  })

  it('an ended row is not aria-disabled and has no gone note, even with its button disabled', () => {
    render(<ConversationRow row={row()} state="ended" home={HOME} now={NOW} disabled onRebuild={vi.fn()} />)
    const el = screen.getByTestId('conversation-row')
    expect(el).not.toHaveAttribute('aria-disabled')
    expect(el).not.toHaveClass('opacity-60')
    expect(screen.queryByTestId('conversation-row-gone-note')).toBeNull()
  })

  describe('gone (U3)', () => {
    it('the whole row is disabled and muted, with no button, and ends with the note', () => {
      const onRebuild = vi.fn()
      render(<ConversationRow row={row()} state="gone" home={HOME} now={NOW} disabled onRebuild={onRebuild} />)
      const el = screen.getByTestId('conversation-row')
      expect(el).toHaveAttribute('data-state', 'gone')
      expect(el).toHaveAttribute('aria-disabled', 'true')
      expect(el).toHaveClass('opacity-60')
      expect(screen.queryByTestId('conversation-row-rebuild')).toBeNull()
      expect(within(el).queryByRole('button')).toBeNull()
      const note = screen.getByTestId('conversation-row-gone-note')
      expect(note).toHaveTextContent('對話檔已清除，無法再啟動')
      expect(el.lastElementChild).toBe(note)
      fireEvent.click(el)
      expect(onRebuild).not.toHaveBeenCalled()
    })

    it('still shows the title, the ~ cwd, the age and the 上次在 chip', () => {
      render(<ConversationRow row={row({ last_in: 'worker' })} state="gone" home={HOME} now={NOW} disabled />)
      expect(screen.getByTestId('conversation-row')).toHaveTextContent('Fix the login flow')
      expect(screen.getByTestId('conversation-row-cwd')).toHaveTextContent('~/Workspace/purdex')
      expect(screen.getByTestId('conversation-row-age')).toHaveTextContent('3 小時前')
      expect(screen.getByTestId('conversation-row-last-in')).toHaveTextContent('上次在 Worker')
    })

    it('a gone row whose cwd no longer exists shows only the note, not 工作目錄已不存在', () => {
      render(<ConversationRow row={row({ cwd_exists: false })} state="gone" home={HOME} now={NOW} disabled />)
      expect(screen.queryByTestId('conversation-row-cwd-missing')).toBeNull()
      expect(screen.getByTestId('conversation-row-gone-note')).toBeInTheDocument()
    })
  })

  it('en copy', () => {
    act(() => { useI18nStore.getState().setLocale('en') })
    render(<ConversationRow row={row({ cwd_exists: false })} state="ended" home={HOME} now={NOW} />)
    expect(screen.getByTestId('conversation-row-age')).toHaveTextContent('3h ago')
    expect(screen.getByTestId('conversation-row-last-in')).toHaveTextContent('Last in terminal')
    expect(screen.getByTestId('conversation-row-cwd-missing')).toHaveTextContent('Working directory no longer exists')
  })

  it('en copy of the gone note', () => {
    act(() => { useI18nStore.getState().setLocale('en') })
    render(<ConversationRow row={row()} state="gone" home={HOME} now={NOW} disabled />)
    expect(screen.getByTestId('conversation-row-gone-note'))
      .toHaveTextContent('Transcript cleaned up; it can no longer be started')
  })
})

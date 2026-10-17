// spa/src/components/relay/HalfHandoffMark.test.tsx — the quiet 「已寫好接力檔」 mark: two forms, a tooltip with the path and the time.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { HalfHandoffMark } from './HalfHandoffMark'
import { useI18nStore } from '../../stores/useI18nStore'

const at = (h: number, m: number, day = 17) => new Date(2026, 9, day, h, m, 0).getTime()

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(at(15, 0))
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('HalfHandoffMark', () => {
  it('renders nothing without a handoff', () => {
    const { container } = render(<HalfHandoffMark handoff={null} variant="chip" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('chip: the words, with the path and the time of day in the tooltip', () => {
    render(<HalfHandoffMark handoff={{ path: '/w/purdex/handoff.md', writtenAt: at(9, 5) }} variant="chip" />)
    const mark = screen.getByTestId('half-handoff-mark')
    expect(mark).toHaveAttribute('data-variant', 'chip')
    expect(mark).toHaveTextContent('已寫好接力檔')
    expect(mark).toHaveAttribute('title', '接力檔：/w/purdex/handoff.md（09:05 寫好）')
  })

  it('icon: no visible words; the words are its accessible name; the tooltip is the same', () => {
    render(<HalfHandoffMark handoff={{ path: '/w/h.md', writtenAt: at(9, 5) }} variant="icon" />)
    const mark = screen.getByTestId('half-handoff-mark')
    expect(mark).toHaveAttribute('data-variant', 'icon')
    expect(mark).toHaveTextContent('')
    expect(screen.getByRole('img', { name: '已寫好接力檔' })).toBe(mark)
    expect(mark).toHaveAttribute('title', '接力檔：/w/h.md（09:05 寫好）')
    expect(mark.querySelector('svg')).not.toBeNull()
  })

  it('a handoff from an earlier day carries its date', () => {
    render(<HalfHandoffMark handoff={{ path: '/w/h.md', writtenAt: at(23, 40, 15) }} variant="chip" />)
    expect(screen.getByTestId('half-handoff-mark')).toHaveAttribute('title', '接力檔：/w/h.md（2026-10-15 23:40 寫好）')
  })

  it('a written time that is not a time leaves the time out: the tooltip is the path alone', () => {
    for (const bad of [1e20, NaN]) {
      const { unmount } = render(<HalfHandoffMark handoff={{ path: '/w/h.md', writtenAt: bad }} variant="chip" />)
      const title = screen.getByTestId('half-handoff-mark').getAttribute('title')
      expect(title).toBe('接力檔：/w/h.md')
      expect(title).not.toMatch(/NaN|Invalid/)
      unmount()
    }
    useI18nStore.getState().setLocale('en')
    render(<HalfHandoffMark handoff={{ path: '/w/h.md', writtenAt: NaN }} variant="icon" />)
    expect(screen.getByTestId('half-handoff-mark')).toHaveAttribute('title', 'Handoff file: /w/h.md')
  })

  it('English wording', () => {
    useI18nStore.getState().setLocale('en')
    render(<HalfHandoffMark handoff={{ path: '/w/h.md', writtenAt: at(9, 5) }} variant="chip" />)
    const mark = screen.getByTestId('half-handoff-mark')
    expect(mark).toHaveTextContent('Handoff file written')
    expect(mark).toHaveAttribute('title', 'Handoff file: /w/h.md (written 09:05)')
  })

  it('uses theme tokens only: no colour literal in its classes or style', () => {
    render(<><HalfHandoffMark handoff={{ path: '/p', writtenAt: 1 }} variant="chip" /><HalfHandoffMark handoff={{ path: '/p', writtenAt: 1 }} variant="icon" /></>)
    for (const el of screen.getAllByTestId('half-handoff-mark')) {
      expect(el.getAttribute('style')).toBeNull()
      expect(el.className).not.toMatch(/#[0-9a-f]{3,8}|\b(red|green|yellow|amber|rose|orange|blue)-\d/)
      expect(el.className).toMatch(/text-text-(muted|secondary)/)
    }
  })
})

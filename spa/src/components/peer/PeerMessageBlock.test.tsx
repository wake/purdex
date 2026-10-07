// spa/src/components/peer/PeerMessageBlock.test.tsx — another conversation's
// message (peer mailbox spec §7, design mock `ublock peer`): a left rule in the
// info colour, 「來自 X · 時間」, and the body drawn like agent prose. Plain
// props, so the terminal underlying can reuse it.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, within, cleanup } from '@testing-library/react'
import PeerMessageBlock from './PeerMessageBlock'
import { useI18nStore } from '../../stores/useI18nStore'
import { getWorkerTheme } from '../../lib/worker-theme/registry'

const timeFormat = new Intl.DateTimeFormat('en-GB', { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' })
// The recorded fixture's created_at (2026-10-07 19:34:17 UTC).
const AT = 1791401657626

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('en') })
afterEach(() => { useI18nStore.getState().setLocale('en') })

describe('PeerMessageBlock', () => {
  it('heads the block with the sender and the time', () => {
    render(<PeerMessageBlock fromName="mlab/purdex-54" text="hi" at={AT} timeFormat={timeFormat} />)
    const header = screen.getByTestId('peer-message-header')
    expect(header).toHaveTextContent(/^From mlab\/purdex-54 · 19:34$/)
    // The full date and time on hover, like the turn footer.
    expect(header.getAttribute('title')).toMatch(/2026/)
  })

  it('says 「來自 X · 時間」 in zh-TW', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<PeerMessageBlock fromName="mlab/purdex-54" text="hi" at={AT} timeFormat={timeFormat} />)
    expect(screen.getByTestId('peer-message-header')).toHaveTextContent(/^來自 mlab\/purdex-54 · 19:34$/)
  })

  it('drops the time when the event carried none', () => {
    render(<PeerMessageBlock fromName="mlab/purdex-54" text="hi" at={0} timeFormat={timeFormat} />)
    const header = screen.getByTestId('peer-message-header')
    expect(header).toHaveTextContent(/^From mlab\/purdex-54$/)
    expect(header).not.toHaveAttribute('title')
  })

  it('draws the body like agent prose (markdown), carrying the search anchor', () => {
    render(<PeerMessageBlock fromName="a/b" text={'**bold** and `code`'} at={AT} timeFormat={timeFormat} searchUnit="7:0:text" />)
    const block = screen.getByTestId('peer-message')
    const prose = within(block).getByTestId('room-prose')
    expect(prose.querySelector('strong')).toHaveTextContent('bold')
    expect(prose.querySelector('[data-search-unit="7:0:text"]')).not.toBeNull()
    // The header is not part of the searchable body.
    expect(screen.getByTestId('peer-message-header')).not.toHaveAttribute('data-search-unit')
  })

  it('has a left rule and a header in the info colour, defined by the pane theme', () => {
    render(<PeerMessageBlock fromName="a/b" text="hi" at={AT} timeFormat={timeFormat} />)
    expect(screen.getByTestId('peer-message').className).toContain('border-l-[var(--wt-peer-color)]')
    expect(screen.getByTestId('peer-message-header').className).toContain('text-[var(--wt-peer-color)]')
    expect(getWorkerTheme('purdex').vars['peer-color']).toMatch(/var\(--/)
  })

  it('is never drawn as the user\'s own line', () => {
    render(<PeerMessageBlock fromName="a/b" text="hi" at={AT} timeFormat={timeFormat} />)
    expect(screen.queryByTestId('room-user-line')).toBeNull()
    expect(screen.queryByTestId('chat-bubble-user')).toBeNull()
  })
})

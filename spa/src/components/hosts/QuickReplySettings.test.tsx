import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QuickReplySettings } from './QuickReplySettings'
import { CommandsSection } from './CommandsSection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore, type HostConfigEntry } from '../../stores/useHostConfigStore'
import { putHostConfig, type QuickReply } from '../../lib/host-config-api'

vi.mock('../../lib/host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-config-api')>()),
  putHostConfig: vi.fn(),
}))

const H = 'h1'
const saveQuickReplies = vi.fn()

function entry(quickReplies: QuickReply[], revision: number, supported = true): HostConfigEntry {
  const e = emptyHostConfigEntry('ready')
  return { ...e, quickReplies, quickRepliesSupported: supported, revisions: { ...e.revisions, quickReplies: revision } }
}

function seed(e: HostConfigEntry) {
  useHostConfigStore.setState({ byHost: { [H]: e }, load: vi.fn(async () => {}), saveQuickReplies })
}

const stored = () => useHostConfigStore.getState().byHost[H].quickReplies
const rowIds = () => screen.queryAllByTestId(/^quick-reply-row-/).map((r) => r.dataset.testid!.replace('quick-reply-row-', ''))

beforeEach(() => {
  // Like the real save: the daemon stores the list and bumps the revision.
  saveQuickReplies.mockReset().mockImplementation(async (hostId: string, items: QuickReply[]) => {
    useHostConfigStore.setState((s) => {
      const cur = s.byHost[hostId]
      return { byHost: { ...s.byHost, [hostId]: { ...cur, quickReplies: items, revisions: { ...cur.revisions, quickReplies: cur.revisions.quickReplies + 1 } } } }
    })
  })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } },
  })
  seed(entry([], 0))
})

describe('QuickReplySettings', () => {
  it('lists the defaults when never written', () => {
    render(<QuickReplySettings hostId={H} />)
    expect(rowIds()).toEqual(['continue', 'run-tests', 'explain'])
    expect(screen.getByTestId('quick-reply-row-run-tests')).toHaveTextContent('run the tests')
    expect(screen.getByTestId('quick-replies-defaults')).toBeInTheDocument()
    expect(saveQuickReplies).not.toHaveBeenCalled()
  })

  it('adds, edits, reorders and deletes', async () => {
    seed(entry([{ id: 'a', text: 'alpha' }, { id: 'b', text: 'beta' }], 3))
    render(<QuickReplySettings hostId={H} />)
    expect(screen.queryByTestId('quick-replies-defaults')).toBeNull()

    // add
    fireEvent.click(screen.getByTestId('quick-reply-add'))
    fireEvent.change(screen.getByTestId('quick-reply-input'), { target: { value: '  gamma  ' } })
    fireEvent.click(screen.getByTestId('quick-reply-save'))
    await waitFor(() => expect(stored().map((r) => r.text)).toEqual(['alpha', 'beta', 'gamma']))
    await waitFor(() => expect(screen.queryByTestId('quick-reply-input')).toBeNull())

    // an empty text is refused without a save
    fireEvent.click(screen.getByTestId('quick-reply-edit-a'))
    fireEvent.change(screen.getByTestId('quick-reply-input'), { target: { value: '   ' } })
    fireEvent.click(screen.getByTestId('quick-reply-save'))
    expect(screen.getByTestId('quick-reply-error')).toBeInTheDocument()
    expect(saveQuickReplies).toHaveBeenCalledTimes(1)

    // edit
    fireEvent.change(screen.getByTestId('quick-reply-input'), { target: { value: 'ALPHA' } })
    fireEvent.click(screen.getByTestId('quick-reply-save'))
    await waitFor(() => expect(stored()[0]).toEqual({ id: 'a', text: 'ALPHA' }))

    // reorder
    fireEvent.click(screen.getByTestId('quick-reply-down-a'))
    await waitFor(() => expect(stored().map((r) => r.id)).toEqual(['b', 'a', stored()[2].id]))
    expect(rowIds().slice(0, 2)).toEqual(['b', 'a'])

    // delete asks first
    fireEvent.click(screen.getByTestId('quick-reply-delete-b'))
    expect(saveQuickReplies).toHaveBeenCalledTimes(3)
    fireEvent.click(screen.getByTestId('quick-reply-delete-confirm-b'))
    await waitFor(() => expect(stored().map((r) => r.text)).toEqual(['ALPHA', 'gamma']))
  })

  it('saves through the queue with the base revision', async () => {
    // The real store save, over a daemon that bumps the revision on every PUT.
    let revision = 0
    vi.mocked(putHostConfig).mockReset().mockImplementation(async (_h, _c, items) => ({ items, revision: ++revision }) as never)
    useHostConfigStore.setState({ saveQuickReplies: useHostConfigStore.getInitialState().saveQuickReplies })
    render(<QuickReplySettings hostId={H} />)

    // Never written: two moves in one tick. The first writes the defaults for
    // real at base revision 0; the second waits for it and builds on its result.
    fireEvent.click(screen.getByTestId('quick-reply-down-continue'))
    fireEvent.click(screen.getByTestId('quick-reply-down-continue'))
    await waitFor(() => expect(putHostConfig).toHaveBeenCalledTimes(2))
    expect(vi.mocked(putHostConfig).mock.calls.map((c) => [c[1], c[3], (c[2] as QuickReply[]).map((r) => r.id)])).toEqual([
      ['quick-replies', 0, ['run-tests', 'continue', 'explain']],
      ['quick-replies', 1, ['run-tests', 'explain', 'continue']],
    ])
    await waitFor(() => expect(rowIds()).toEqual(['run-tests', 'explain', 'continue']))
    expect(screen.queryByTestId('quick-replies-defaults')).toBeNull()
  })

  it('an emptied list shows the empty note, not the defaults', () => {
    seed(entry([], 2))
    render(<QuickReplySettings hostId={H} />)
    expect(rowIds()).toEqual([])
    expect(screen.getByTestId('quick-replies-empty')).toBeInTheDocument()
    expect(screen.queryByTestId('quick-replies-defaults')).toBeNull()
  })

  it('shows the unsupported note on an old daemon', () => {
    seed(entry([], 0, false))
    render(<QuickReplySettings hostId={H} />)
    expect(screen.getByTestId('quick-replies-unsupported')).toBeInTheDocument()
    expect(screen.queryByTestId('quick-reply-add')).toBeNull()
    expect(rowIds()).toEqual([])
  })

  it('is the third tab of the Commands section', () => {
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('commands-tab-quick'))
    expect(screen.getByTestId('quick-replies')).toBeInTheDocument()
    expect(screen.queryByTestId('command-add')).toBeNull()
  })
})

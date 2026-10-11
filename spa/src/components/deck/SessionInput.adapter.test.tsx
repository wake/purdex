// U4-2 (critic): the input's back-end is chosen by data. A conversation another back-end owns (a worker's: `capabilities.send` is `execution`) with
// no adapter bound for it must NOT fall back to the terminal's mod — nothing is sent through `hostSendPort`, and the input says it cannot send.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllAdapters } from '../../lib/conversations/deck-adapter'
import { clearAllDrafts } from '../../lib/conversations/draft-memory'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), upload: vi.fn() }))
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: mocks.fetch, agentUploadToPath: mocks.upload }))

const ui = (capabilities: unknown) => <SessionInput paneKey="p1" hostId="h" sessionId="s1" sessionCode="dev001" capabilities={capabilities as never} onSwitchToTerminal={() => {}} />

beforeEach(() => { mocks.fetch.mockReset(); mocks.upload.mockReset() })
afterEach(() => { cleanup(); clearAllAdapters(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues() })

describe('SessionInput picks its back-end by data', () => {
  it('an execution-owned conversation with nothing bound: disabled, and the terminal\'s send path is never touched', () => {
    render(ui({ send: 'execution' }))
    expect(screen.getByTestId('session-input-disabled')).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).toBeNull()
    expect(mocks.fetch).not.toHaveBeenCalled()
  })

  it('a terminal session whose mod says prompt is a normal input', () => {
    render(ui({ send: 'prompt' }))
    expect(screen.getByRole('textbox')).toBeInTheDocument()
    expect(screen.queryByTestId('session-input-disabled')).toBeNull()
  })

  it('a capability the terminal does not know is "cannot send" (it is not read as `prompt`)', () => {
    render(ui({ send: 'typed' }))
    expect(screen.getByTestId('session-input-disabled')).toBeInTheDocument()
  })
})

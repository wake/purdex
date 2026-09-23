import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import * as transferApi from '../../lib/host-transfer-api'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'
import { useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ShareHostsDialog } from './ShareHostsDialog'

const TRUST = /will hold the access tokens of the hosts you share, readable by that host, until the code is used or expires \(10 min\)\. Only relay through a host you trust\./

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    hosts: {
      relay: { id: 'relay', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: 'relay-tok', daemonId: 'd1_m' },
      air: { id: 'air', name: 'air26', ip: '100.64.0.4', port: 7860, order: 1, token: 'air-tok', icon: 'Laptop' },
      bare: { id: 'bare', name: 'tokenless', ip: '10.0.0.9', port: 7860, order: 2 },
    },
    hostOrder: ['relay', 'air', 'bare'],
    runtime: { relay: { status: 'connected' }, air: { status: 'disconnected' } },
    activeHostId: 'relay',
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ShareHostsDialog', () => {
  it('shows the trust sentence, naming the relay, before any code is created', () => {
    const create = vi.spyOn(transferApi, 'createTransfer')
    render(<ShareHostsDialog onClose={() => {}} />)
    const trust = screen.getByTestId('transfer-trust')
    expect(trust.textContent).toMatch(TRUST)
    expect(trust.textContent).toContain('mlab')
    expect(create).not.toHaveBeenCalled()
  })

  it('lists a host without a token disabled, and never sends it', async () => {
    const create = vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'ok', code: 'ABCD2345', expiresAt: Date.now() + 600_000 })
    render(<ShareHostsDialog onClose={() => {}} />)
    expect((screen.getByLabelText(/tokenless/) as HTMLInputElement).disabled).toBe(true)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create code' }))
    })
    expect(create).toHaveBeenCalledTimes(1)
    const [relay, rows] = create.mock.calls[0]
    expect(relay).toBe('relay')
    expect(rows.map((r) => r.name)).toEqual(['mlab', 'air26'])
    expect(rows[0]).toMatchObject({ token: 'relay-tok', daemonId: 'd1_m' })
    expect(rows[1]).toMatchObject({ token: 'air-tok', look: { icon: 'Laptop' } })
  })

  it('an unticked host is left out', async () => {
    const create = vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'ok', code: 'ABCD2345', expiresAt: 0 })
    render(<ShareHostsDialog onClose={() => {}} />)
    fireEvent.click(screen.getByLabelText(/air26/))
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create code' }))
    })
    expect(create.mock.calls[0][1].map((r) => r.name)).toEqual(['mlab'])
  })

  it('shows the code as ABCD-2345 with the relay name', async () => {
    vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'ok', code: 'ABCD2345', expiresAt: Date.now() + 600_000 })
    render(<ShareHostsDialog onClose={() => {}} />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create code' }))
    })
    expect(screen.getByTestId('transfer-code').textContent).toBe('ABCD-2345')
    expect(screen.getByTestId('transfer-code-meta').textContent).toContain('mlab')
  })

  it('a failure is shown in words', async () => {
    vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'failed', reason: 'capacity', status: 429 })
    render(<ShareHostsDialog onClose={() => {}} />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create code' }))
    })
    expect(screen.getByRole('alert').textContent).toMatch(/Too many open codes on mlab/)
  })

  it('offers only connected hosts as the relay; none connected → create disabled', () => {
    useHostStore.setState({ runtime: {} })
    render(<ShareHostsDialog onClose={() => {}} />)
    expect((screen.getByRole('button', { name: 'Create code' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText(/No connected host to relay through/)).toBeTruthy()
  })

  describe('the relay limits (32 rows, 64 KiB body)', () => {
    function manyHosts(n: number) {
      const hosts: Record<string, HostConfig> = {}
      const hostOrder: string[] = []
      for (let i = 1; i <= n; i++) {
        const id = `h${i}`
        hosts[id] = { id, name: `host-${String(i).padStart(2, '0')}`, ip: `10.0.0.${i}`, port: 7860, order: i - 1, token: `tok-${i}` }
        hostOrder.push(id)
      }
      useHostStore.setState({ hosts, hostOrder, runtime: { h1: { status: 'connected' } }, activeHostId: 'h1' })
    }
    const box = (i: number) => screen.getByLabelText(new RegExp(`host-${String(i).padStart(2, '0')}`)) as HTMLInputElement
    const create = () => screen.getByRole('button', { name: 'Create code' }) as HTMLButtonElement

    it('exports the daemon limits', () => {
      expect(transferApi.MAX_TRANSFER_ROWS).toBe(32)
      expect(transferApi.MAX_TRANSFER_BODY_BYTES).toBe(65536)
    })

    it('33 shareable hosts: only the first 32 start ticked, the limit is said, and create works with 32 rows', async () => {
      manyHosts(33)
      const spy = vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'ok', code: 'ABCD2345', expiresAt: 0 })
      render(<ShareHostsDialog onClose={() => {}} />)
      expect(box(1).checked).toBe(true)
      expect(box(32).checked).toBe(true)
      expect(box(33).checked).toBe(false)
      expect(screen.getByTestId('transfer-limit').textContent).toMatch(/at most 32 hosts at a time/)
      expect(create().disabled).toBe(false)
      await act(async () => {
        fireEvent.click(create())
      })
      expect(spy.mock.calls[0][1]).toHaveLength(32)
    })

    it('ticking the 33rd disables create and asks to untick 1', () => {
      manyHosts(33)
      const spy = vi.spyOn(transferApi, 'createTransfer')
      render(<ShareHostsDialog onClose={() => {}} />)
      fireEvent.click(box(33))
      expect(box(33).checked).toBe(true)
      expect(create().disabled).toBe(true)
      expect(screen.getByTestId('transfer-limit').textContent).toMatch(/at most 32 hosts at a time; untick 1/)
      fireEvent.click(create())
      expect(spy).not.toHaveBeenCalled()
      fireEvent.click(box(5))
      expect(create().disabled).toBe(false)
    })

    it('32 or fewer shareable hosts: no limit note', () => {
      manyHosts(32)
      render(<ShareHostsDialog onClose={() => {}} />)
      expect(screen.queryByTestId('transfer-limit')).toBeNull()
      expect(create().disabled).toBe(false)
    })

    it('a body estimated over 64 KiB disables create and says so', () => {
      const big = { id: 'big', name: 'big', ip: '10.0.0.99', port: 7860, order: 3, token: 'x'.repeat(70_000) }
      useHostStore.setState((st) => ({ hosts: { ...st.hosts, big }, hostOrder: [...st.hostOrder, 'big'] }))
      const spy = vi.spyOn(transferApi, 'createTransfer')
      render(<ShareHostsDialog onClose={() => {}} />)
      expect(create().disabled).toBe(true)
      expect(screen.getByTestId('transfer-limit').textContent).toMatch(/too large/)
      fireEvent.click(create())
      expect(spy).not.toHaveBeenCalled()
      fireEvent.click(screen.getByLabelText(/^big/))
      expect(create().disabled).toBe(false)
      expect(screen.queryByTestId('transfer-limit')).toBeNull()
    })
  })
})

describe('the code\'s expiry time is in the UI language, not the browser\'s', () => {
  const expiresAt = new Date(2026, 8, 24, 15, 17, 38).getTime()
  const realToLocaleTimeString = Date.prototype.toLocaleTimeString
  const fmt = (locale: string) => realToLocaleTimeString.call(new Date(expiresAt), locale)

  // Pretend the browser speaks `tag`: a call that names no locale gets `tag`'s format, as on the real machine.
  function browserSpeaks(tag: string) {
    vi.spyOn(Date.prototype, 'toLocaleTimeString').mockImplementation(function (this: Date, locales, options) {
      return realToLocaleTimeString.call(this, locales ?? tag, options)
    })
  }

  async function createCode(label: string) {
    vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'ok', code: 'ABCD2345', expiresAt })
    render(<ShareHostsDialog onClose={() => {}} />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: label }))
    })
    return screen.getByTestId('transfer-code-meta').textContent
  }

  afterEach(() => {
    useI18nStore.getState().setLocale('en')
  })

  it('English UI on a zh-TW browser: "Expires at" an English time', async () => {
    expect(fmt('en')).not.toBe(fmt('zh-TW'))
    browserSpeaks('zh-TW')
    useI18nStore.getState().setLocale('en')
    const meta = await createCode(en['hosts.transfer.create'])
    expect(meta).toBe(en['hosts.transfer.code_meta'].replace('{{time}}', fmt('en')).replace('{{relay}}', 'mlab'))
  })

  it('zh-TW UI on an English browser: a zh-TW time', async () => {
    browserSpeaks('en')
    useI18nStore.getState().setLocale('zh-TW')
    const meta = await createCode(zhTW['hosts.transfer.create'])
    expect(meta).toBe(zhTW['hosts.transfer.code_meta'].replace('{{time}}', fmt('zh-TW')).replace('{{relay}}', 'mlab'))
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { CompactionSection } from './CompactionSection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore, type HostConfigEntry } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostConfigConflictError, type CompactionDefaults } from '../../lib/host-config-api'

const H = 'h1'
const saveCompaction = vi.fn()
const DEFAULTS: CompactionDefaults = {
  solo: { auto: true, threshold: 70, idle: true },
  team: { auto: true, threshold: 70, idle: true },
}

function entry(compaction: CompactionDefaults, supported = true, status: HostConfigEntry['status'] = 'ready'): HostConfigEntry {
  const e = emptyHostConfigEntry(status)
  return { ...e, compaction, compactionSupported: supported, revisions: { ...e.revisions, compaction: 1 } }
}
function seed(e: HostConfigEntry) {
  useHostConfigStore.setState({ byHost: { [H]: e }, load: vi.fn(async () => {}), saveCompaction })
}

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  saveCompaction.mockReset().mockImplementation(async (hostId: string, items: CompactionDefaults) => {
    useHostConfigStore.setState((s) => {
      const cur = s.byHost[hostId]
      return { byHost: { ...s.byHost, [hostId]: { ...cur, compaction: items } } }
    })
  })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } },
  })
})

describe('CompactionSection', () => {
  it('shows both rows with the stored values and the note', () => {
    seed(entry({ ...DEFAULTS, team: { auto: false, threshold: 40, idle: false } }))
    render(<CompactionSection hostId={H} />)
    expect(screen.getByText('壓縮')).toBeTruthy()
    expect(screen.getByText('獨立 session')).toBeTruthy()
    expect(screen.getByText('team')).toBeTruthy()
    expect(screen.getByTestId('compaction-solo-auto').getAttribute('aria-checked')).toBe('true')
    expect(screen.getByTestId('compaction-team-auto').getAttribute('aria-checked')).toBe('false')
    expect(screen.getByTestId('compaction-solo-threshold-value').textContent).toContain('70')
    expect(screen.getByTestId('compaction-team-threshold-value').textContent).toContain('40')
    expect(screen.getByText('關掉自動壓縮時，context 快滿（約 97%）仍會由 Claude Code 自動壓縮。')).toBeTruthy()
  })

  it("team's idle switch PUTs the whole object with only that cell changed", async () => {
    seed(entry(DEFAULTS))
    render(<CompactionSection hostId={H} />)
    fireEvent.click(screen.getByTestId('compaction-team-idle'))
    await waitFor(() => expect(saveCompaction).toHaveBeenCalledWith(H, {
      solo: { auto: true, threshold: 70, idle: true },
      team: { auto: true, threshold: 70, idle: false },
    }))
  })

  it('+ moves the threshold from 70 to 75', async () => {
    seed(entry(DEFAULTS))
    render(<CompactionSection hostId={H} />)
    fireEvent.click(screen.getByTestId('compaction-solo-threshold-plus'))
    await waitFor(() => expect(saveCompaction).toHaveBeenCalledWith(H, {
      solo: { auto: true, threshold: 75, idle: true }, team: DEFAULTS.team,
    }))
  })

  // The first save is held open: the second press is queued behind it and must apply to what the first wrote.
  function holdFirst() {
    let release!: () => void
    const gate = new Promise<void>((r) => { release = r })
    const real = saveCompaction.getMockImplementation()!
    saveCompaction.mockImplementationOnce(async (h: string, items: CompactionDefaults) => { await gate; await real(h, items) })
    return release
  }

  it('two quick + presses with the first PUT pending send 80', async () => {
    seed(entry(DEFAULTS))
    const release = holdFirst()
    render(<CompactionSection hostId={H} />)
    fireEvent.click(screen.getByTestId('compaction-solo-threshold-plus'))
    fireEvent.click(screen.getByTestId('compaction-solo-threshold-plus'))
    release()
    await waitFor(() => expect(saveCompaction).toHaveBeenCalledTimes(2))
    expect(saveCompaction.mock.calls[0][1].solo.threshold).toBe(75)
    expect(saveCompaction.mock.calls[1][1].solo.threshold).toBe(80)
  })

  it('+ then − lands back on 70', async () => {
    seed(entry(DEFAULTS))
    const release = holdFirst()
    render(<CompactionSection hostId={H} />)
    fireEvent.click(screen.getByTestId('compaction-solo-threshold-plus'))
    fireEvent.click(screen.getByTestId('compaction-solo-threshold-minus'))
    release()
    await waitFor(() => expect(saveCompaction).toHaveBeenCalledTimes(2))
    expect(saveCompaction.mock.calls[1][1].solo.threshold).toBe(70)
  })

  it('a press on the other row in between keeps both rows right', async () => {
    seed(entry(DEFAULTS))
    const release = holdFirst()
    render(<CompactionSection hostId={H} />)
    fireEvent.click(screen.getByTestId('compaction-solo-threshold-plus'))
    fireEvent.click(screen.getByTestId('compaction-team-threshold-minus'))
    fireEvent.click(screen.getByTestId('compaction-solo-threshold-plus'))
    release()
    await waitFor(() => expect(saveCompaction).toHaveBeenCalledTimes(3))
    expect(saveCompaction.mock.calls[2][1]).toEqual({
      solo: { auto: true, threshold: 80, idle: true }, team: { auto: true, threshold: 65, idle: true },
    })
  })

  it('an earlier failure message is gone once a later queued save succeeds', async () => {
    seed(entry(DEFAULTS))
    saveCompaction.mockRejectedValueOnce(new HostConfigConflictError({ items: DEFAULTS, revision: 2 }))
    render(<CompactionSection hostId={H} />)
    fireEvent.click(screen.getByTestId('compaction-solo-idle'))
    fireEvent.click(screen.getByTestId('compaction-team-idle'))
    await waitFor(() => expect(saveCompaction).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByTestId('compaction-team-idle').getAttribute('aria-checked')).toBe('false'))
    expect(screen.queryByTestId('compaction-save-error')).toBeNull()
  })

  it('a malformed row shows the problem notice and still saves (as the relay section does)', async () => {
    seed({ ...entry(DEFAULTS), problems: { compaction: { kind: 'shape' } } })
    render(<CompactionSection hostId={H} />)
    expect(screen.getByTestId('host-config-problem')).toBeTruthy()
    fireEvent.click(screen.getByTestId('compaction-solo-idle'))
    await waitFor(() => expect(saveCompaction).toHaveBeenCalled())
  })

  it('the stepper is disabled while auto is off', () => {
    seed(entry({ ...DEFAULTS, solo: { auto: false, threshold: 70, idle: true } }))
    render(<CompactionSection hostId={H} />)
    expect((screen.getByTestId('compaction-solo-threshold-plus') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('compaction-solo-threshold-minus') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('compaction-team-threshold-plus') as HTMLButtonElement).disabled).toBe(false)
  })

  it('a 409 shows the conflict text', async () => {
    seed(entry(DEFAULTS))
    saveCompaction.mockRejectedValue(new HostConfigConflictError({ items: DEFAULTS, revision: 2 }))
    render(<CompactionSection hostId={H} />)
    fireEvent.click(screen.getByTestId('compaction-solo-idle'))
    await waitFor(() => expect(screen.getByTestId('compaction-save-error').textContent).toBe(useI18nStore.getState().t('host_config.conflict')))
  })

  it('a host without the compaction row shows the unsupported text', () => {
    seed(entry(DEFAULTS, false))
    render(<CompactionSection hostId={H} />)
    expect(screen.getByTestId('compaction-unsupported')).toBeTruthy()
    expect(screen.queryByTestId('compaction-solo-auto')).toBeNull()
  })

  it('is locked when the entry is not editable', () => {
    seed(entry(DEFAULTS, true, 'loading'))
    render(<CompactionSection hostId={H} />)
    expect(screen.getByTestId('compaction-solo-auto').closest('span.opacity-50')).toBeTruthy()
    fireEvent.click(screen.getByTestId('compaction-solo-auto'))
    expect(saveCompaction).not.toHaveBeenCalled()
  })
})

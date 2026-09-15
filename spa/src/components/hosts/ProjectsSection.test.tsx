import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { ProjectsSection } from './ProjectsSection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import * as api from '../../lib/host-config-api'
import { HostConfigApiError, type HostProject } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS } from '../../lib/host-config-validate'

vi.mock('../../lib/host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-config-api')>()),
  checkHostPath: vi.fn(async () => ({ status: 'dir', resolved: '/x' })),
}))

const H = 'h1'
const P1: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const P2: HostProject = { id: 'p2', name: 'Ploom', slug: 'ploom', path: '/missing' }
const saveProjects = vi.fn()

function seed(projects: HostProject[], status: 'ready' | 'unsupported' = 'ready') {
  useHostConfigStore.setState({
    byHost: { [H]: { ...emptyHostConfigEntry(status), projects } },
    load: vi.fn(async () => {}),
    saveProjects,
  })
}

beforeEach(() => {
  saveProjects.mockReset().mockImplementation(async (hostId: string, items: HostProject[]) => {
    useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], projects: items } } }))
  })
  vi.mocked(api.checkHostPath).mockImplementation(async (_h, path) =>
    ({ status: path === '/missing' ? 'missing' : 'dir', resolved: path }))
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
  })
  seed([P1, P2])
})

describe('ProjectsSection', () => {
  it('lists projects in order with name, slug, path and a per-row path status', async () => {
    render(<ProjectsSection hostId={H} />)
    const rows = screen.getAllByTestId(/^project-row-/)
    expect(rows.map((r) => r.getAttribute('data-testid'))).toEqual(['project-row-p1', 'project-row-p2'])
    expect(within(rows[0]).getByText('purdex')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('project-path-status-p2')).toHaveAttribute('data-status', 'missing'))
    expect(screen.getByTestId('project-path-status-p1')).toHaveAttribute('data-status', 'dir')
  })

  it('reorders with the down button and saves the new order', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-down-p1'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledWith(H, [P2, P1]))
    await waitFor(() => expect(screen.getByTestId('project-up-p1')).toBeEnabled())
    expect(screen.getByTestId('project-down-p1')).toBeDisabled()
  })

  it('a second row action fired before the first save lands keeps both intents', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-down-p1'))
    fireEvent.click(screen.getByTestId('project-delete-p2'))
    fireEvent.click(screen.getByTestId('project-delete-confirm-p2'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledTimes(2))
    expect(saveProjects.mock.calls[0][1]).toEqual([P2, P1])
    // Planned from the reordered list the first action left behind.
    expect(saveProjects.mock.calls[1][1]).toEqual([P1])
  })

  it('adds a project; slug follows the name until edited; client validation blocks bad input', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-add'))
    fireEvent.change(screen.getByTestId('project-field-name'), { target: { value: 'My App' } })
    expect(screen.getByTestId('project-field-slug')).toHaveValue('my-app')
    fireEvent.change(screen.getByTestId('project-field-slug'), { target: { value: 'purdex' } })
    fireEvent.change(screen.getByTestId('project-field-name'), { target: { value: 'My App 2' } })
    expect(screen.getByTestId('project-field-slug')).toHaveValue('purdex') // user-edited: no longer follows
    fireEvent.change(screen.getByTestId('project-field-path'), { target: { value: 'relative' } })
    fireEvent.click(screen.getByTestId('project-save'))
    expect(screen.getByTestId('project-error-slug')).toHaveTextContent('already used')
    expect(screen.getByTestId('project-error-path')).toBeInTheDocument()
    expect(saveProjects).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('project-field-slug'), { target: { value: 'my-app' } })
    fireEvent.change(screen.getByTestId('project-field-path'), { target: { value: '~/w/app' } })
    fireEvent.click(screen.getByTestId('project-save'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledTimes(1))
    const saved = saveProjects.mock.calls[0][1] as HostProject[]
    expect(saved).toHaveLength(3)
    expect(saved[2]).toMatchObject({ name: 'My App 2', slug: 'my-app', path: '~/w/app' })
    expect(saved[2].id).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
    await waitFor(() => expect(screen.queryByTestId('project-field-name')).not.toBeInTheDocument())
  })

  it('the dialog shows the live path check but never blocks saving on it', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-edit-p1'))
    fireEvent.change(screen.getByTestId('project-field-path'), { target: { value: '/missing' } })
    await waitFor(() => expect(screen.getByTestId('project-dialog-path-status')).toHaveAttribute('data-status', 'missing'))
    fireEvent.click(screen.getByTestId('project-save'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledWith(H, [{ ...P1, path: '/missing' }, P2]))
  })

  it('a daemon 400 keeps the dialog open and shows the body text inline (amendment A3)', async () => {
    saveProjects.mockRejectedValue(new HostConfigApiError(400, 'projects[0].path: must be absolute'))
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-edit-p1'))
    fireEvent.click(screen.getByTestId('project-save'))
    expect(await screen.findByTestId('project-dialog-error')).toHaveTextContent('projects[0].path: must be absolute')
    expect(screen.getByTestId('project-field-name')).toBeInTheDocument()
    expect(screen.queryByTestId('projects-save-error')).not.toBeInTheDocument()
  })

  it('deletes after confirmation', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-delete-p2'))
    fireEvent.click(screen.getByTestId('project-delete-confirm-p2'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledWith(H, [P1]))
  })

  // How a failure is WORDED is the shared hook's (useHostConfigCollection); what
  // this owns is where it is rendered.
  it('a daemon 400 on a row action shows the body text in the list', async () => {
    saveProjects.mockRejectedValue(new HostConfigApiError(400, 'duplicate slug'))
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-down-p1'))
    expect(await screen.findByTestId('projects-save-error')).toHaveTextContent('duplicate slug')
  })

  it('adding is disabled at the item limit', () => {
    seed(Array.from({ length: MAX_CONFIG_ITEMS }, (_, i) => ({ id: `p${i}`, name: `P${i}`, slug: `p${i}`, path: '/' })))
    render(<ProjectsSection hostId={H} />)
    expect(screen.getByTestId('project-add')).toBeDisabled()
    expect(screen.getByTestId('projects-limit')).toBeInTheDocument()
  })

  it('offline or old daemon → notice, editing disabled', () => {
    seed([P1], 'unsupported')
    const { unmount } = render(<ProjectsSection hostId={H} />)
    expect(screen.getByTestId('host-config-notice')).toHaveAttribute('data-notice', 'host_config.unsupported')
    expect(screen.getByTestId('project-add')).toBeDisabled()
    unmount()
    seed([P1])
    useHostStore.setState({ runtime: { [H]: { status: 'disconnected' } } })
    render(<ProjectsSection hostId={H} />)
    expect(screen.getByTestId('host-config-notice')).toHaveAttribute('data-notice', 'host_config.offline')
    expect(screen.getByTestId('project-edit-p1')).toBeDisabled()
  })
})
